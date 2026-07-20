// Copyright 2018 Project Harbor Authors
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

package api

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	buildkitdockerfilectl "github.com/goharbor/harbor/src/controller/buildkitdockerfile"
	"github.com/stretchr/testify/require"
)

// cannot verify the real scenario here
func TestSyncQuota(t *testing.T) {
	cases := []*codeCheckingCase{
		// 401
		{
			request: &testingRequest{
				method: http.MethodPost,
				url:    "/api/internal/syncquota",
			},
			code: http.StatusUnauthorized,
		},
		// 200
		{
			request: &testingRequest{
				method:     http.MethodPost,
				url:        "/api/internal/syncquota",
				credential: sysAdmin,
			},
			code: http.StatusOK,
		},
		// 403
		{
			request: &testingRequest{
				url:        "/api/internal/syncquota",
				method:     http.MethodPost,
				credential: nonSysAdmin,
			},
			code: http.StatusForbidden,
		},
	}
	runCodeCheckingCases(t, cases...)
}

func TestExtractBuildkitDockerfile(t *testing.T) {
	oldCtl := buildkitDockerfileCtl
	oldOptimize := buildkitDockerfileOptimize
	t.Cleanup(func() {
		buildkitDockerfileCtl = oldCtl
		buildkitDockerfileOptimize = oldOptimize
	})

	buildkitDockerfileCtl = &stubBuildkitDockerfileController{
		result: &buildkitdockerfilectl.Result{
			Dockerfile:                "FROM scratch\n",
			AttestationManifestDigest: "sha256:attestation",
			StatementDigest:           "sha256:statement",
		},
	}
	buildkitDockerfileOptimize = func(_ context.Context, dockerfile string) (string, error) {
		return dockerfile + "# optimized\n", nil
	}

	cases := []*codeCheckingCase{
		{
			request: &testingRequest{
				method: http.MethodPost,
				url:    "/api/internal/buildkitdockerfile/extract",
			},
			code: http.StatusUnauthorized,
		},
		{
			request: &testingRequest{
				method:     http.MethodPost,
				url:        "/api/internal/buildkitdockerfile/extract",
				credential: nonSysAdmin,
			},
			code: http.StatusForbidden,
		},
	}
	runCodeCheckingCases(t, cases...)

	resp, err := handle(&testingRequest{
		method: http.MethodPost,
		url:    "/api/internal/buildkitdockerfile/extract",
		bodyJSON: map[string]any{
			"oci_archive_path": "/tmp/image.oci",
		},
		credential: sysAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected status code %d: %s", resp.Code, resp.Body.String())
	}

	var payload buildkitDockerfileExtractResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Dockerfile != "FROM scratch\n" {
		t.Fatalf("unexpected dockerfile %q", payload.Dockerfile)
	}
	if payload.AttestationManifestDigest != "sha256:attestation" {
		t.Fatalf("unexpected attestation digest %q", payload.AttestationManifestDigest)
	}
	if payload.StatementDigest != "sha256:statement" {
		t.Fatalf("unexpected statement digest %q", payload.StatementDigest)
	}

	t.Setenv("LLMGW_API_KEY_ENV", "TEST_LLMGW_API_KEY")
	t.Setenv("TEST_LLMGW_API_KEY", "dummy")
	buildkitDockerfileCtl.(*stubBuildkitDockerfileController).readerResult = &buildkitdockerfilectl.Result{
		Dockerfile:                "FROM uploaded\n",
		AttestationManifestDigest: "sha256:upload-attestation",
		StatementDigest:           "sha256:upload-statement",
	}
	optimizedResp, err := handle(&testingRequest{
		method: http.MethodPost,
		url:    "/api/internal/buildkitdockerfile/extract",
		bodyJSON: map[string]any{
			"oci_archive_path": "/tmp/image.oci",
			"optimize":         true,
		},
		credential: sysAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	if optimizedResp.Code != http.StatusOK {
		t.Fatalf("unexpected status code %d: %s", optimizedResp.Code, optimizedResp.Body.String())
	}

	var optimizedPayload buildkitDockerfileExtractResponse
	if err := json.Unmarshal(optimizedResp.Body.Bytes(), &optimizedPayload); err != nil {
		t.Fatal(err)
	}
	if optimizedPayload.OptimizedDockerfile != "FROM scratch\n# optimized\n" {
		t.Fatalf("unexpected optimized dockerfile %q", optimizedPayload.OptimizedDockerfile)
	}

	uploadedArchive := createOCIArchive(t, "FROM alpine:3.20\nRUN echo hello\n")
	t.Setenv("LLMGW_API_KEY_ENV", "TEST_LLMGW_API_KEY")
	t.Setenv("TEST_LLMGW_API_KEY", "dummy")
	readerStub := buildkitDockerfileCtl.(*stubBuildkitDockerfileController)
	readerStub.readerResult = &buildkitdockerfilectl.Result{
		Dockerfile:                "FROM alpine:3.20\nRUN echo hello\n",
		AttestationManifestDigest: "sha256:upload-attestation",
		StatementDigest:           "sha256:upload-statement",
	}
	uploadedResp, err := handleMultipart(&testingRequest{credential: sysAdmin}, uploadedArchive, true)
	if err != nil {
		t.Fatal(err)
	}
	if uploadedResp.Code != http.StatusOK {
		t.Fatalf("unexpected status code %d: %s", uploadedResp.Code, uploadedResp.Body.String())
	}
	var uploadedPayload buildkitDockerfileExtractResponse
	if err := json.Unmarshal(uploadedResp.Body.Bytes(), &uploadedPayload); err != nil {
		t.Fatal(err)
	}
	if !readerStub.readerCalled {
		t.Fatal("expected multipart upload to call reader-based extractor")
	}
	if uploadedPayload.Dockerfile != "FROM alpine:3.20\nRUN echo hello\n" {
		t.Fatalf("unexpected uploaded dockerfile %q", uploadedPayload.Dockerfile)
	}
	if uploadedPayload.OptimizedDockerfile != "FROM alpine:3.20\nRUN echo hello\n# optimized\n" {
		t.Fatalf("unexpected uploaded optimized dockerfile %q", uploadedPayload.OptimizedDockerfile)
	}
}

func handleMultipart(r *testingRequest, archive []byte, optimize bool) (*httptest.ResponseRecorder, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("optimize", strconv.FormatBool(optimize)); err != nil {
		return nil, err
	}
	part, err := writer.CreateFormFile("oci_archive", "image.oci")
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, bytes.NewReader(archive)); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, "/api/internal/buildkitdockerfile/extract", &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if r != nil && r.credential != nil {
		req.SetBasicAuth(r.credential.Name, r.credential.Passwd)
	}

	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp, nil
}

func createOCIArchive(t *testing.T, dockerfile string) []byte {
	t.Helper()

	encodedDockerfile := base64.StdEncoding.EncodeToString([]byte(dockerfile))
	indexJSON := map[string]any{
		"manifests": []map[string]any{{
			"digest": "sha256:attestation",
			"annotations": map[string]string{
				"vnd.docker.reference.type": "attestation-manifest",
			},
		}},
	}
	attestationJSON := map[string]any{"layers": []map[string]string{{"digest": "sha256:statement"}}}
	statementJSON := map[string]any{
		"predicate": map[string]any{
			"runDetails": map[string]any{
				"metadata": map[string]any{
					"buildkit_metadata": map[string]any{
						"source": map[string]any{
							"infos": []map[string]string{{"data": encodedDockerfile}},
						},
					},
				},
			},
		},
	}

	indexContent, err := json.Marshal(indexJSON)
	require.NoError(t, err)
	attestationContent, err := json.Marshal(attestationJSON)
	require.NoError(t, err)
	statementContent, err := json.Marshal(statementJSON)
	require.NoError(t, err)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "index.json", Mode: 0600, Size: int64(len(indexContent))}))
	_, err = tw.Write(indexContent)
	require.NoError(t, err)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "blobs/sha256/attestation", Mode: 0600, Size: int64(len(attestationContent))}))
	_, err = tw.Write(attestationContent)
	require.NoError(t, err)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "blobs/sha256/statement", Mode: 0600, Size: int64(len(statementContent))}))
	_, err = tw.Write(statementContent)
	require.NoError(t, err)
	require.NoError(t, tw.Close())

	return buf.Bytes()
}

type stubBuildkitDockerfileController struct {
	result       *buildkitdockerfilectl.Result
	readerResult *buildkitdockerfilectl.Result
	err          error
	readerCalled bool
}

func (s *stubBuildkitDockerfileController) ExtractDockerfile(_ context.Context, _ string) (*buildkitdockerfilectl.Result, error) {
	return s.result, s.err
}

func (s *stubBuildkitDockerfileController) ExtractDockerfileFromReader(_ context.Context, _ io.Reader) (*buildkitdockerfilectl.Result, error) {
	s.readerCalled = true
	if s.readerResult != nil {
		return s.readerResult, s.err
	}
	return s.result, s.err
}

func (s *stubBuildkitDockerfileController) ExtractDockerfileFromSource(_ context.Context, _ buildkitdockerfilectl.BlobSource) (*buildkitdockerfilectl.Result, error) {
	return s.result, s.err
}
