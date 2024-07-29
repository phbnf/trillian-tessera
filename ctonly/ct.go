// Copyright 2024 The Tessera authors. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
//
// Original source: https://github.com/FiloSottile/sunlight/blob/main/tile.go
//
// # Copyright 2023 The Sunlight Authors
//
// Permission to use, copy, modify, and/or distribute this software for any
// purpose with or without fee is hereby granted, provided that the above
// copyright notice and this permission notice appear in all copies.
//
// THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
// WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
// MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
// ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
// WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
// ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
// OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.

// Package ctonly has support for CT Tiles API.
//
// This code should not be reused outside of CT.
// Most of this code came from Filipo's Sunlight implementation of https://c2sp.org/ct-static-api.
package ctonly

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/google/certificate-transparency-go/x509"
	"github.com/transparency-dev/merkle/rfc6962"
	"golang.org/x/crypto/cryptobyte"
)

// Entry represents a CT log entry.
type Entry struct {
	Timestamp          uint64
	IsPrecert          bool
	Certificate        []byte
	Precertificate     []byte
	PrecertSigningCert []byte
	IssuerKeyHash      []byte
}

// LeafData returns the data which should be added to an entry bundle for this entry.
//
// Note that this will include data which IS NOT directly committed to by the entry's
// MerkleLeafHash.
func (c Entry) LeafData(idx uint64) []byte {
	b := cryptobyte.NewBuilder([]byte{})
	b.AddUint64(uint64(c.Timestamp))
	if !c.IsPrecert {
		b.AddUint16(0 /* entry_type = x509_entry */)
		b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddBytes(c.Certificate)
		})
	} else {
		b.AddUint16(1 /* entry_type = precert_entry */)
		b.AddBytes(c.IssuerKeyHash[:])
		b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddBytes(c.Certificate)
		})
	}
	addExtensions(b, idx)
	if c.IsPrecert {
		b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddBytes(c.Precertificate)
		})
		b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddBytes(c.PrecertSigningCert)
		})
	}
	return b.BytesOrPanic()
}

// MerkleTreeLeaf returns a RFC 6962 MerkleTreeLeaf.
func (e *Entry) MerkleTreeLeaf(idx uint64) []byte {
	b := &cryptobyte.Builder{}
	b.AddUint8(0 /* version = v1 */)
	b.AddUint8(0 /* leaf_type = timestamped_entry */)
	b.AddUint64(uint64(e.Timestamp))
	if !e.IsPrecert {
		b.AddUint16(0 /* entry_type = x509_entry */)
		b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddBytes(e.Certificate)
		})
	} else {
		b.AddUint16(1 /* entry_type = precert_entry */)
		b.AddBytes(e.IssuerKeyHash[:])
		b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddBytes(e.Certificate)
		})
	}
	addExtensions(b, idx)
	return b.BytesOrPanic()
}

// MerkleLeafHash returns the RFC6962 leaf hash for this entry.
//
// Note that we embed an SCT extension which captures the index of the entry in the log according to
// the mechanism specified in https://c2sp.org/ct-static-api.
func (c Entry) MerkleLeafHash(leafIndex uint64) []byte {
	b := &cryptobyte.Builder{}
	b.AddUint8(0 /* version = v1 */)
	b.AddUint8(0 /* leaf_type = timestamped_entry */)
	b.AddUint64(uint64(c.Timestamp))
	if !c.IsPrecert {
		b.AddUint16(0 /* entry_type = x509_entry */)
		b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddBytes(c.Certificate)
		})
	} else {
		b.AddUint16(1 /* entry_type = precert_entry */)
		b.AddBytes(c.IssuerKeyHash[:])
		b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddBytes(c.Certificate)
		})
	}
	addExtensions(b, leafIndex)
	return rfc6962.DefaultHasher.HashLeaf(b.BytesOrPanic())
}

func (c Entry) Identity() []byte {
	var r [sha256.Size]byte
	if c.IsPrecert {
		r = sha256.Sum256(c.Precertificate)
	} else {
		r = sha256.Sum256(c.Certificate)
	}
	return r[:]
}

func addExtensions(b *cryptobyte.Builder, leafIndex uint64) {
	b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
		ext, err := extensions{LeafIndex: leafIndex}.Marshal()
		if err != nil {
			b.SetError(err)
			return
		}
		b.AddBytes(ext)
	})
}

// extensions is the CTExtensions field of SignedCertificateTimestamp and
// TimestampedEntry, according to c2sp.org/static-ct-api.
type extensions struct {
	LeafIndex uint64
}

func (c extensions) Marshal() ([]byte, error) {
	// enum {
	//     leaf_index(0), (255)
	// } ExtensionType;
	//
	// struct {
	//     ExtensionType extension_type;
	//     opaque extension_data<0..2^16-1>;
	// } Extension;
	//
	// Extension CTExtensions<0..2^16-1>;
	//
	// uint8 uint40[5];
	// uint40 LeafIndex;

	b := &cryptobyte.Builder{}
	b.AddUint8(0 /* extension_type = leaf_index */)
	b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
		if c.LeafIndex >= 1<<40 {
			b.SetError(errors.New("leaf_index out of range"))
			return
		}
		addUint40(b, uint64(c.LeafIndex))
	})
	return b.Bytes()
}

// addUint40 appends a big-endian, 40-bit value to the byte string.
func addUint40(b *cryptobyte.Builder, v uint64) {
	b.AddBytes([]byte{byte(v >> 32), byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// MerkleTreeLeafFromChain generates a MerkleTreeLeaf from a chain and timestamp.
// copied from ctg/serialization.go
func EntryFromChain(chain []*x509.Certificate, isPrecert bool, timestamp uint64) (*Entry, error) {
	leaf := Entry{
		IsPrecert: isPrecert,
		Timestamp: timestamp,
	}
	if !isPrecert {
		leaf.Certificate = chain[0].Raw
		return &leaf, nil
	}

	// Pre-certs are more complicated. First, parse the leaf pre-cert and its
	// putative issuer.
	if len(chain) < 2 {
		return nil, fmt.Errorf("no issuer cert available for precert leaf building")
	}
	issuer := chain[1]
	cert := chain[0]

	var preIssuer *x509.Certificate
	if IsPreIssuer(issuer) {
		// Replace the cert's issuance information with details from the pre-issuer.
		preIssuer = issuer

		// The issuer of the pre-cert is not going to be the issuer of the final
		// cert.  Change to use the final issuer's key hash.
		if len(chain) < 3 {
			return nil, fmt.Errorf("no issuer cert available for pre-issuer")
		}
		issuer = chain[2]
	}

	// Next, post-process the DER-encoded TBSCertificate, to remove the CT poison
	// extension and possibly update the issuer field.
	defangedTBS, err := x509.BuildPrecertTBS(cert.RawTBSCertificate, preIssuer)
	if err != nil {
		return nil, fmt.Errorf("failed to remove poison extension: %v", err)
	}

	leaf.Precertificate = cert.Raw
	leaf.PrecertSigningCert = issuer.Raw
	leaf.Certificate = defangedTBS

	issuerKeyHash := sha256.Sum256(issuer.RawSubjectPublicKeyInfo)
	leaf.IssuerKeyHash = issuerKeyHash[:]
	return &leaf, nil
}

// IsPreIssuer indicates whether a certificate is a pre-cert issuer with the specific
// certificate transparency extended key usage.
func IsPreIssuer(issuer *x509.Certificate) bool {
	for _, eku := range issuer.ExtKeyUsage {
		if eku == x509.ExtKeyUsageCertificateTransparency {
			return true
		}
	}
	return false
}
