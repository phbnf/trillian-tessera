// Copyright 2016 Google LLC. All Rights Reserved.
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

// The ct_server binary runs the CT personality.
package main

import (
	"context"
	"crypto"
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/google/trillian/crypto/keys"
	"github.com/google/trillian/crypto/keys/der"
	"github.com/google/trillian/crypto/keys/pem"
	"github.com/google/trillian/crypto/keys/pkcs11"
	"github.com/google/trillian/crypto/keyspb"
	"github.com/google/trillian/monitoring/opencensus"
	"github.com/google/trillian/monitoring/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/cors"
	tessera "github.com/transparency-dev/trillian-tessera"
	"github.com/transparency-dev/trillian-tessera/personalities/sctfe"
	"github.com/transparency-dev/trillian-tessera/personalities/sctfe/modules/dedup"
	"github.com/transparency-dev/trillian-tessera/personalities/sctfe/storage/bbolt"
	gcpSCTFE "github.com/transparency-dev/trillian-tessera/personalities/sctfe/storage/gcp"
	gcpTessera "github.com/transparency-dev/trillian-tessera/storage/gcp"
	"golang.org/x/mod/sumdb/note"
	"google.golang.org/protobuf/proto"
	"k8s.io/klog/v2"
)

// Global flags that affect all log instances.
var (
	httpEndpoint       = flag.String("http_endpoint", "localhost:6962", "Endpoint for HTTP (host:port)")
	tlsCert            = flag.String("tls_certificate", "", "Path to server TLS certificate")
	tlsKey             = flag.String("tls_key", "", "Path to server TLS private key")
	metricsEndpoint    = flag.String("metrics_endpoint", "", "Endpoint for serving metrics; if left empty, metrics will be visible on --http_endpoint")
	rpcDeadline        = flag.Duration("rpc_deadline", time.Second*10, "Deadline for backend RPC requests")
	logConfig          = flag.String("log_config", "", "File holding log config in text proto format")
	maskInternalErrors = flag.Bool("mask_internal_errors", false, "Don't return error strings with Internal Server Error HTTP responses")
	tracing            = flag.Bool("tracing", false, "If true opencensus Stackdriver tracing will be enabled. See https://opencensus.io/.")
	tracingProjectID   = flag.String("tracing_project_id", "", "project ID to pass to stackdriver. Can be empty for GCP, consult docs for other platforms.")
	tracingPercent     = flag.Int("tracing_percent", 0, "Percent of requests to be traced. Zero is a special case to use the DefaultSampler")
	pkcs11ModulePath   = flag.String("pkcs11_module_path", "", "Path to the PKCS#11 module to use for keys that use the PKCS#11 interface")
	dedupPath          = flag.String("dedup_path", "", "Path to the deduplication database")
	origin             = flag.String("origin", "", "origin of the log, for checkpoints and the monitoring prefix")
	projectID          = flag.String("project_id", "", "origin of the log, for checkpoints and the monitoring prefix")
	bucket             = flag.String("bucket", "", "name of the bucket to store the log in")
	spannerDB          = flag.String("spanner_db_path", "", "projects/{projectId}/instances/{instanceId}/databases/{databaseId}")
	rootsPemFile       = flag.String("roots_pem_file", "", "Path to the file containing root certificates that are acceptable to the log. The certs are served through get-roots endpoint.")
	rejectExpired      = flag.Bool("reject_expired", false, "if true then the certificate validity period will be checked against the current time during the validation of submissions. This will cause expired certificates to be rejected.")
	rejectUnexpired    = flag.Bool("reject_unexpired", false, "If reject_unexpired is true then CTFE rejects certificates that are either currently valid or not yet valid.")
	extKeyUsages       = flag.String("ext_key_usages", "", "If set, ext_key_usages will restrict the set of such usages that the server will accept. By default all are accepted. The values specified must be ones known to the x509 package.")
	rejectExtensions   = flag.String("reject_extension", "", "A list of X.509 extension OIDs, in dotted string form (e.g. '2.3.4.5') which, if present, should cause submissions to be rejected.")
)

