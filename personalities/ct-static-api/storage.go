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

package ctfe

import (
	"context"
	"fmt"

	tessera "github.com/transparency-dev/trillian-tessera"
	"github.com/transparency-dev/trillian-tessera/ctonly"
	"github.com/transparency-dev/trillian-tessera/personalities/ct-static-api/configpb"
	"github.com/transparency-dev/trillian-tessera/storage/gcp"
)

type Storage interface {
	// 	Add assign an index to the provided Entry, stages the entry for integration, and return it the assigned index.
	Add(context.Context, *ctonly.Entry) (uint64, error)
}

// ctStorage implements Storage
type ctStorage struct {
	storeData func(context.Context, *ctonly.Entry) (uint64, error)
	// TODO(phboneff): add storeExtraData
	// TODO(phboneff): add dedupe
}

func newCTSTorage(logStorage tessera.Storage) (*ctStorage, error) {
	ctStorage := new(ctStorage)
	ctStorage.storeData = tessera.NewCertificateTransparencySequencedWriter(logStorage)
	return ctStorage, nil
}

func (cts ctStorage) Add(ctx context.Context, entry *ctonly.Entry) (uint64, error) {
	return cts.storeData(ctx, entry)
}

func NewGCPStorage(ctx context.Context, cfg *configpb.GCPConfig) (*ctStorage, error) {
	gcpCfg := gcp.Config{
		// TODO(phboneff): get projectID in a better way
		ProjectID: "phboneff-dev",
		Bucket:    cfg.Bucket,
		Spanner:   cfg.SpannerDbPath,
	}
	storage, err := gcp.New(ctx, gcpCfg)
	if err != nil {
		return nil, fmt.Errorf("Failed to initialize GCP storage: %v", err)
	}
	return newCTSTorage(storage)
}
