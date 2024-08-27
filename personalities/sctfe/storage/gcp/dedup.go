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
	"io"
	"net/http"
	"path"

	gcs "cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"k8s.io/klog/v2"
)

// DedupStorage is a key value store backed by GCS on GCP to store leaf indexes.
type DedupStorage struct {
	bucket      *gcs.BucketHandle
	prefix      string
	contentType string
}

// NewIssuerStorage creates a new GCSStorage.
//
// The specified bucket must exist or an error will be returned.
func NewDedupStorage(ctx context.Context, projectID string, bucket string, prefix string, contentType string) (*DedupStorage, error) {
	c, err := gcs.NewClient(ctx, gcs.WithJSONReads())
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %v", err)
	}

	it := c.Buckets(ctx, projectID)
	for {
		bAttrs, err := it.Next()
		if err == iterator.Done {
			return nil, fmt.Errorf("bucket %q does not exist, please create it", bucket)
		}
		if err != nil {
			return nil, fmt.Errorf("error scanning buckets: %v", err)
		}
		if bAttrs.Name == bucket {
			break
		}
	}
	r := &DedupStorage{
		bucket:      c.Bucket(bucket),
		prefix:      prefix,
		contentType: contentType,
	}

	return r, nil
}

// Add stores the provided data under key.
//
// If there is already an object under that key, it does not override it, and returns.
// TODO(phboneff): consider reading the object to make sure it's identical
func (d DedupStorage) Add(ctx context.Context, key []byte, idx uint64) error {
	idxb := binary.BigEndian.AppendUint64([]byte{}, idx)

	objName := d.keyToObjName(key)
	obj := d.bucket.Object(objName)

	// Don't overwrite if it already exists
	w := obj.If(gcs.Conditions{DoesNotExist: true}).NewWriter(ctx)
	w.ObjectAttrs.ContentType = d.contentType

	if _, err := w.Write(idxb); err != nil {
		return fmt.Errorf("failed to write object %q to bucket %q: %w", objName, d.bucket.BucketName(), err)
	}

	if err := w.Close(); err != nil {
		// If we run into a precondition failure error, it means that the object already exists.
		if ee, ok := err.(*googleapi.Error); ok && ee.Code == http.StatusPreconditionFailed {
			klog.V(2).Infof("Add: object %q already exists in bucket %q, continuing", objName, d.bucket.BucketName())
			return nil
		}

		return fmt.Errorf("failed to close write on %q: %v", objName, err)
	}
	return nil
}

func (d DedupStorage) Get(ctx context.Context, key []byte) (uint64, bool, error) {
	objName := d.keyToObjName(key)
	r, err := d.bucket.Object(objName).NewReader(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("Get: failed to create reader for object %q in bucket %q: %w", objName, d.bucket.BucketName(), err)
	}

	idxb, err := io.ReadAll(r)
	if err == gcs.ErrObjectNotExist {
		return 0, false, nil
	} else if err != nil {
		return 0, false, fmt.Errorf("failed to read %q: %v", objName, err)
	}
	idx := binary.BigEndian.Uint64(idxb)

	return idx, true, err
}

// keyToObjName converts bytes to a GCS object name.
func (s *DedupStorage) keyToObjName(key []byte) string {
	return path.Join(s.prefix, string(key))
}
