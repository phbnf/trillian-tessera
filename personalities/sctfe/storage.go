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

package sctfe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"sync"
	"time"

	"github.com/google/certificate-transparency-go/x509"
	tessera "github.com/transparency-dev/trillian-tessera"
	"github.com/transparency-dev/trillian-tessera/api/layout"
	"github.com/transparency-dev/trillian-tessera/client"
	"github.com/transparency-dev/trillian-tessera/ctonly"
	"golang.org/x/crypto/cryptobyte"
	"golang.org/x/mod/sumdb/note"
	"k8s.io/klog/v2"
)

const (
	// Each key is 32 bytes long, so this will take up to 32MB.
	// A CT log references ~15k unique issuer certifiates in 2024, so this gives plenty of space
	// if we ever run into this limit, we should re-think how it works.
	maxCachedIssuerKeys = 1 << 20
)

// Storage provides all the storage primitives necessary to write to a ct-static-api log.
type Storage interface {
	// Add assigns an index to the provided Entry, stages the entry for integration, and return it the assigned index.
	Add(context.Context, *ctonly.Entry) (uint64, error)
	// AddIssuerChain stores every the chain certificate in a content-addressable store under their sha256 hash.
	AddIssuerChain(context.Context, []*x509.Certificate) error
	// AddCertIndex stores the index of certificate in a content-addressable store.
	AddCertIndex(context.Context, *x509.Certificate, uint64) error
	// GetCertIndex gets the index of certificate from a content-addressable store.
	GetCertIndex(context.Context, *x509.Certificate) (uint64, bool, error)
}

type KV struct {
	K []byte
	V []byte
}

// IssuerStorage issuer certificates under their hex encoded sha256.
type IssuerStorage interface {
	AddIssuersIfNotExist(ctx context.Context, kv []KV) error
}

type CertIndexStorage interface {
	Add(ctx context.Context, key []byte, idx uint64) error
	Get(ctx context.Context, key []byte) (uint64, bool, error)
}

// CTStorage implements Storage.
type CTStorage struct {
	storeData    func(context.Context, *ctonly.Entry) (uint64, error)
	storeIssuers func(context.Context, []KV) error
	crtIdxs      CertIndexStorage
}

// NewCTStorage instantiates a CTStorage object.
func NewCTSTorage(logStorage tessera.Storage, issuerStorage IssuerStorage, certIdxStorage CertIndexStorage) (*CTStorage, error) {
	ctStorage := &CTStorage{
		storeData:    tessera.NewCertificateTransparencySequencedWriter(logStorage),
		storeIssuers: cachedStoreIssuers(issuerStorage),
		crtIdxs:      certIdxStorage,
	}
	return ctStorage, nil
}

// Add stores CT entries.
func (cts *CTStorage) Add(ctx context.Context, entry *ctonly.Entry) (uint64, error) {
	// TODO(phboneff): add deduplication and chain storage
	return cts.storeData(ctx, entry)
}

// AddIssuerChain stores every chain certificate under its sha256.
//
// If an object is already stored under this hash, continues.
func (cts *CTStorage) AddIssuerChain(ctx context.Context, chain []*x509.Certificate) error {
	kvs := []KV{}
	for _, c := range chain {
		id := sha256.Sum256(c.Raw)
		key := []byte(hex.EncodeToString(id[:]))
		kvs = append(kvs, KV{K: key, V: c.Raw})
	}
	if err := cts.storeIssuers(ctx, kvs); err != nil {
		return fmt.Errorf("error storing intermediates: %v", err)
	}
	return nil
}

// cachedStoreIssuers returns a caching wrapper for an IssuerStorage
//
// This is intended to make querying faster. It does not keep a copy of the certs, only sha256.
// Only up to maxCachedIssuerKeys keys will be stored locally.
func cachedStoreIssuers(s IssuerStorage) func(context.Context, []KV) error {
	var mu sync.RWMutex
	m := make(map[string]struct{})
	return func(ctx context.Context, kv []KV) error {
		req := []KV{}
		for _, kv := range kv {
			mu.RLock()
			_, ok := m[string(kv.K)]
			mu.RUnlock()
			if ok {
				klog.V(2).Infof("cachedStoreIssuers wrapper: found %q in local key cache", kv.K)
				continue
			}
			req = append(req, kv)
		}
		if err := s.AddIssuersIfNotExist(ctx, req); err != nil {
			return fmt.Errorf("AddIssuersIfNotExist()s: error storing issuer data in the underlying IssuerStorage: %v", err)
		}
		for _, kv := range req {
			if len(m) >= maxCachedIssuerKeys {
				klog.V(2).Infof("cachedStoreIssuers wrapper: local issuer cache full, will stop caching issuers.")
				return nil
			}
			mu.Lock()
			m[string(kv.K)] = struct{}{}
			mu.Unlock()
		}
		return nil
	}
}