// nolint:staticcheck
func main() {
	klog.InitFlags(nil)
	flag.Parse()
	ctx := context.Background()

	keys.RegisterHandler(&keyspb.PEMKeyFile{}, pem.FromProto)
	keys.RegisterHandler(&keyspb.PrivateKey{}, der.FromProto)
	keys.RegisterHandler(&keyspb.PKCS11Config{}, func(ctx context.Context, pb proto.Message) (crypto.Signer, error) {
		if cfg, ok := pb.(*keyspb.PKCS11Config); ok {
			return pkcs11.FromConfig(*pkcs11ModulePath, cfg)
		}
		return nil, fmt.Errorf("pkcs11: got %T, want *keyspb.PKCS11Config", pb)
	})

	cfg, err := sctfe.LogConfigFromFile(*logConfig)
	if err != nil {
		klog.Exitf("Failed to read config: %v", err)
	}

	vCfg, err := sctfe.ValidateLogConfig(cfg, *origin, *projectID, *bucket, *spannerDB, *rootsPemFile, *rejectExpired, *rejectUnexpired, *extKeyUsages, *rejectExtensions)
	if err != nil {
		klog.Exitf("Invalid config: %v", err)
	}

	klog.CopyStandardLogTo("WARNING")
	klog.Info("**** CT HTTP Server Starting ****")

	metricsAt := *metricsEndpoint
	if metricsAt == "" {
		metricsAt = *httpEndpoint
	}

	// Allow cross-origin requests to all handlers registered on corsMux.
	// This is safe for CT log handlers because the log is public and
	// unauthenticated so cross-site scripting attacks are not a concern.
	corsMux := http.NewServeMux()
	corsHandler := cors.AllowAll().Handler(corsMux)
	http.Handle("/", corsHandler)

	// Register handlers for all the configured logs using the correct RPC
	// client.
	// TODO(phboneff): setupAndRegister can probably be inlined / removed later
	_, err = setupAndRegister(ctx,
		*rpcDeadline,
		vCfg,
		corsMux,
		*maskInternalErrors,
	)
	if err != nil {
		klog.Exitf("Failed to set up log instance for %+v: %v", vCfg, err)
	}

	// Return a 200 on the root, for GCE default health checking :/
	corsMux.HandleFunc("/", func(resp http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/" {
			resp.WriteHeader(http.StatusOK)
		} else {
			resp.WriteHeader(http.StatusNotFound)
		}
	})

	// Export a healthz target.
	corsMux.HandleFunc("/healthz", func(resp http.ResponseWriter, req *http.Request) {
		// TODO(al): Wire this up to tell the truth.
		if _, err := resp.Write([]byte("ok")); err != nil {
			klog.Errorf("resp.Write(): %v", err)
		}
	})

	if metricsAt != *httpEndpoint {
		// Run a separate handler for metrics.
		go func() {
			mux := http.NewServeMux()
			mux.Handle("/metrics", promhttp.Handler())
			metricsServer := http.Server{Addr: metricsAt, Handler: mux}
			err := metricsServer.ListenAndServe()
			klog.Warningf("Metrics server exited: %v", err)
		}()
	} else {
		// Handle metrics on the DefaultServeMux.
		http.Handle("/metrics", promhttp.Handler())
	}

	// If we're enabling tracing we need to use an instrumented http.Handler.
	var handler http.Handler
	if *tracing {
		handler, err = opencensus.EnableHTTPServerTracing(*tracingProjectID, *tracingPercent)
		if err != nil {
			klog.Exitf("Failed to initialize stackdriver / opencensus tracing: %v", err)
		}
	}

	// Bring up the HTTP server and serve until we get a signal not to.
	srv := http.Server{}
	if *tlsCert != "" && *tlsKey != "" {
		cert, err := tls.LoadX509KeyPair(*tlsCert, *tlsKey)
		if err != nil {
			klog.Errorf("failed to load TLS certificate/key: %v", err)
		}
		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		srv = http.Server{Addr: *httpEndpoint, Handler: handler, TLSConfig: tlsConfig}
	} else {
		srv = http.Server{Addr: *httpEndpoint, Handler: handler}
	}
	shutdownWG := new(sync.WaitGroup)
	go awaitSignal(func() {
		shutdownWG.Add(1)
		defer shutdownWG.Done()
		// Allow 60s for any pending requests to finish then terminate any stragglers
		// TODO(phboneff): maybe wait for the sequencer queue to be empty?
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*60)
		defer cancel()
		klog.Info("Shutting down HTTP server...")
		if err := srv.Shutdown(ctx); err != nil {
			klog.Errorf("srv.Shutdown(): %v", err)
		}
		klog.Info("HTTP server shutdown")
	})

	if *tlsCert != "" && *tlsKey != "" {
		err = srv.ListenAndServeTLS("", "")
	} else {
		err = srv.ListenAndServe()
	}
	if err != http.ErrServerClosed {
		klog.Warningf("Server exited: %v", err)
	}
	// Wait will only block if the function passed to awaitSignal was called,
	// in which case it'll block until the HTTP server has gracefully shutdown
	shutdownWG.Wait()
	klog.Flush()
}

