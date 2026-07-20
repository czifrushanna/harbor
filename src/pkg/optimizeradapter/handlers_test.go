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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	v1 "github.com/goharbor/harbor/src/pkg/optimizer/rest/v1"
)

func testServer() *Server {
	return NewServer(&Config{
		ListenAddr:     ":0",
		JobTTL:         time.Hour,
		MaxConcurrency: 1,
		APIKey:         "test-key",
		APIBaseURL:     "http://llm.invalid",
		Model:          "test-model",
	})
}

func TestMetadataEndpoint(t *testing.T) {
	srv := httptest.NewServer(testServer().Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/metadata")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, v1.MimeTypeAdapterMeta, resp.Header.Get("Content-Type"))

	meta := &v1.OptimizerAdapterMetadata{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(meta))
	// The served document must pass the same validation Harbor runs at registration.
	require.NoError(t, meta.Validate())
	require.True(t, meta.HasCapability(v1.MimeTypeOCIIndex))
}

func TestSubmitOptimize_InvalidBody(t *testing.T) {
	srv := httptest.NewServer(testServer().Handler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/v1/optimize", v1.MimeTypeOptimizeRequest, strings.NewReader("{not json"))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestSubmitOptimize_MissingArtifact(t *testing.T) {
	srv := httptest.NewServer(testServer().Handler())
	defer srv.Close()

	body := `{"registry": {"url": "http://harbor-core"}}`
	resp, err := http.Post(srv.URL+"/api/v1/optimize", v1.MimeTypeOptimizeRequest, strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

func TestGetReport_UnknownID(t *testing.T) {
	srv := httptest.NewServer(testServer().Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/optimize/no-such-id/report")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestGetReport_RunningThenReady(t *testing.T) {
	s := testServer()
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	s.store.put("job-1")

	// The 302 signal must not be followed by the test client.
	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get(srv.URL + "/api/v1/optimize/job-1/report")
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Equal(t, "5", resp.Header.Get("Refresh-After"))

	s.store.complete("job-1", &v1.OptimizationReport{
		Status:     v1.ReportStatusSuccess,
		Dockerfile: "FROM scratch",
	})

	resp, err = client.Get(srv.URL + "/api/v1/optimize/job-1/report")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	report := &v1.OptimizationReport{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(report))
	require.Equal(t, v1.ReportStatusSuccess, report.Status)
}

func TestHealthz(t *testing.T) {
	srv := httptest.NewServer(testServer().Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/probe/healthy")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestStoreSweep(t *testing.T) {
	st := newStore(time.Hour)
	st.put("old")
	st.entries["old"].createdAt = time.Now().Add(-2 * time.Hour)
	st.put("fresh")

	cutoff := time.Now().Add(-st.ttl)
	st.mu.Lock()
	for id, e := range st.entries {
		if e.createdAt.Before(cutoff) {
			delete(st.entries, id)
		}
	}
	st.mu.Unlock()

	require.Nil(t, st.get("old"))
	require.NotNil(t, st.get("fresh"))
}

func TestStripCodeFences(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "FROM alpine\nRUN true", "FROM alpine\nRUN true\n"},
		{"bare fences", "```\nFROM alpine\n```", "FROM alpine\n"},
		{"dockerfile fences", "```dockerfile\nFROM alpine\nRUN true\n```", "FROM alpine\nRUN true\n"},
		{"leading whitespace", "\n\n```dockerfile\nFROM alpine\n```\n\n", "FROM alpine\n"},
		{"only fence", "```", ""},
		{"no trailing fence", "```dockerfile\nFROM alpine", "FROM alpine\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, stripCodeFences(c.in))
		})
	}
}
