// Copyright 2024 The Tessera authors. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// hammer is a tool to load test a Tessera log.
package main

import (
	"context"
	crand "crypto/rand"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/transparency-dev/trillian-tessera/client"
	"golang.org/x/mod/sumdb/note"
	"k8s.io/klog/v2"
)

func init() {
	flag.Var(&logURL, "log_url", "Log storage root URL (can be specified multiple times), e.g. https://log.server/and/path/")
}

var (
	logURL multiStringFlag

	logPubKey = flag.String("log_public_key", os.Getenv("TILES_LOG_PUBLIC_KEY"), "Public key for the log. This is defaulted to the environment variable TILES_LOG_PUBLIC_KEY")

	maxReadOpsPerSecond = flag.Int("max_read_ops", 20, "The maximum number of read operations per second")
	numReadersRandom    = flag.Int("num_readers_random", 4, "The number of readers looking for random leaves")
	numReadersFull      = flag.Int("num_readers_full", 4, "The number of readers downloading the whole log")

	maxWriteOpsPerSecond = flag.Int("max_write_ops", 0, "The maximum number of write operations per second")
	numWriters           = flag.Int("num_writers", 0, "The number of independent write tasks to run")

	leafMinSize = flag.Int("leaf_min_size", 0, "Minimum size in bytes of individual leaves")

	showUI = flag.Bool("show_ui", true, "Set to false to disable the text-based UI")

	hc = &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        256,
			MaxIdleConnsPerHost: 256,
			DisableKeepAlives:   false,
		},
		Timeout: 5 * time.Second,
	}
)

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	ctx := context.Background()

	logSigV, err := note.NewVerifier(*logPubKey)
	if err != nil {
		klog.Exitf("failed to create verifier: %v", err)
	}

	f, w := newLogClientsFromFlags()

	var cpRaw []byte
	cons := client.UnilateralConsensus(f.Fetch)
	tracker, err := client.NewLogStateTracker(ctx, f.Fetch, cpRaw, logSigV, logSigV.Name(), cons)
	if err != nil {
		klog.Exitf("Failed to create LogStateTracker: %v", err)
	}
	// Fetch initial state of log
	_, _, _, err = tracker.Update(ctx)
	if err != nil {
		klog.Exitf("Failed to get initial state of the log: %v", err)
	}

	hammer := NewHammer(&tracker, f.Fetch, w.Write)
	hammer.Run(ctx)

	if *showUI {
		hostUI(ctx, hammer)
	} else {
		<-ctx.Done()
	}
}

func NewHammer(tracker *client.LogStateTracker, f client.Fetcher, w LeafWriter) *Hammer {
	readThrottle := NewThrottle(*maxReadOpsPerSecond)
	writeThrottle := NewThrottle(*maxWriteOpsPerSecond)
	errChan := make(chan error, 20)

	gen := newLeafGenerator(tracker.LatestConsistent.Size, *leafMinSize)
	randomReaders := newWorkerPool(func() worker {
		return NewLeafReader(tracker, f, RandomNextLeaf(), readThrottle.tokenChan, errChan)
	})
	fullReaders := newWorkerPool(func() worker {
		return NewLeafReader(tracker, f, MonotonicallyIncreasingNextLeaf(), readThrottle.tokenChan, errChan)
	})
	writers := newWorkerPool(func() worker { return NewLogWriter(w, gen, writeThrottle.tokenChan, errChan) })

	return &Hammer{
		randomReaders: randomReaders,
		fullReaders:   fullReaders,
		writers:       writers,
		readThrottle:  readThrottle,
		writeThrottle: writeThrottle,
		tracker:       tracker,
		errChan:       errChan,
	}
}

type Hammer struct {
	randomReaders workerPool
	fullReaders   workerPool
	writers       workerPool
	readThrottle  *Throttle
	writeThrottle *Throttle
	tracker       *client.LogStateTracker
	errChan       chan error
}

func (h *Hammer) Run(ctx context.Context) {
	// Kick off readers & writers
	for i := 0; i < *numReadersRandom; i++ {
		h.randomReaders.Grow(ctx)
	}
	for i := 0; i < *numReadersFull; i++ {
		h.fullReaders.Grow(ctx)
	}
	for i := 0; i < *numWriters; i++ {
		h.writers.Grow(ctx)
	}

	// Set up logging for any errors
	go func() {
		for {
			select {
			case <-ctx.Done(): //context cancelled
				return
			case err := <-h.errChan:
				klog.Warning(err)
			}
		}
	}()

	// Start the throttles
	go h.readThrottle.Run(ctx)
	go h.writeThrottle.Run(ctx)

	go func() {
		tick := time.NewTicker(1 * time.Second)
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				size := h.tracker.LatestConsistent.Size
				_, _, _, err := h.tracker.Update(ctx)
				if err != nil {
					klog.Warning(err)
					inconsistentErr := client.ErrInconsistency{}
					if errors.As(err, &inconsistentErr) {
						klog.Fatalf("Last Good Checkpoint:\n%s\n\nFirst Bad Checkpoint:\n%s\n\n%v", string(inconsistentErr.SmallerRaw), string(inconsistentErr.LargerRaw), inconsistentErr)
					}
				}
				newSize := h.tracker.LatestConsistent.Size
				if newSize > size {
					klog.V(1).Infof("Updated checkpoint from %d to %d", size, newSize)
				}
			}
		}
	}()
}