func (cts CTStorage) AddCertIndex(ctx context.Context, c *x509.Certificate, idx uint64) error {
	key := sha256.Sum256(c.Raw)
	if err := cts.crtIdxs.Add(ctx, key[:], idx); err != nil {
		return fmt.Errorf("error storing index %d of %q: %v", idx, hex.EncodeToString(key[:]), err)
	}
	return nil
}

func (cts CTStorage) GetCertIndex(ctx context.Context, c *x509.Certificate) (uint64, bool, error) {
	key := sha256.Sum256(c.Raw)
	idx, ok, err := cts.crtIdxs.Get(ctx, key[:])
	if err != nil {
		return 0, false, fmt.Errorf("error fetching index of %q: %v", hex.EncodeToString(key[:]), err)
	}
	return idx, ok, nil
}

type LocalDedupStorage interface {
	Add(ctx context.Context, leafID [32]byte, idx uint64) error
	Get(ctx context.Context, leafID [32]byte) (uint64, bool, error)
	LogSize(ctx context.Context) (uint64, error) // returns the largest idx Add has successfully been called with
	SetLogSize(ctx context.Context, idx uint64) error
}

type LocalBesEffortDedup struct {
	CertIndexStorage
	LogSize    func(context.Context) (uint64, error)
	SetLogSize func(context.Context, uint64) error
	fetcher    client.Fetcher
}

func NewLocalBestEffortDedup(ctx context.Context, lds LocalDedupStorage, t time.Duration, f client.Fetcher, v note.Verifier, origin string) *LocalBesEffortDedup {
	ret := &LocalBesEffortDedup{CertIndexStorage: lds, LogSize: lds.LogSize, SetLogSize: lds.SetLogSize, fetcher: f}
	go func() {
		tck := time.NewTicker(t)
		defer tck.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tck.C:
				if err := ret.sync(ctx, origin, v); err != nil {
					klog.Warningf("error updating deduplication data: %v", err)
				}
			}
		}
	}()
	return ret
}

func (d *LocalBesEffortDedup) sync(ctx context.Context, origin string, v note.Verifier) error {
	ckpt, _, _, err := client.FetchCheckpoint(ctx, d.fetcher, v, origin)
	if err != nil {
		return fmt.Errorf("FetchCheckpoint: %v", err)
	}
	oldSize, err := d.LogSize(ctx)
	if err != nil {
		return fmt.Errorf("OldSize(): %v", err)
	}

	// TODO(phboneff): add parallelism
	// Greatly inspired by https://github.com/FiloSottile/sunlight/blob/main/tile.go and
	// https://github.com/transparency-dev/trillian-tessera/blob/main/client/client.go
	if ckpt.Size > oldSize {
		for i := oldSize / 8; i <= ckpt.Size/8; i++ {
			entries := [][]byte{}
			p := layout.EntriesPath(i, ckpt.Size)
			eRaw, err := d.fetcher(ctx, p)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("leaf bundle at index %d not found: %v", i, err)
				}
				return fmt.Errorf("failed to fetch leaf bundle at index %d: %v", i, err)
			}
			s := cryptobyte.String(eRaw)

			for len(s) > 0 {
				var timestamp uint64
				var entryType uint16
				var extensions, fingerprints cryptobyte.String
				if !s.ReadUint64(&timestamp) || !s.ReadUint16(&entryType) || timestamp > math.MaxInt64 {
					return fmt.Errorf("invalid data tile")
				}
				crt := []byte{}
				switch entryType {
				case 0: // x509_entry
					if !s.ReadUint24LengthPrefixed((*cryptobyte.String)(&crt)) ||
						// TODO(phboneff): remove below?
						!s.ReadUint16LengthPrefixed(&extensions) ||
						!s.ReadUint16LengthPrefixed(&fingerprints) {
						return fmt.Errorf("invalid data tile x509_entry")
					}
				case 1: // precert_entry
					IssuerKeyHash := [32]byte{}
					var defangedCrt, extensions cryptobyte.String
					if !s.CopyBytes(IssuerKeyHash[:]) ||
						!s.ReadUint24LengthPrefixed(&defangedCrt) ||
						!s.ReadUint16LengthPrefixed(&extensions) ||
						!s.ReadUint24LengthPrefixed((*cryptobyte.String)(&crt)) ||
						// TODO(phboneff): remove below?
						!s.ReadUint16LengthPrefixed(&fingerprints) {
						return fmt.Errorf("invalid data tile precert_entry")
					}
				default:
					return fmt.Errorf("invalid data tile: unknown type %d", entryType)
				}
				entries = append(entries, crt)
			}

			for k, e := range entries {
				key := sha256.Sum256(e)
				idx := i*8 + uint64(k)
				err := d.Add(ctx, key, idx)
				if err != nil {
					return fmt.Errorf("error storing deduplication index %d for entry %q", idx, hex.EncodeToString(key[:]))
				}
			}
			if err := d.SetLogSize(ctx, ckpt.Size); err != nil {
				return fmt.Errorf("error storing checkpoint size: %v", err)
			}
		}
	}
	return nil
}
