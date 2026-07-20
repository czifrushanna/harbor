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
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/docker/distribution"
	"github.com/stretchr/testify/require"

	"github.com/goharbor/harbor/src/lib"
	v1 "github.com/goharbor/harbor/src/pkg/optimizer/rest/v1"
	"github.com/goharbor/harbor/src/pkg/registry"
	registrytesting "github.com/goharbor/harbor/src/testing/pkg/registry"
)

// stubManifest is a minimal distribution.Manifest for tests; RegistryBlobSource
// only ever calls Payload() on what PullManifest returns.
type stubManifest struct {
	data []byte
}

func (s *stubManifest) References() []distribution.Descriptor { return nil }
func (s *stubManifest) Payload() (string, []byte, error)      { return "application/json", s.data, nil }

// withFakeRegistryClient overrides newRegistryClient for the duration of the test
// so run() talks to a mock registry.Client instead of a real HTTP endpoint.
func withFakeRegistryClient(t *testing.T, cli registry.Client) {
	t.Helper()
	orig := newRegistryClient
	newRegistryClient = func(_ string, _ lib.Authorizer, _ bool) registry.Client { return cli }
	t.Cleanup(func() { newRegistryClient = orig })
}

func sampleOptimizeRequest() *v1.OptimizeRequest {
	return &v1.OptimizeRequest{
		Registry: &v1.Registry{URL: "http://registry.invalid", Authorization: "Basic dXNlcjpwYXNz"},
		Artifact: &v1.Artifact{Repository: "library/photon", Digest: "sha256:artifact"},
	}
}

func TestRawAuthorizer_ModifySetsHeader(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)

	a := &rawAuthorizer{header: "Basic dGVzdA=="}
	require.NoError(t, a.Modify(req))
	require.Equal(t, "Basic dGVzdA==", req.Header.Get("Authorization"))
}

func TestRawAuthorizer_ModifyEmptyHeaderLeavesRequestUnchanged(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	require.NoError(t, err)

	a := &rawAuthorizer{header: ""}
	require.NoError(t, a.Modify(req))
	require.Empty(t, req.Header.Get("Authorization"))
}

func TestFailedReport(t *testing.T) {
	report := failedReport(v1.ErrorCodeLLMFailed, "boom")
	require.Equal(t, v1.ReportStatusFailed, report.Status)
	require.NotNil(t, report.Error)
	require.Equal(t, v1.ErrorCodeLLMFailed, report.Error.Code)
	require.Equal(t, "boom", report.Error.Message)
}

func TestRun_NoAttestation(t *testing.T) {
	idx := []byte(`{"manifests":[]}`)

	cli := &registrytesting.Client{}
	cli.On("PullManifest", "library/photon", "sha256:artifact").Return(&stubManifest{data: idx}, "sha256:artifact", nil)
	withFakeRegistryClient(t, cli)

	s := testServer()
	report := s.run(context.Background(), sampleOptimizeRequest())

	require.Equal(t, v1.ReportStatusFailed, report.Status)
	require.NotNil(t, report.Error)
	require.Equal(t, v1.ErrorCodeNoAttestation, report.Error.Code)
}

func TestRun_ExtractionFailedOnRegistryError(t *testing.T) {
	cli := &registrytesting.Client{}
	cli.On("PullManifest", "library/photon", "sha256:artifact").Return(nil, "", errors.New("registry unavailable"))
	withFakeRegistryClient(t, cli)

	s := testServer()
	report := s.run(context.Background(), sampleOptimizeRequest())

	require.Equal(t, v1.ReportStatusFailed, report.Status)
	require.NotNil(t, report.Error)
	require.Equal(t, v1.ErrorCodeExtractionFailed, report.Error.Code)
}

