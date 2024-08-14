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
	"fmt"

	"github.com/google/certificate-transparency-go/x509"
	tessera "github.com/transparency-dev/trillian-tessera"
	"github.com/transparency-dev/trillian-tessera/ctonly"
	"golang.org/x/sync/errgroup"
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

type IssuerStorage interface {
	Exists(ctx context.Context, key [32]byte) (bool, error)
	Add(ctx context.Context, key [32]byte, data []byte) error
}

type CertIndexStorage interface {
	Add(ctx context.Context, key [32]byte, data []byte) error
	Get(ctx context.Context, key [32]byte) ([]byte, bool, error)
}

// CTStorage implements Storage.
type CTStorage struct {
	storeData func(context.Context, *ctonly.Entry) (uint64, error)
	issuers   IssuerStorage
	crtIdxs   CertIndexStorage
}

// NewCTStorage instantiates a CTStorage object.
func NewCTSTorage(logStorage tessera.Storage, issuerStorage IssuerStorage, certIdxStorage CertIndexStorage) (*CTStorage, error) {
	ctStorage := &CTStorage{
		storeData: tessera.NewCertificateTransparencySequencedWriter(logStorage),
		issuers:   NewCachedIssuerStorage(issuerStorage),
		crtIdxs:   certIdxStorage,
	}
	return ctStorage, nil
}

// Add stores CT entries.
func (cts *CTStorage) Add(ctx context.Context, entry *ctonly.Entry) (uint64, error) {
	// TODO(phboneff): add deduplication and chain storage
	return cts.storeData(ctx, entry)
}

// AddIssuerChain stores every chain certificate under its sha256.
// If an object is already stored under this hash, continues.
func (cts *CTStorage) AddIssuerChain(ctx context.Context, chain []*x509.Certificate) error {
	errG := errgroup.Group{}
	for _, c := range chain {
		errG.Go(func() error {
			key := sha256.Sum256(c.Raw)
			// We first try and see if this issuer cert has already been stored since reads
			// are cheaper than writes.
			// TODO(phboneff): monitor usage, eventually write directly depending on usage patterns
			ok, err := cts.issuers.Exists(ctx, key)
			if err != nil {
				return fmt.Errorf("error checking if issuer %q exists: %s", hex.EncodeToString(key[:]), err)
			}
			if !ok {
				if err = cts.issuers.Add(ctx, key, c.Raw); err != nil {
					return fmt.Errorf("error adding certificate for issuer %q: %v", hex.EncodeToString(key[:]), err)
				}
			}
			return nil
		})
	}
	if err := errG.Wait(); err != nil {
		return err
	}
	return nil
}

// cachedIssuerStorage wraps an IssuerStorage, and keeps a local copy the keys it contains.
// This is intended to make querying faster. It does not keep a copy of the data, only keys.
// Only up to N keys will be stored locally.
// TODO(phboneff): add monitoring for the number of keys
type cachedIssuerStorage struct {
	m map[string]bool
	N int // maximum number of entries allowed in m
	s IssuerStorage
}

// Exists checks whether the key is stored locally, it not checks in the underlying storage.
// If it finds it there, caches the key locally.
func (c cachedIssuerStorage) Exists(ctx context.Context, key [32]byte) (bool, error) {
	_, ok := c.m[string(key[:])]
	if ok {
		klog.V(2).Infof("Exists: found %q in local key cache", hex.EncodeToString(key[:]))
		return true, nil
	}
	ok, err := c.s.Exists(ctx, key)
	if err != nil {
		return false, fmt.Errorf("error checking if issuer %q exists in the underlying IssuerStorage: %s", hex.EncodeToString(key[:]), err)
	}
	if ok {
		c.m[string(key[:])] = true
	}
	return ok, nil
}

// Add first adds the data under key to the underlying storage, then caches the key locally.
//
// Add will only store up to c.N keys.
func (c cachedIssuerStorage) Add(ctx context.Context, key [32]byte, data []byte) error {
	err := c.s.Add(ctx, key, data)
	if err != nil {
		return fmt.Errorf("Add: error storing issuer data for %q in the underlying IssuerStorage", hex.EncodeToString(key[:]))
	}
	if len(c.m) >= c.N {
		klog.V(2).Infof("Add: local key cache full, won't cache %q", hex.EncodeToString(key[:]))
		return nil
	}
	c.m[string(key[:])] = true
	return nil
}

func NewCachedIssuerStorage(s IssuerStorage) cachedIssuerStorage {
	c := cachedIssuerStorage{s: s, N: maxCachedIssuerKeys}
	c.m = make(map[string]bool)
	return c
}