// awaitSignal waits for standard termination signals, then runs the given
// function; it should be run as a separate goroutine.
func awaitSignal(doneFn func()) {
	// Arrange notification for the standard set of signals used to terminate a server
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// Now block main and wait for a signal
	sig := <-sigs
	klog.Warningf("Signal received: %v", sig)
	klog.Flush()

	doneFn()
}

func setupAndRegister(ctx context.Context, deadline time.Duration, vCfg *sctfe.ValidatedLogConfig, mux *http.ServeMux, maskInternalErrors bool) (*sctfe.Instance, error) {
	opts := sctfe.InstanceOptions{
		Validated:          vCfg,
		Deadline:           deadline,
		MetricFactory:      prometheus.MetricFactory{},
		RequestLog:         new(sctfe.DefaultRequestLog),
		MaskInternalErrors: maskInternalErrors,
		CreateStorage:      newGCPStorage,
	}

	inst, err := sctfe.SetUpInstance(ctx, opts)
	if err != nil {
		return nil, err
	}
	for path, handler := range inst.Handlers {
		mux.Handle(path, handler)
	}
	return inst, nil
}

func newGCPStorage(ctx context.Context, signer note.Signer) (*sctfe.CTStorage, error) {
	gcpCfg := gcpTessera.Config{
		ProjectID: *projectID,
		Bucket:    *bucket,
		Spanner:   *spannerDB,
	}
	tesseraStorage, err := gcpTessera.New(ctx, gcpCfg, tessera.WithCheckpointSignerVerifier(signer, nil), tessera.WithCTLayout())
	if err != nil {
		return nil, fmt.Errorf("Failed to initialize GCP Tessera storage: %v", err)
	}

	issuerStorage, err := gcpSCTFE.NewIssuerStorage(ctx, *projectID, *bucket, "fingerprints/", "application/pkix-cert")
	if err != nil {
		return nil, fmt.Errorf("Failed to initialize GCP issuer storage: %v", err)
	}

	// TODO: replace with a global dedup storage for GCP
	beDedupStorage, err := bbolt.NewStorage(*dedupPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize BBolt deduplication database: %v", err)
	}

	fetcher, err := gcpSCTFE.GetFetcher(ctx, *bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to get a log fetcher: %v", err)
	}

	go dedup.UpdateFromLog(ctx, beDedupStorage, time.Second, fetcher, sctfe.DedupFromBundle)

	return sctfe.NewCTSTorage(tesseraStorage, issuerStorage, beDedupStorage)
}
