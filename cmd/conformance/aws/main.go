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

// gcp is a simple personality allowing to run conformance/compliance/performance tests and showing how to use the Tessera AWS storage implmentation.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"time"

	tessera "github.com/transparency-dev/trillian-tessera"
	"github.com/transparency-dev/trillian-tessera/storage/aws"
	"golang.org/x/mod/sumdb/note"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"k8s.io/klog/v2"
)

var (
	bucket            = flag.String("bucket", "", "Bucket to use for storing log")
	listen            = flag.String("listen", ":2024", "Address:port to listen on")
	dbUser            = flag.String("db_user", "", "AuroraDB user")
	dbPassword        = flag.String("db_password", "", "AuroraDB user")
	dbName            = flag.String("db_name", "", "AuroraDB name")
	dbHost            = flag.String("db_host", "", "AuroraDB host")
	dbPort            = flag.Int("db_port", 3306, "AuroraDB port")
	dbMaxConns        = flag.Int("db_max_conns", 0, "Maximum connections to the database, defaults to 0, i.e unlimited")
	dbMaxIdle         = flag.Int("db_max_idle_conns", 2, "Maximum idle database connections in the connection pool, defaults to 2")
	signer            = flag.String("signer", "", "Note signer to use to sign checkpoints")
	additionalSigners = []string{}
)

func init() {
	flag.Func("additional_signer", "Additional note signer for checkpoints, may be specified multiple times", func(s string) error {
		additionalSigners = append(additionalSigners, s)
		return nil
	})
}

func main() {
	klog.InitFlags(nil)
	flag.Parse()
	ctx := context.Background()

	s, a := signerFromFlags()

	// Create our Tessera storage backend:
	gcpCfg := storageConfigFromFlags()
	storage, err := aws.New(ctx, gcpCfg,
		tessera.WithCheckpointSigner(s, a...),
		tessera.WithBatching(1024, time.Second),
		tessera.WithPushback(10*4096),
	)
	if err != nil {
		klog.Exitf("Failed to create new GCP storage: %v", err)
	}

	// Expose a HTTP handler for the conformance test writes.
	// This should accept arbitrary bytes POSTed to /add, and return an ascii
	// decimal representation of the index assigned to the entry.
	http.HandleFunc("POST /add", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		idx, err := storage.Add(r.Context(), tessera.NewEntry(b))()
		if err != nil {
			if errors.Is(err, tessera.ErrPushback) {
				w.Header().Add("Retry-After", "1")
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(err.Error()))
			return
		}
		// Write out the assigned index
		_, _ = w.Write([]byte(fmt.Sprintf("%d", idx)))
	})

	h2s := &http2.Server{}
	h1s := &http.Server{
		Addr:    *listen,
		Handler: h2c.NewHandler(http.DefaultServeMux, h2s),
	}

	if err := h1s.ListenAndServe(); err != nil {
		klog.Exitf("ListenAndServe: %v", err)
	}
}

// storageConfigFromFlags returns an aws.Config struct populated with values
// provided via flags.
func storageConfigFromFlags() aws.Config {
	if *bucket == "" {
		klog.Exit("--bucket must be set")
	}
	if *dbName == "" {
		klog.Exit("--db_name must be set")
	}
	if *dbUser == "" {
		klog.Exit("--db_user must be set")
	}
	if *dbHost == "" {
		klog.Exit("--db_host must be set")
	}
	if *dbPort == 0 {
		klog.Exit("--db_port must be set")
	}

	var dbEndpoint string = fmt.Sprintf("%s:%d", *dbHost, *dbPort)

	dsn := fmt.Sprintf("%s:%s@tcp(%s)/%s?allowCleartextPasswords=true",
		*dbUser, *dbPassword, dbEndpoint, *dbName,
	)
	return aws.Config{
		Bucket:       *bucket,
		AuroraDSN:    dsn,
		MaxOpenConns: *dbMaxConns,
		MaxIdleConns: *dbMaxIdle,
	}
}

func signerFromFlags() (note.Signer, []note.Signer) {
	s, err := note.NewSigner(*signer)
	if err != nil {
		klog.Exitf("Failed to create new signer: %v", err)
	}

	var a []note.Signer
	for _, as := range additionalSigners {
		s, err := note.NewSigner(as)
		if err != nil {
			klog.Exitf("Failed to create additional signer: %v", err)
		}
		a = append(a, s)
	}

	return s, a
}
