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
	"crypto"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/certificate-transparency-go/x509"
	"github.com/transparency-dev/trillian-tessera/personalities/ct-static-api/configpb"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"k8s.io/klog/v2"
)

// ValidatedLogConfig represents the LogConfig with the information that has
// been successfully parsed as a result of validating it.
type ValidatedLogConfig struct {
	Config        *configpb.LogConfig
	PubKey        crypto.PublicKey
	PrivKey       proto.Message
	KeyUsages     []x509.ExtKeyUsage
	NotAfterStart *time.Time
	NotAfterLimit *time.Time
}

// LogMultiConfigFromFile creates a LogMultiConfig proto from the given
// filename, which should contain text or binary-encoded protobuf configuration data.
// Does not do full validation of the config but checks that it is non empty.
func LogMultiConfigFromFile(filename string) (*configpb.LogMultiConfig, error) {
	cfgBytes, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var cfg configpb.LogMultiConfig
	if txtErr := prototext.Unmarshal(cfgBytes, &cfg); txtErr != nil {
		if binErr := proto.Unmarshal(cfgBytes, &cfg); binErr != nil {
			return nil, fmt.Errorf("failed to parse LogMultiConfig from %q as text protobuf (%v) or binary protobuf (%v)", filename, txtErr, binErr)
		}
	}

	if len(cfg.GetConfig()) == 0 {
		return nil, errors.New("config is missing backends and/or log configs")
	}
	return &cfg, nil
}

// ValidateLogConfig checks that a single log config is valid. In particular:
//   - A mirror log has a valid public key and no private key.
//   - A non-mirror log has a private, and optionally a public key (both valid).
//   - Each of NotBeforeStart and NotBeforeLimit, if set, is a valid timestamp
//     proto. If both are set then NotBeforeStart <= NotBeforeLimit.
//   - Merge delays (if present) are correct.
//   - Frozen STH (if present) is correct and signed by the provided public key.
//
// Returns the validated structures (useful to avoid double validation).
// TODO(phboneff): return an error if there is no backend config
func ValidateLogConfig(cfg *configpb.LogConfig) (*ValidatedLogConfig, error) {
	if cfg.SubmissionPrefix == "" {
		return nil, errors.New("empty log SubmissionPrefix")
	}

	vCfg := ValidatedLogConfig{Config: cfg}

	// Validate the public key.
	if pubKey := cfg.PublicKey; pubKey != nil {
		var err error
		if vCfg.PubKey, err = x509.ParsePKIXPublicKey(pubKey.Der); err != nil {
			return nil, fmt.Errorf("x509.ParsePKIXPublicKey: %w", err)
		}
	}

	// Validate the private key.
	if cfg.PrivateKey == nil {
		return nil, errors.New("empty private key")
	}
	privKey, err := cfg.PrivateKey.UnmarshalNew()
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %v", err)
	}
	vCfg.PrivKey = privKey

	// Validate the Backend config:
	if cfg.StorageConfig == nil {
		return nil, errors.New("empty storage config")
	}

	if cfg.RejectExpired && cfg.RejectUnexpired {
		return nil, errors.New("rejecting all certificates")
	}

	// Validate the extended key usages list.
	if len(cfg.ExtKeyUsages) > 0 {
		for _, kuStr := range cfg.ExtKeyUsages {
			if ku, ok := stringToKeyUsage[kuStr]; ok {
				// If "Any" is specified, then we can ignore the entire list and
				// just disable EKU checking.
				if ku == x509.ExtKeyUsageAny {
					klog.Infof("%s: Found ExtKeyUsageAny, allowing all EKUs", cfg.SubmissionPrefix)
					vCfg.KeyUsages = nil
					break
				}
				vCfg.KeyUsages = append(vCfg.KeyUsages, ku)
			} else {
				return nil, fmt.Errorf("unknown extended key usage: %s", kuStr)
			}
		}
	}

	// Validate the time interval.
	start, limit := cfg.NotAfterStart, cfg.NotAfterLimit
	if start != nil {
		vCfg.NotAfterStart = &time.Time{}
		if err := start.CheckValid(); err != nil {
			return nil, fmt.Errorf("invalid start timestamp: %v", err)
		}
		*vCfg.NotAfterStart = start.AsTime()
	}
	if limit != nil {
		vCfg.NotAfterLimit = &time.Time{}
		if err := limit.CheckValid(); err != nil {
			return nil, fmt.Errorf("invalid limit timestamp: %v", err)
		}
		*vCfg.NotAfterLimit = limit.AsTime()
	}
	if start != nil && limit != nil && (*vCfg.NotAfterLimit).Before(*vCfg.NotAfterStart) {
		return nil, errors.New("limit before start")
	}

	switch {
	case cfg.MaxMergeDelaySec < 0:
		return nil, errors.New("negative maximum merge delay")
	case cfg.ExpectedMergeDelaySec < 0:
		return nil, errors.New("negative expected merge delay")
	case cfg.ExpectedMergeDelaySec > cfg.MaxMergeDelaySec:
		return nil, errors.New("expected merge delay exceeds MMD")
	}

	return &vCfg, nil
}