func genLeaf(n uint64, minLeafSize int) []byte {
	// Make a slice with half the number of requested bytes since we'll
	// hex-encode them below which gets us back up to the full amount.
	filler := make([]byte, minLeafSize/2)
	_, _ = crand.Read(filler)
	return []byte(fmt.Sprintf("%x %d", filler, n))
}

func newLeafGenerator(n uint64, minLeafSize int) func() []byte {
	const dupChance = 0.1
	nextLeaf := genLeaf(n, minLeafSize)
	return func() []byte {
		if rand.Float64() <= dupChance {
			// This one will actually be unique, but the next iteration will
			// duplicate it. In future, this duplication could be randomly
			// selected to include really old leaves too, to test long-term
			// deduplication in the log (if it supports  that).
			return nextLeaf
		}

		n++
		r := nextLeaf
		nextLeaf = genLeaf(n, minLeafSize)
		return r
	}
}

func NewThrottle(opsPerSecond int) *Throttle {
	return &Throttle{
		opsPerSecond: opsPerSecond,
		tokenChan:    make(chan bool, opsPerSecond),
	}
}

type Throttle struct {
	opsPerSecond int
	tokenChan    chan bool

	oversupply int
}

func (t *Throttle) Increase() {
	tokenCount := t.opsPerSecond
	delta := float64(tokenCount) * 0.1
	if delta < 1 {
		delta = 1
	}
	t.opsPerSecond = tokenCount + int(delta)
}

func (t *Throttle) Decrease() {
	tokenCount := t.opsPerSecond
	if tokenCount <= 1 {
		return
	}
	delta := float64(tokenCount) * 0.1
	if delta < 1 {
		delta = 1
	}
	t.opsPerSecond = tokenCount - int(delta)
}

func (t *Throttle) Run(ctx context.Context) {
	interval := time.Second
	ticker := time.NewTicker(interval)
	for {
		select {
		case <-ctx.Done(): //context cancelled
			return
		case <-ticker.C:
			tokenCount := t.opsPerSecond
			timeout := time.After(interval)
		Loop:
			for i := 0; i < t.opsPerSecond; i++ {
				select {
				case t.tokenChan <- true:
					tokenCount--
				case <-timeout:
					break Loop
				}
			}
			t.oversupply = tokenCount
		}
	}
}

func (t *Throttle) String() string {
	return fmt.Sprintf("Current max: %d/s. Oversupply in last second: %d", t.opsPerSecond, t.oversupply)
}

func hostUI(ctx context.Context, hammer *Hammer) {
	grid := tview.NewGrid()
	grid.SetRows(3, 0, 10).SetColumns(0).SetBorders(true)
	// Status box
	statusView := tview.NewTextView()
	grid.AddItem(statusView, 0, 0, 1, 1, 0, 0, false)
	// Log view box
	logView := tview.NewTextView()
	logView.ScrollToEnd()
	logView.SetMaxLines(10000)
	grid.AddItem(logView, 1, 0, 1, 1, 0, 0, false)
	if err := flag.Set("logtostderr", "false"); err != nil {
		klog.Exitf("Failed to set flag: %v", err)
	}
	if err := flag.Set("alsologtostderr", "false"); err != nil {
		klog.Exitf("Failed to set flag: %v", err)
	}
	klog.SetOutput(logView)

	helpView := tview.NewTextView()
	helpView.SetText("+/- to increase/decrease read load\n>/< to increase/decrease write load\nw/W to increase/decrease workers")
	grid.AddItem(helpView, 2, 0, 1, 1, 0, 0, false)

	app := tview.NewApplication()
	ticker := time.NewTicker(1 * time.Second)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				text := fmt.Sprintf("Read: %s\nWrite: %s", hammer.readThrottle.String(), hammer.writeThrottle.String())
				statusView.SetText(text)
				app.Draw()
			}
		}
	}()
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Rune() {
		case '+':
			klog.Info("Increasing the read operations per second")
			hammer.readThrottle.Increase()
		case '-':
			klog.Info("Decreasing the read operations per second")
			hammer.readThrottle.Decrease()
		case '>':
			klog.Info("Increasing the write operations per second")
			hammer.writeThrottle.Increase()
		case '<':
			klog.Info("Decreasing the write operations per second")
			hammer.writeThrottle.Decrease()
		case 'w':
			klog.Info("Increasing the number of workers")
			hammer.randomReaders.Grow(ctx)
			hammer.fullReaders.Grow(ctx)
			hammer.writers.Grow(ctx)
		case 'W':
			klog.Info("Decreasing the number of workers")
			hammer.randomReaders.Shrink(ctx)
			hammer.fullReaders.Shrink(ctx)
			hammer.writers.Shrink(ctx)
		}
		return event
	})
	// logView.SetChangedFunc(func() {
	// 	app.Draw()
	// })
	if err := app.SetRoot(grid, true).Run(); err != nil {
		panic(err)
	}
}

// multiStringFlag allows a flag to be specified multiple times on the command
// line, and stores all of these values.
type multiStringFlag []string

func (ms *multiStringFlag) String() string {
	return strings.Join(*ms, ",")
}

func (ms *multiStringFlag) Set(w string) error {
	*ms = append(*ms, w)
	return nil
}