// buildValidChain returns index/attestation/statement bytes for a full
// extraction success, following the same shape used by
// pkg/buildkitdockerfile's own workflow tests.
func buildValidChain(t *testing.T, dockerfile string) (idx, attestation, statement []byte) {
	t.Helper()

	encoded := base64.StdEncoding.EncodeToString([]byte(dockerfile))

	indexJSON := map[string]any{
		"manifests": []map[string]any{{
			"digest": "sha256:attestation",
			"annotations": map[string]string{
				"vnd.docker.reference.type": "attestation-manifest",
			},
		}},
	}
	attestationJSON := map[string]any{
		"layers": []map[string]string{{"digest": "sha256:statement"}},
	}
	statementJSON := map[string]any{
		"predicate": map[string]any{
			"runDetails": map[string]any{
				"metadata": map[string]any{
					"buildkit_metadata": map[string]any{
						"source": map[string]any{
							"infos": []map[string]string{{"data": encoded}},
						},
					},
				},
			},
		},
	}

	var err error
	idx, err = json.Marshal(indexJSON)
	require.NoError(t, err)
	attestation, err = json.Marshal(attestationJSON)
	require.NoError(t, err)
	statement, err = json.Marshal(statementJSON)
	require.NoError(t, err)
	return idx, attestation, statement
}

func TestRun_SuccessAndLLMFailedShareExtraction(t *testing.T) {
	idx, attestation, statement := buildValidChain(t, "FROM alpine:3.20\nRUN echo hello\n")

	newCli := func() *registrytesting.Client {
		cli := &registrytesting.Client{}
		cli.On("PullManifest", "library/photon", "sha256:artifact").Return(&stubManifest{data: idx}, "sha256:artifact", nil)
		cli.On("PullManifest", "library/photon", "sha256:attestation").Return(&stubManifest{data: attestation}, "sha256:attestation", nil)
		cli.On("PullManifest", "library/photon", "sha256:statement").Return(&stubManifest{data: statement}, "sha256:statement", nil)
		return cli
	}

	t.Run("LLM failure surfaces as LLM_FAILED", func(t *testing.T) {
		withFakeRegistryClient(t, newCli())

		llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "gateway error")
		}))
		defer llm.Close()

		s := NewServer(&Config{APIKey: "key", APIBaseURL: llm.URL, Model: "test-model", MaxConcurrency: 1})
		report := s.run(context.Background(), sampleOptimizeRequest())

		require.Equal(t, v1.ReportStatusFailed, report.Status)
		require.NotNil(t, report.Error)
		require.Equal(t, v1.ErrorCodeLLMFailed, report.Error.Code)
	})

	t.Run("success end to end, including code-fence stripping", func(t *testing.T) {
		withFakeRegistryClient(t, newCli())

		llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"`+"```dockerfile\\nFROM alpine:3.21\\n```"+`"}}]}`)
			fmt.Fprintln(w, "data: [DONE]")
		}))
		defer llm.Close()

		s := NewServer(&Config{APIKey: "key", APIBaseURL: llm.URL, Model: "test-model", MaxConcurrency: 1})
		report := s.run(context.Background(), sampleOptimizeRequest())

		require.Equal(t, v1.ReportStatusSuccess, report.Status)
		require.Equal(t, "FROM alpine:3.20\nRUN echo hello\n", report.Dockerfile)
		require.Equal(t, "FROM alpine:3.21\n", report.OptimizedDockerfile)
		require.Equal(t, "sha256:attestation", report.AttestationManifestDigest)
		require.Equal(t, "sha256:statement", report.StatementDigest)
	})
}

func TestProcess_StoresTerminalReport(t *testing.T) {
	idx := []byte(`{"manifests":[]}`)
	cli := &registrytesting.Client{}
	cli.On("PullManifest", "library/photon", "sha256:artifact").Return(&stubManifest{data: idx}, "sha256:artifact", nil)
	withFakeRegistryClient(t, cli)

	s := testServer()
	s.store.put("job-1")

	s.process("job-1", sampleOptimizeRequest())

	entry := s.store.get("job-1")
	require.NotNil(t, entry)
	require.NotNil(t, entry.report)
	require.Equal(t, v1.ReportStatusFailed, entry.report.Status)
	require.Equal(t, v1.ErrorCodeNoAttestation, entry.report.Error.Code)
}
