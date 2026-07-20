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

package optimizeradapter

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/goharbor/harbor/src/pkg/buildkitdockerfile"
)

// setBaseEnv clears every adapter env var so each test starts from a known state
// regardless of what the surrounding environment has set.
func setBaseEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{envAPIKey, envModel, envAPIBaseURL, envListenAddr, envJobTTL, envMaxConcurrency} {
		t.Setenv(k, "")
	}
}

func TestLoadConfig_APIKeyRequired(t *testing.T) {
	setBaseEnv(t)
	t.Setenv(envModel, "some-model")

	_, err := LoadConfig()
	require.Error(t, err)
	require.Contains(t, err.Error(), envAPIKey)
}

func TestLoadConfig_ModelRequired(t *testing.T) {
	setBaseEnv(t)
	t.Setenv(envAPIKey, "key")

	_, err := LoadConfig()
	require.Error(t, err)
	require.Contains(t, err.Error(), envModel)
}

func TestLoadConfig_Defaults(t *testing.T) {
	setBaseEnv(t)
	t.Setenv(envAPIKey, "key")
	t.Setenv(envModel, "some-model")

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "key", cfg.APIKey)
	require.Equal(t, "some-model", cfg.Model)
	require.Equal(t, buildkitdockerfile.DefaultLLMAPIBaseURL, cfg.APIBaseURL)
	require.Equal(t, defaultListenAddr, cfg.ListenAddr)
	require.Equal(t, defaultJobTTL, cfg.JobTTL)
	require.Equal(t, defaultMaxConcurrency, cfg.MaxConcurrency)
}

func TestLoadConfig_Overrides(t *testing.T) {
	setBaseEnv(t)
	t.Setenv(envAPIKey, "key")
	t.Setenv(envModel, "some-model")
	t.Setenv(envAPIBaseURL, "https://example.test/v1/chat")
	t.Setenv(envListenAddr, ":9090")
	t.Setenv(envJobTTL, "30m")
	t.Setenv(envMaxConcurrency, "8")

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "https://example.test/v1/chat", cfg.APIBaseURL)
	require.Equal(t, ":9090", cfg.ListenAddr)
	require.Equal(t, 30*time.Minute, cfg.JobTTL)
	require.Equal(t, 8, cfg.MaxConcurrency)
}

func TestLoadConfig_InvalidValues(t *testing.T) {
	cases := map[string]string{
		envJobTTL:         "not-a-duration",
		envMaxConcurrency: "-1",
	}
	for env, bad := range cases {
		t.Run(env, func(t *testing.T) {
			setBaseEnv(t)
			t.Setenv(envAPIKey, "key")
			t.Setenv(envModel, "some-model")
			t.Setenv(env, bad)

			_, err := LoadConfig()
			require.Error(t, err)
			require.Contains(t, err.Error(), env)
		})
	}
}
