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

// Package gcp implements SCTFE storage systems for issuers and deduplication.
//
// The interfaces are defined in sctfe/storage.go
package gcp

import (
	"context"
	"encoding/binary"
	"fmt"
)

type KV interface {
	Add(ctx context.Context, key [32]byte, data []byte) error
	Get(ctx context.Context, key [32]byte) ([]byte, bool, error)
}

// GlobalBestEffortDedup implements CertIndexStorage.
type GlobalBestEffortDedup struct {
	kv KV
}

func NewGlobalBestEffortDedup(ctx context.Context, projectID string, bucket string, prefix string, contentType string) (*GlobalBestEffortDedup, error) {
	storage, err := NewGCSStorage(ctx, projectID, bucket, prefix, contentType)
	if err != nil {
		return nil, fmt.Errorf("Failed to initialize GCP issuer storage: %v", err)
	}
	return &GlobalBestEffortDedup{kv: storage}, nil
}

func (d GlobalBestEffortDedup) Add(ctx context.Context, key [32]byte, idx uint64) error {
	idxb := binary.BigEndian.AppendUint64([]byte{}, idx)
	return d.kv.Add(ctx, key, idxb)
}

func (d GlobalBestEffortDedup) Get(ctx context.Context, key [32]byte) (uint64, bool, error) {
	idxb, ok, err := d.kv.Get(ctx, key)
	idx := binary.BigEndian.Uint64(idxb)
	return idx, ok, err
}
