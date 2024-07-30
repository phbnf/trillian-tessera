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
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/trillian/crypto/keyspb"
	"github.com/transparency-dev/trillian-tessera/personalities/ct-static-api/configpb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	invalidTimestamp = &timestamppb.Timestamp{Nanos: int32(1e9)}
)

func mustMarshalAny(pb proto.Message) *anypb.Any {
	ret, err := anypb.New(pb)
	if err != nil {
		panic(fmt.Sprintf("MarshalAny failed: %v", err))
	}
	return ret
}

func mustReadPublicKey(path string) *keyspb.PublicKey {
	keyPEM, err := os.ReadFile(path)
	if err != nil {
		panic(fmt.Sprintf("os.ReadFile(%q): %v", path, err))
	}
	block, _ := pem.Decode(keyPEM)
	pubKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		panic(fmt.Sprintf("ReadPublicKeyFile(): %v", err))
	}
	keyDER, err := x509.MarshalPKIXPublicKey(pubKey)
	if err != nil {
		panic(fmt.Sprintf("x509.MarshalPKIXPublicKey(): %v", err))
	}
	return &keyspb.PublicKey{Der: keyDER}
}

func mustDecodeBase64(str string) []byte {
	data, err := base64.StdEncoding.DecodeString(str)
	if err != nil {
		panic(fmt.Sprintf("base64: DecodeString failed: %v", err))
	}
	return data
}

func TestValidateLogConfig(t *testing.T) {
	//pubKey := mustReadPublicKey("../testdata/ct-http-server.pubkey.pem")
	privKey := mustMarshalAny(&keyspb.PEMKeyFile{Path: "../testdata/ct-http-server.privkey.pem", Password: "dirk"})

	for _, tc := range []struct {
		desc    string
		cfg     *configpb.LogConfig
		wantErr string
	}{
		{
			desc:    "empty-private-key",
			wantErr: "empty private key",
			cfg:     &configpb.LogConfig{SubmissionPrefix: "testlog"},
		},
		{
			desc:    "invalid-private-key",
			wantErr: "invalid private key",
			cfg: &configpb.LogConfig{
				SubmissionPrefix: "testlog",
				PrivateKey:       &anypb.Any{},
			},
		},
		{
			desc:    "rejecting-all",
			wantErr: "rejecting all certificates",
			cfg: &configpb.LogConfig{
				SubmissionPrefix: "testlog",
				RejectExpired:    true,
				RejectUnexpired:  true,
				PrivateKey:       privKey,
			},
		},
		{
			desc:    "unknown-ext-key-usage-1",
			wantErr: "unknown extended key usage",
			cfg: &configpb.LogConfig{
				SubmissionPrefix: "testlog",
				PrivateKey:       privKey,
				ExtKeyUsages:     []string{"wrong_usage"},
			},
		},
		{
			desc:    "unknown-ext-key-usage-2",
			wantErr: "unknown extended key usage",
			cfg: &configpb.LogConfig{
				SubmissionPrefix: "testlog",
				PrivateKey:       privKey,
				ExtKeyUsages:     []string{"ClientAuth", "ServerAuth", "TimeStomping"},
			},
		},
		{
			desc:    "unknown-ext-key-usage-3",
			wantErr: "unknown extended key usage",
			cfg: &configpb.LogConfig{
				SubmissionPrefix: "testlog",
				PrivateKey:       privKey,
				ExtKeyUsages:     []string{"Any "},
			},
		},
		{
			desc:    "invalid-start-timestamp",
			wantErr: "invalid start timestamp",
			cfg: &configpb.LogConfig{
				SubmissionPrefix: "testlog",
				PrivateKey:       privKey,
				NotAfterStart:    invalidTimestamp,
			},
		},
		{
			desc:    "invalid-limit-timestamp",
			wantErr: "invalid limit timestamp",
			cfg: &configpb.LogConfig{
				SubmissionPrefix: "testlog",
				PrivateKey:       privKey,
				NotAfterLimit:    invalidTimestamp,
			},
		},
		{
			desc:    "limit-before-start",
			wantErr: "limit before start",
			cfg: &configpb.LogConfig{
				SubmissionPrefix: "testlog",
				PrivateKey:       privKey,
				NotAfterStart:    &timestamppb.Timestamp{Seconds: 200},
				NotAfterLimit:    &timestamppb.Timestamp{Seconds: 100},
			},
		},
		{
			desc:    "negative-maximum-merge",
			wantErr: "negative maximum merge",
			cfg: &configpb.LogConfig{
				SubmissionPrefix: "testlog",
				PrivateKey:       privKey,
				MaxMergeDelaySec: -100,
			},
		},
		{
			desc:    "negative-expected-merge",
			wantErr: "negative expected merge",
			cfg: &configpb.LogConfig{
				SubmissionPrefix:      "testlog",
				PrivateKey:            privKey,
				ExpectedMergeDelaySec: -100,
			},
		},
		{
			desc:    "expected-exceeds-max",
			wantErr: "expected merge delay exceeds MMD",
			cfg: &configpb.LogConfig{
				SubmissionPrefix:      "testlog",
				PrivateKey:            privKey,
				MaxMergeDelaySec:      50,
				ExpectedMergeDelaySec: 100,
			},
		},
		{
			desc: "ok",
			cfg: &configpb.LogConfig{
				SubmissionPrefix: "testlog",
				PrivateKey:       privKey,
			},
		},
		{
			// Note: Substituting an arbitrary proto.Message as a PrivateKey will not
			// fail the validation because the actual key loading happens at runtime.
			// TODO(pavelkalinnikov): Decouple key protos validation and loading, and
			// make this test fail.
			desc: "ok-not-a-key",
			cfg: &configpb.LogConfig{
				SubmissionPrefix: "testlog",
				PrivateKey:       mustMarshalAny(&configpb.LogConfig{}),
			},
		},
		{
			desc: "ok-ext-key-usages",
			cfg: &configpb.LogConfig{
				SubmissionPrefix: "testlog",
				PrivateKey:       privKey,
				ExtKeyUsages:     []string{"ServerAuth", "ClientAuth", "OCSPSigning"},
			},
		},
		{
			desc: "ok-start-timestamp",
			cfg: &configpb.LogConfig{
				SubmissionPrefix: "testlog",
				PrivateKey:       privKey,
				NotAfterStart:    &timestamppb.Timestamp{Seconds: 100},
			},
		},
		{
			desc: "ok-limit-timestamp",
			cfg: &configpb.LogConfig{
				SubmissionPrefix: "testlog",
				PrivateKey:       privKey,
				NotAfterLimit:    &timestamppb.Timestamp{Seconds: 200},
			},
		},
		{
			desc: "ok-range-timestamp",
			cfg: &configpb.LogConfig{
				SubmissionPrefix: "testlog",
				PrivateKey:       privKey,
				NotAfterStart:    &timestamppb.Timestamp{Seconds: 300},
				NotAfterLimit:    &timestamppb.Timestamp{Seconds: 400},
			},
		},
		{
			desc: "ok-merge-delay",
			cfg: &configpb.LogConfig{
				SubmissionPrefix:      "testlog",
				PrivateKey:            privKey,
				MaxMergeDelaySec:      86400,
				ExpectedMergeDelaySec: 7200,
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			vc, err := ValidateLogConfig(tc.cfg)
			if len(tc.wantErr) == 0 && err != nil {
				t.Errorf("ValidateLogConfig()=%v, want nil", err)
			}
			if len(tc.wantErr) > 0 && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Errorf("ValidateLogConfig()=%v, want err containing %q", err, tc.wantErr)
			}
			if err == nil && vc == nil {
				t.Error("err and ValidatedLogConfig are both nil")
			}
			// TODO(pavelkalinnikov): Test that ValidatedLogConfig is correct.
		})
	}
}

