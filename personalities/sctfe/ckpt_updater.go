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

// This file allows to update a static-ct-api checkpoint on a clock
package sctfe

import (
	"context"
	"fmt"
	"time"

	tdnote "github.com/transparency-dev/formats/note"
	"github.com/transparency-dev/trillian-tessera/client"
	"golang.org/x/mod/sumdb/note"
	"k8s.io/klog/v2"
)

const (
	keyHashSize   = 4
	timestampSize = 8
)

type Writer func(ctx context.Context, path string, data []byte) error

func updateCheckpointIfTooOld(ctx context.Context, f client.Fetcher, t time.Duration, signer note.Signer, writer Writer, path string, v note.Verifier) error {
	cpRaw, err := f(ctx, path)
	if err != nil {
		return fmt.Errorf("error reading checkpoint: %v", err)
	}
	//	var ckpt log.Checkpoint
	ckpt, err := note.Open(cpRaw, note.VerifierList(v))
	if err != nil {
		return fmt.Errorf("error opening checkpoint: %v", err)
	}
	if len(ckpt.Sigs) < 1 {
		return fmt.Errorf("can't find RFC6962NoteSignature")
	}
	timestamp, err := tdnote.RFC6962STHTimestamp(ckpt.Sigs[0])
	if err != nil {
		return fmt.Errorf("RFC6962STHTimestamp(): can't extract timestamp from RFC6962NoteSignature: %v")
	}
	now := time.Now()
	mustUpdateBy := timestamp.Add(t)
	if mustUpdateBy.After(time.Now()) {
		klog.V(3).Infof("updateCheckpointIdTooOld: ckpt_timstamp(%t) + update_freq(%t) < now(%t), won't update the checkpoint", timestamp, t, now)
		return nil
	}

	signer.Sign()

	return nil
}
