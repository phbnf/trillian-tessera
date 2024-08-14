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

// Package bbolt implements SCTFE storage systems for deduplication.
//
// The interfaces are defined in sctfe/storage.go
package bbolt

import (
	"context"
	"encoding/binary"
	"fmt"

	bolt "go.etcd.io/bbolt"
	"k8s.io/klog/v2"
)

var (
	dedupBucket = "leafIdx"
	sizeBucket  = "logSize"
)

//	Add(ctx context.Context, leafID [32]byte, idx uint64) error
//	Get(ctx context.Context, leafID [32]byte) (uint64, bool, error)
//	OldSize(ctx context.Context) (uint64, error) // returns the largest idx Add has successfully been called with
//	SetOldSize(ctx context.Context, idx uint64) error

type Storage struct {
	db *bolt.DB
}

func NewStorage(ctx context.Context, path string) (*Storage, error) {
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("bolt.Open(): %v", err)
	}
	fmt.Println("Created a DB")
	s := &Storage{db: db}

	err = db.Update(func(tx *bolt.Tx) error {
		dedupB := tx.Bucket([]byte(dedupBucket))
		sizeB := tx.Bucket([]byte(sizeBucket))
		if dedupB == nil && sizeB == nil {
			klog.V(2).Infof("no pre-existing buckets, will create %q and %q.", dedupBucket, sizeBucket)
			_, err := tx.CreateBucket([]byte(dedupBucket))
			if err != nil {
				return fmt.Errorf("create %q bucket: %v", dedupBucket, err)
			}
			sb, err := tx.CreateBucket([]byte(sizeBucket))
			if err != nil {
				return fmt.Errorf("create %q bucket: %v", sizeBucket, err)
			}
			klog.V(2).Infof("initializing %q with size 0.", sizeBucket)
			err = sb.Put([]byte("size"), itob(0))
			if err != nil {
				return fmt.Errorf("error reading logsize: %v", err)
			}
		} else if dedupB == nil && sizeB != nil {
			return fmt.Errorf("inconsistent deduplication storage state %q is nil but %q it not nil", dedupBucket, sizeBucket)
		} else if dedupB != nil && sizeB == nil {
			return fmt.Errorf("inconsistent deduplication storage state, %q is not nil but %q is nil", dedupBucket, sizeBucket)
		} else {
			klog.V(2).Infof("found pre-existing %q and %q buckets.", dedupBucket, sizeBucket)
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error initializing buckets: %v", err)
	}

	return s, nil
}

func (s *Storage) Add(ctx context.Context, leafID [32]byte, idx uint64) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(dedupBucket))
		return b.Put(leafID[:], itob(idx))
	})
}

func (s *Storage) Get(ctx context.Context, leafID [32]byte) (uint64, bool, error) {
	v := make([]byte, 8)
	_ = s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(dedupBucket))
		copy(v, b.Get(leafID[:]))
		return nil
	})
	if v == nil {
		return 0, false, nil
	}
	return btoi(v), true, nil
}

func (s *Storage) LogSize(ctx context.Context) (uint64, error) {
	v := make([]byte, 8)
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(sizeBucket))
		copy(v, b.Get([]byte("size")))
		return nil
	})
	if v == nil {
		return 0, fmt.Errorf("can't find log size in bucket %q", sizeBucket)
	}
	return btoi(v), nil
}

func (s *Storage) SetLogSize(ctx context.Context, size uint64) error {
	err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(dedupBucket))
		return b.Put([]byte("size"), itob(size))
	})
	return err
}

// itob returns an 8-byte big endian representation of idx.
func itob(idx uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(idx))
	return b
}

// btoi converts a byte array to a uint64
func btoi(b []byte) uint64 {
	return binary.BigEndian.Uint64(b)
}