func validateConfigs(cfg []*configpb.LogConfig) error {
	// Check that logs have no duplicate or empty prefixes. Apply other LogConfig
	// specific checks.
	logOriginMap := make(map[string]bool)
	for _, logCfg := range cfg {
		if _, err := ValidateLogConfig(logCfg); err != nil {
			return fmt.Errorf("log config: %v: %v", err, logCfg)
		}
		if len(logCfg.SubmissionPrefix) == 0 {
			return fmt.Errorf("log config: empty prefix: %v", logCfg)
		}
		if logOriginMap[logCfg.SubmissionPrefix] {
			return fmt.Errorf("log config: duplicate prefix: %s: %v", logCfg.SubmissionPrefix, logCfg)
		}
		logOriginMap[logCfg.SubmissionPrefix] = true
	}

	return nil
}

// ValidateLogConfigs checks that a config is valid for use with a single log
// server. The rules applied are:
//
// 1. All log configs must be valid (see ValidateLogConfig).
// 2. The prefixes of configured logs must all be distinct and must not be
// empty.
// 3. The set of tree IDs must be distinct.
func ValidateLogConfigs(cfg []*configpb.LogConfig) error {
	if err := validateConfigs(cfg); err != nil {
		return err
	}

	// Check that logs have no duplicate Origins.
	treeIDs := make(map[string]bool)
	for _, logCfg := range cfg {
		if treeIDs[logCfg.SubmissionPrefix] {
			return fmt.Errorf("log config: dup submission prefix: %s for: %v", logCfg.SubmissionPrefix, logCfg)
		}
		treeIDs[logCfg.SubmissionPrefix] = true
	}

	return nil
}

// ValidateLogMultiConfig checks that a config is valid
func ValidateLogMultiConfig(cfg *configpb.LogMultiConfig) error {
	return validateConfigs(cfg.GetConfig())
}

var stringToKeyUsage = map[string]x509.ExtKeyUsage{
	"Any":                        x509.ExtKeyUsageAny,
	"ServerAuth":                 x509.ExtKeyUsageServerAuth,
	"ClientAuth":                 x509.ExtKeyUsageClientAuth,
	"CodeSigning":                x509.ExtKeyUsageCodeSigning,
	"EmailProtection":            x509.ExtKeyUsageEmailProtection,
	"IPSECEndSystem":             x509.ExtKeyUsageIPSECEndSystem,
	"IPSECTunnel":                x509.ExtKeyUsageIPSECTunnel,
	"IPSECUser":                  x509.ExtKeyUsageIPSECUser,
	"TimeStamping":               x509.ExtKeyUsageTimeStamping,
	"OCSPSigning":                x509.ExtKeyUsageOCSPSigning,
	"MicrosoftServerGatedCrypto": x509.ExtKeyUsageMicrosoftServerGatedCrypto,
	"NetscapeServerGatedCrypto":  x509.ExtKeyUsageNetscapeServerGatedCrypto,
}