//func TestValidateLogMultiConfig(t *testing.T) {
//	privKey := mustMarshalAny(&keyspb.PEMKeyFile{Path: "../testdata/ct-http-server.privkey.pem", Password: "dirk"})
//	for _, tc := range []struct {
//		desc    string
//		cfg     *configpb.LogMultiConfig
//		wantErr string
//	}{
//		// TODO(phboneff): add config for multiple storage
//		{
//			desc:    "empty-backend-name",
//			wantErr: "empty backend name",
//			cfg: &configpb.LogMultiConfig{
//				Backends: &configpb.LogBackendSet{
//					Backend: []*configpb.LogBackend{
//						{BackendSpec: "testspec"},
//					},
//				},
//			},
//		},
//		{
//			desc:    "empty-backend-spec",
//			wantErr: "empty backend spec",
//			cfg: &configpb.LogMultiConfig{
//				Backends: &configpb.LogBackendSet{
//					Backend: []*configpb.LogBackend{
//						{Name: "log1"},
//					},
//				},
//			},
//		},
//		{
//			desc:    "duplicate-backend-name",
//			wantErr: "duplicate backend name",
//			cfg: &configpb.LogMultiConfig{
//				Backends: &configpb.LogBackendSet{
//					Backend: []*configpb.LogBackend{
//						{Name: "dup", BackendSpec: "testspec"},
//						{Name: "dup", BackendSpec: "testspec"},
//					},
//				},
//			},
//		},
//		{
//			desc:    "duplicate-backend-spec",
//			wantErr: "duplicate backend spec",
//			cfg: &configpb.LogMultiConfig{
//				Backends: &configpb.LogBackendSet{
//					Backend: []*configpb.LogBackend{
//						{Name: "log1", BackendSpec: "testspec"},
//						{Name: "log2", BackendSpec: "testspec"},
//					},
//				},
//			},
//		},
//		{
//			desc:    "invalid-log-config",
//			wantErr: "log config: empty log ID",
//			cfg: &configpb.LogMultiConfig{
//				Backends: &configpb.LogBackendSet{
//					Backend: []*configpb.LogBackend{
//						{Name: "log1", BackendSpec: "testspec"},
//					},
//				},
//				LogConfigs: &configpb.LogConfigSet{
//					Config: []*configpb.LogConfig{
//						{Prefix: "pref"},
//					},
//				},
//			},
//		},
//		{
//			desc:    "empty-prefix",
//			wantErr: "empty prefix",
//			cfg: &configpb.LogMultiConfig{
//				Backends: &configpb.LogBackendSet{
//					Backend: []*configpb.LogBackend{
//						{Name: "log1", BackendSpec: "testspec"},
//					},
//				},
//				LogConfigs: &configpb.LogConfigSet{
//					Config: []*configpb.LogConfig{
//						{LogId: 1, PrivateKey: privKey, LogBackendName: "log1"},
//					},
//				},
//			},
//		},
//		{
//			desc:    "duplicate-prefix",
//			wantErr: "duplicate prefix",
//			cfg: &configpb.LogMultiConfig{
//				Backends: &configpb.LogBackendSet{
//					Backend: []*configpb.LogBackend{
//						{Name: "log1", BackendSpec: "testspec1"},
//					},
//				},
//				LogConfigs: &configpb.LogConfigSet{
//					Config: []*configpb.LogConfig{
//						{LogId: 1, Prefix: "pref1", PrivateKey: privKey, LogBackendName: "log1"},
//						{LogId: 2, Prefix: "pref2", PrivateKey: privKey, LogBackendName: "log1"},
//						{LogId: 3, Prefix: "pref1", PrivateKey: privKey, LogBackendName: "log1"},
//					},
//				},
//			},
//		},
//		{
//			desc:    "references-undefined-backend",
//			wantErr: "references undefined backend",
//			cfg: &configpb.LogMultiConfig{
//				Backends: &configpb.LogBackendSet{
//					Backend: []*configpb.LogBackend{
//						{Name: "log1", BackendSpec: "testspec"},
//					},
//				},
//				LogConfigs: &configpb.LogConfigSet{
//					Config: []*configpb.LogConfig{
//						{LogId: 2, Prefix: "pref2", PrivateKey: privKey, LogBackendName: "log2"},
//					},
//				},
//			},
//		},
//		{
//			desc:    "dup-tree-id-on-same-backend",
//			wantErr: "dup tree id",
//			cfg: &configpb.LogMultiConfig{
//				Backends: &configpb.LogBackendSet{
//					Backend: []*configpb.LogBackend{
//						{Name: "log1", BackendSpec: "testspec1"},
//					},
//				},
//				LogConfigs: &configpb.LogConfigSet{
//					Config: []*configpb.LogConfig{
//						{LogId: 1, Prefix: "pref1", PrivateKey: privKey, LogBackendName: "log1"},
//						{LogId: 2, Prefix: "pref2", PrivateKey: privKey, LogBackendName: "log1"},
//						{LogId: 1, Prefix: "pref3", PrivateKey: privKey, LogBackendName: "log1"},
//					},
//				},
//			},
//		},
//		{
//			desc: "ok-all-distinct",
//			cfg: &configpb.LogMultiConfig{
//				Backends: &configpb.LogBackendSet{
//					Backend: []*configpb.LogBackend{
//						{Name: "log1", BackendSpec: "testspec1"},
//						{Name: "log2", BackendSpec: "testspec2"},
//						{Name: "log3", BackendSpec: "testspec3"},
//					},
//				},
//				LogConfigs: &configpb.LogConfigSet{
//					Config: []*configpb.LogConfig{
//						{LogId: 1, Prefix: "pref1", PrivateKey: privKey, LogBackendName: "log1"},
//						{LogId: 2, Prefix: "pref2", PrivateKey: privKey, LogBackendName: "log2"},
//						{LogId: 3, Prefix: "pref3", PrivateKey: privKey, LogBackendName: "log3"},
//					},
//				},
//			},
//		},
//		{
//			desc: "ok-dup-tree-ids-on-different-backends",
//			cfg: &configpb.LogMultiConfig{
//				Backends: &configpb.LogBackendSet{
//					Backend: []*configpb.LogBackend{
//						{Name: "log1", BackendSpec: "testspec1"},
//						{Name: "log2", BackendSpec: "testspec2"},
//						{Name: "log3", BackendSpec: "testspec3"},
//					},
//				},
//				LogConfigs: &configpb.LogConfigSet{
//					Config: []*configpb.LogConfig{
//						{LogId: 1, Prefix: "pref1", PrivateKey: privKey, LogBackendName: "log1"},
//						{LogId: 1, Prefix: "pref2", PrivateKey: privKey, LogBackendName: "log2"},
//						{LogId: 1, Prefix: "pref3", PrivateKey: privKey, LogBackendName: "log3"},
//					},
//				},
//			},
//		},
//	} {
//		t.Run(tc.desc, func(t *testing.T) {
//			_, err := ValidateLogMultiConfig(tc.cfg)
//			if len(tc.wantErr) == 0 && err != nil {
//				t.Fatalf("ValidateLogMultiConfig()=%v, want nil", err)
//			}
//			if len(tc.wantErr) > 0 && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
//				t.Errorf("ValidateLogMultiConfig()=%v, want err containing %q", err, tc.wantErr)
//			}
//		})
//	}
//}
//
