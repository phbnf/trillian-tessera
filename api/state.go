// Copyright 2024 Google LLC. All Rights Reserved.
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

// Package api contains the tiles definitions from the [tlog-tiles API].
//
// [tlog-tiles API]: https://c2sp.org/tlog-tiles
package api

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math"

	"golang.org/x/crypto/cryptobyte"
)

// HashTile represents a tile within the Merkle hash tree.
// Leaf HashTiles will have a corresponding EntryBundle, where each
// entry in the EntryBundle slice hashes to the value at the same
// index in the Nodes slice.
type HashTile struct {
	// Nodes stores the leaf hash nodes in this tile.
	// Note that only non-ephemeral nodes are stored.
	Nodes [][]byte
}

// MarshalText implements encoding/TextMarshaller and writes out an HashTile
// instance as sequences of concatenated hashes as specified by the tlog-tiles spec.
func (t HashTile) MarshalText() ([]byte, error) {
	r := &bytes.Buffer{}
	for _, n := range t.Nodes {
		if _, err := r.Write(n); err != nil {
			return nil, err
		}
	}
	return r.Bytes(), nil
}

// UnmarshalText implements encoding/TextUnmarshaler and reads HashTiles
// which are encoded using the tlog-tiles spec.
func (t *HashTile) UnmarshalText(raw []byte) error {
	if len(raw)%sha256.Size != 0 {
		return fmt.Errorf("%d is not a multiple of %d", len(raw), sha256.Size)
	}
	nodes := make([][]byte, 0, len(raw)/sha256.Size)
	for index := 0; index < len(raw); index += sha256.Size {
		data := raw[index : index+sha256.Size]
		nodes = append(nodes, data)
	}
	t.Nodes = nodes
	return nil
}

// EntryBundle represents a sequence of entries in the log.
// These entries correspond to a leaf tile in the hash tree.
type EntryBundle struct {
	// Entries stores the leaf entries of the log, in order.
	Entries [][]byte
}

// UnmarshalText implements encoding/TextUnmarshaler and reads EntryBundles
// which are encoded using the tlog-tiles spec.
func (t *EntryBundle) UnmarshalText(raw []byte) error {
	entries := make([][]byte, 0, 256)
	s := cryptobyte.String(raw)

	for len(s) > 0 {
		entry := []byte{}
		var timestamp uint64
		var entryType uint16
		var extensions, fingerprints cryptobyte.String
		if !s.ReadUint64(&timestamp) || !s.ReadUint16(&entryType) || timestamp > math.MaxInt64 {
			return fmt.Errorf("invalid data tile")
		}
		bb := []byte{}
		b := cryptobyte.NewBuilder(bb)
		b.AddUint64(timestamp)
		b.AddUint16(entryType)

		switch entryType {
		case 0: // x509_entry
			if !s.ReadUint24LengthPrefixed((*cryptobyte.String)(&entry)) ||
				// TODO(phboneff): remove below?
				!s.ReadUint16LengthPrefixed(&extensions) ||
				!s.ReadUint16LengthPrefixed(&fingerprints) {
				return fmt.Errorf("invalid data tile x509_entry")
			}
			b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
				b.AddBytes(entry)
			})
			b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
				b.AddBytes(extensions)
			})
			b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
				b.AddBytes(fingerprints)
			})

		case 1: // precert_entry
			IssuerKeyHash := [32]byte{}
			var defangedCrt, extensions cryptobyte.String
			if !s.CopyBytes(IssuerKeyHash[:]) ||
				!s.ReadUint24LengthPrefixed(&defangedCrt) ||
				!s.ReadUint16LengthPrefixed(&extensions) ||
				!s.ReadUint24LengthPrefixed((*cryptobyte.String)(&entry)) ||
				// TODO(phboneff): remove below?
				!s.ReadUint16LengthPrefixed(&fingerprints) {
				return fmt.Errorf("invalid data tile precert_entry")
			}
			b.AddBytes(IssuerKeyHash[:])
			b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
				b.AddBytes(defangedCrt)
			})
			b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
				b.AddBytes(extensions)
			})
			b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
				b.AddBytes(entry)
			})
			b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
				b.AddBytes(fingerprints)
			})
		default:
			return fmt.Errorf("invalid data tile: unknown type %d", entryType)
		}
		entries = append(entries, b.BytesOrPanic())
	}
	t.Entries = entries
	return nil

}
