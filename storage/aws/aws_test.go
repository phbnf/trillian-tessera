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

package aws

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"

	gcs "cloud.google.com/go/storage"
	"github.com/aws/smithy-go"
	"github.com/google/go-cmp/cmp"
	tessera "github.com/transparency-dev/trillian-tessera"
	"github.com/transparency-dev/trillian-tessera/api"
	"github.com/transparency-dev/trillian-tessera/api/layout"
	storage "github.com/transparency-dev/trillian-tessera/storage/internal"
	"k8s.io/klog"
)

var (
	mySQLURI            = flag.String("mysql_uri", "root:password@tcp(localhost:3306)/test_tessera", "Connection string for a MySQL database")
	isMySQLTestOptional = flag.Bool("is_mysql_test_optional", true, "Boolean value to control whether the MySQL test is optional")
)

const (
	// Matching public key: "transparency.dev/tessera/example+ae330e15+ASf4/L1zE859VqlfQgGzKy34l91Gl8W6wfwp+vKP62DW"
	testPrivateKey = "PRIVATE+KEY+transparency.dev/tessera/example+ae330e15+AXEwZQ2L6Ga3NX70ITObzyfEIketMr2o9Kc+ed/rt/QR"
)

// TestMain parses flags.
func TestMain(m *testing.M) {
	klog.InitFlags(nil)
	flag.Parse()
	os.Exit(m.Run())
}

// canSkipMySQLTest checks if the test MySQL db is available and if not, if the test can be skipped.
//
// Use this method before every MySQL test.
//
// If is_mysql_test_optional is set to true and MySQL database cannot be opened or pinged,
// the test will fail immediately.  Otherwise, the test will be skipped if the test is optional
// and the database is not available.
func canSkipMySQLTest(t *testing.T, ctx context.Context) bool {
	t.Helper()

	db, err := sql.Open("mysql", *mySQLURI)
	if err != nil {
		if *isMySQLTestOptional {
			return true
		}
		t.Fatalf("failed to open MySQL test db: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("failed to close MySQL database: %v", err)
		}
	}()
	if err := db.PingContext(ctx); err != nil {
		if *isMySQLTestOptional {
			return true
		}
		t.Fatalf("failed to ping MySQL test db: %v", err)
	}
	return false
}

func mustDropTables(t *testing.T, ctx context.Context) {
	t.Helper()

	db, err := sql.Open("mysql", *mySQLURI+"?multiStatements=true")
	if err != nil {
		t.Fatalf("failed to connect to db: %v", *mySQLURI)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatalf("failed to close db: %v", err)
		}
	}()

	if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS `Seq`, `SeqCoord`, `IntCoord`"); err != nil {
		t.Fatalf("failed to drop all tables: %v", err)
	}
}

func TestMySQLSequencerAssignEntries(t *testing.T) {
	ctx := context.Background()
	if canSkipMySQLTest(t, ctx) {
		klog.Warningf("MySQL not available, skipping %q", t.Name())
		return
	}
	// Clean tables in case there's something in the database.
	mustDropTables(t, ctx)
	// Clean up after yourself.
	defer mustDropTables(t, ctx)

	seq, err := newMySQLSequencer(ctx, *mySQLURI, 1000, 0, 0)
	if err != nil {
		t.Fatalf("newMySQLSequencer: %v", err)
	}

	want := uint64(0)
	for chunks := 0; chunks < 10; chunks++ {
		entries := []*tessera.Entry{}
		for i := 0; i < 10+chunks; i++ {
			entries = append(entries, tessera.NewEntry([]byte(fmt.Sprintf("item %d/%d", chunks, i))))
		}
		if err := seq.assignEntries(ctx, entries); err != nil {
			t.Fatalf("assignEntries: %v", err)
		}
		for i, e := range entries {
			if got := *e.Index(); got != want {
				t.Errorf("Chunk %d entry %d got seq %d, want %d", chunks, i, got, want)
			}
			want++
		}
	}
}

