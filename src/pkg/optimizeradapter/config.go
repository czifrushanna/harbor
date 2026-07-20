// Copyright Project Harbor Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package optimizeradapter implements the rec-engine optimizer adapter service: a
// standalone HTTP server speaking the optimizer adapter REST contract
// (pkg/optimizer/rest/v1). It pulls the artifact from the Harbor registry with the
// credential Harbor hands it, extracts the Dockerfile from the BuildKit provenance
// attestation and optimizes it via the LLM gateway.
//
// The service currently lives in the main Harbor module for code reuse
// (pkg/buildkitdockerfile, pkg/registry). If it ever needs an independent release
// cycle, extract it into its own repository the way goharbor/harbor-scanner-trivy
// is separate from goharbor/harbor.
package optimizeradapter

import (
	"os"
	"strconv"
	"time"

	"github.com/goharbor/harbor/src/lib/errors"
	"github.com/goharbor/harbor/src/pkg/buildkitdockerfile"
)

const (
	envListenAddr     = "OPTIMIZER_ADAPTER_LISTEN_ADDR"
	envJobTTL         = "OPTIMIZER_ADAPTER_JOB_TTL"
	envMaxConcurrency = "OPTIMIZER_ADAPTER_MAX_CONCURRENCY"
	envAPIKey         = "LLMGW_API_KEY"
	envAPIBaseURL     = "LLMGW_API_BASE_URL"
	envModel          = "LLMGW_MODEL"

	defaultListenAddr     = ":8080"
	defaultJobTTL         = time.Hour
	defaultMaxConcurrency = 4
)

// Config carries the adapter service configuration, resolved from environment
// variables. The LLM gateway credential lives here — on the adapter deployment —
// not on harbor-core.
type Config struct {
	// ListenAddr is the address the HTTP server binds to.
	ListenAddr string
	// JobTTL is how long finished optimization results are kept in the in-memory store.
	JobTTL time.Duration
	// MaxConcurrency bounds the number of optimizations running at once.
	MaxConcurrency int
	// APIKey is the LLM gateway API key (required).
	APIKey string
	// APIBaseURL is the LLM gateway chat-completions endpoint.
	APIBaseURL string
	// Model is the LLM model identifier. It is deployment-specific and has no
	// baked-in default, so it must be supplied via LLMGW_MODEL.
	Model string
}

// LoadConfig resolves the adapter configuration from the environment.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		ListenAddr:     defaultListenAddr,
		JobTTL:         defaultJobTTL,
		MaxConcurrency: defaultMaxConcurrency,
		APIKey:         os.Getenv(envAPIKey),
		APIBaseURL:     buildkitdockerfile.DefaultLLMAPIBaseURL,
		Model:          os.Getenv(envModel),
	}

	if cfg.APIKey == "" {
		return nil, errors.Errorf("%s is required", envAPIKey)
	}

	if cfg.Model == "" {
		return nil, errors.Errorf("%s is required", envModel)
	}

	if v := os.Getenv(envListenAddr); v != "" {
		cfg.ListenAddr = v
	}

	if v := os.Getenv(envAPIBaseURL); v != "" {
		cfg.APIBaseURL = v
	}

	if v := os.Getenv(envJobTTL); v != "" {
		ttl, err := time.ParseDuration(v)
		if err != nil || ttl <= 0 {
			return nil, errors.Errorf("invalid %s %q: must be a positive duration like 1h", envJobTTL, v)
		}
		cfg.JobTTL = ttl
	}

	if v := os.Getenv(envMaxConcurrency); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, errors.Errorf("invalid %s %q: must be a positive integer", envMaxConcurrency, v)
		}
		cfg.MaxConcurrency = n
	}

	return cfg, nil
}