func TestMySQLSequencerPushback(t *testing.T) {
	ctx := context.Background()
	if canSkipMySQLTest(t, ctx) {
		klog.Warningf("MySQL not available, skipping %q", t.Name())
		return
	}
	// Clean tables in case there's something in the database.
	mustDropTables(t, ctx)
	// Clean up after yourself.
	defer mustDropTables(t, ctx)

	for _, test := range []struct {
		name           string
		threshold      uint64
		initialEntries int
		wantPushback   bool
	}{
		{
			name:           "no pushback: num < threshold",
			threshold:      10,
			initialEntries: 5,
		},
		{
			name:           "no pushback: num = threshold",
			threshold:      10,
			initialEntries: 10,
		},
		{
			name:           "pushback: initial > threshold",
			threshold:      10,
			initialEntries: 15,
			wantPushback:   true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mustDropTables(t, ctx)

			seq, err := newMySQLSequencer(ctx, *mySQLURI, test.threshold, 0, 0)
			if err != nil {
				t.Fatalf("newMySQLSequencer: %v", err)
			}
			// Set up the test scenario with the configured number of initial outstanding entries
			entries := []*tessera.Entry{}
			for i := 0; i < test.initialEntries; i++ {
				entries = append(entries, tessera.NewEntry([]byte(fmt.Sprintf("initial item %d", i))))
			}
			if err := seq.assignEntries(ctx, entries); err != nil {
				t.Fatalf("initial assignEntries: %v", err)
			}

			// Now perform the test with a single additional entry to check for pushback
			entries = []*tessera.Entry{tessera.NewEntry([]byte("additional"))}
			err = seq.assignEntries(ctx, entries)
			if gotPushback := errors.Is(err, tessera.ErrPushback); gotPushback != test.wantPushback {
				t.Fatalf("assignEntries: got pushback %t (%v), want pushback: %t", gotPushback, err, test.wantPushback)
			} else if !gotPushback && err != nil {
				t.Fatalf("assignEntries: %v", err)
			}
		})
	}
}

func TestMySQLSequencerRoundTrip(t *testing.T) {
	ctx := context.Background()
	if canSkipMySQLTest(t, ctx) {
		klog.Warningf("MySQL not available, skipping %q", t.Name())
		return
	}
	// Clean tables in case there's something in the database.
	mustDropTables(t, ctx)
	// Clean up after yourself.
	defer mustDropTables(t, ctx)

	s, err := newMySQLSequencer(ctx, *mySQLURI, 1000, 0, 0)
	if err != nil {
		t.Fatalf("newMySQLSequencer: %v", err)
	}

	seq := 0
	wantEntries := []storage.SequencedEntry{}
	for chunks := 0; chunks < 10; chunks++ {
		entries := []*tessera.Entry{}
		for i := 0; i < 10+chunks; i++ {
			e := tessera.NewEntry([]byte(fmt.Sprintf("item %d", seq)))
			entries = append(entries, e)
			wantEntries = append(wantEntries, storage.SequencedEntry{
				BundleData: e.MarshalBundleData(uint64(seq)),
				LeafHash:   e.LeafHash(),
			})
			seq++
		}
		if err := s.assignEntries(ctx, entries); err != nil {
			t.Fatalf("assignEntries: %v", err)
		}
	}

	seenIdx := uint64(0)
	f := func(_ context.Context, fromSeq uint64, entries []storage.SequencedEntry) error {
		if fromSeq != seenIdx {
			return fmt.Errorf("f called with fromSeq %d, want %d", fromSeq, seenIdx)
		}
		for i, e := range entries {

			if got, want := e, wantEntries[i]; !reflect.DeepEqual(got, want) {
				return fmt.Errorf("entry %d+%d != %d", fromSeq, i, seenIdx)
			}
			seenIdx++
		}
		return nil
	}

	more, err := s.consumeEntries(ctx, 7, f, false)
	if err != nil {
		t.Errorf("consumeEntries: %v", err)
	}
	if !more {
		t.Errorf("more: false, expected true")
	}
}

func makeTile(t *testing.T, size uint64) *api.HashTile {
	t.Helper()
	r := &api.HashTile{Nodes: make([][]byte, size)}
	for i := uint64(0); i < size; i++ {
		h := sha256.Sum256([]byte(fmt.Sprintf("%d", i)))
		r.Nodes[i] = h[:]
	}
	return r
}

func TestTileRoundtrip(t *testing.T) {
	ctx := context.Background()
	m := newMemObjStore()
	s := &Storage{
		objStore: m,
	}

	for _, test := range []struct {
		name     string
		level    uint64
		index    uint64
		logSize  uint64
		tileSize uint64
	}{
		{
			name:     "ok",
			level:    0,
			index:    3 * 256,
			logSize:  3*256 + 20,
			tileSize: 20,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			wantTile := makeTile(t, test.tileSize)
			if err := s.setTile(ctx, test.level, test.index, test.logSize, wantTile); err != nil {
				t.Fatalf("setTile: %v", err)
			}

			expPath := layout.TilePath(test.level, test.index, test.logSize)
			_, ok := m.mem[expPath]
			if !ok {
				t.Fatalf("want tile at %v but found none", expPath)
			}

			got, err := s.getTiles(ctx, []storage.TileID{{Level: test.level, Index: test.index}}, test.logSize)
			if err != nil {
				t.Fatalf("getTile: %v", err)
			}
			if !cmp.Equal(got[0], wantTile) {
				t.Fatal("roundtrip returned different data")
			}
		})
	}
}

func makeBundle(t *testing.T, size uint64) []byte {
	t.Helper()
	r := &bytes.Buffer{}
	for i := uint64(0); i < size; i++ {
		e := tessera.NewEntry([]byte(fmt.Sprintf("%d", i)))
		if _, err := r.Write(e.MarshalBundleData(i)); err != nil {
			t.Fatalf("MarshalBundleEntry: %v", err)
		}
	}
	return r.Bytes()
}

func TestBundleRoundtrip(t *testing.T) {
	ctx := context.Background()
	m := newMemObjStore()
	s := &Storage{
		objStore:    m,
		entriesPath: layout.EntriesPath,
	}

	for _, test := range []struct {
		name       string
		index      uint64
		logSize    uint64
		bundleSize uint64
	}{
		{
			name:       "ok",
			index:      3 * 256,
			logSize:    3*256 + 20,
			bundleSize: 20,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			wantBundle := makeBundle(t, test.bundleSize)
			if err := s.setEntryBundle(ctx, test.index, test.logSize, wantBundle); err != nil {
				t.Fatalf("setEntryBundle: %v", err)
			}

			expPath := layout.EntriesPath(test.index, test.logSize)
			_, ok := m.mem[expPath]
			if !ok {
				t.Fatalf("want bundle at %v but found none", expPath)
			}

			got, err := s.getEntryBundle(ctx, test.index, test.logSize)
			if err != nil {
				t.Fatalf("getEntryBundle: %v", err)
			}
			if !cmp.Equal(got, wantBundle) {
				t.Fatal("roundtrip returned different data")
			}
		})
	}
}

type memObjStore struct {
	sync.RWMutex
	mem map[string][]byte
}

func newMemObjStore() *memObjStore {
	return &memObjStore{
		mem: make(map[string][]byte),
	}
}

func (m *memObjStore) getObject(_ context.Context, obj string) ([]byte, error) {
	m.RLock()
	defer m.RUnlock()

	d, ok := m.mem[obj]
	if !ok {
		return nil, fmt.Errorf("obj %q not found: %w", obj, gcs.ErrObjectNotExist)
	}
	return d, nil
}

// TODO(phboneff): add content type tests
func (m *memObjStore) setObject(_ context.Context, obj string, data []byte, _ string) error {
	m.Lock()
	defer m.Unlock()
	m.mem[obj] = data
	return nil
}

// TODO(phboneff): add content type tests
func (m *memObjStore) setObjectIfNoneMatch(_ context.Context, obj string, data []byte, _ string) error {
	m.Lock()
	defer m.Unlock()

	d, ok := m.mem[obj]
	if ok && !bytes.Equal(d, data) {
		return &smithy.GenericAPIError{Code: "PreconditionFailed"}
	}
	m.mem[obj] = data
	return nil
}
