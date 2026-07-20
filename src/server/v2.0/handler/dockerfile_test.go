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

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-openapi/runtime"
	"github.com/stretchr/testify/require"

	"github.com/goharbor/harbor/src/common/security"
	"github.com/goharbor/harbor/src/controller/artifact"
	buildkitdockerfilectl "github.com/goharbor/harbor/src/controller/buildkitdockerfile"
	"github.com/goharbor/harbor/src/controller/project"
	liberrors "github.com/goharbor/harbor/src/lib/errors"
	dockerfileoptdao "github.com/goharbor/harbor/src/pkg/dockerfileoptimization/dao"
	operation "github.com/goharbor/harbor/src/server/v2.0/restapi/operations/dockerfile"
	artifacttesting "github.com/goharbor/harbor/src/testing/controller/artifact"
	"github.com/goharbor/harbor/src/testing/mock"
	securitytesting "github.com/goharbor/harbor/src/testing/common/security"
)

// stubDockerfileCtl is a simple in-test stub for buildkitdockerfilectl.Controller.
type stubDockerfileCtl struct {
	result *buildkitdockerfilectl.Result
	err    error
}

func (s *stubDockerfileCtl) ExtractDockerfile(_ context.Context, _ string) (*buildkitdockerfilectl.Result, error) {
	return s.result, s.err
}
func (s *stubDockerfileCtl) ExtractDockerfileFromReader(_ context.Context, _ io.Reader) (*buildkitdockerfilectl.Result, error) {
	return s.result, s.err
}
func (s *stubDockerfileCtl) ExtractDockerfileFromSource(_ context.Context, _ buildkitdockerfilectl.BlobSource) (*buildkitdockerfilectl.Result, error) {
	return s.result, s.err
}

// stubOptDAO is a simple in-test stub for dockerfileoptdao.DAO.
type stubOptDAO struct {
	upsertErr   error
	upsertedRec *dockerfileoptdao.DockerfileOptimization // captured by Upsert
	getResult   *dockerfileoptdao.DockerfileOptimization
	getErr      error
}

func (s *stubOptDAO) Upsert(_ context.Context, rec *dockerfileoptdao.DockerfileOptimization) error {
	s.upsertedRec = rec
	return s.upsertErr
}
func (s *stubOptDAO) GetByArtifact(_ context.Context, _, _ string) (*dockerfileoptdao.DockerfileOptimization, error) {
	return s.getResult, s.getErr
}

// ctxWithSecurity returns a context with a mock security context that grants
// access (IsAuthenticated=true, Can=true).
func ctxWithSecurity() context.Context {
	sec := &securitytesting.Context{}
	sec.On("IsAuthenticated").Return(true)
	mock.OnAnything(sec, "Can").Return(true)

	// We also need baseProjectCtl to resolve project name → ID.
	// Use the package-level mock set up by TestMain.
	mock.OnAnything(projectCtlMock, "GetByName").Return(&project.Project{ProjectID: 1}, nil)

	return security.NewContext(context.Background(), sec)
}

// newTestDockerfileAPI creates a dockerfileAPI with stubbed dependencies.
func newTestDockerfileAPI(
	artCtl artifact.Controller,
	dockerCtl buildkitdockerfilectl.Controller,
	optDAO dockerfileoptdao.DAO,
) *dockerfileAPI {
	return &dockerfileAPI{
		artCtl:    artCtl,
		dockerCtl: dockerCtl,
		optDAO:    optDAO,
	}
}

func TestGetDockerfileOptimization_HappyPath(t *testing.T) {
	artCtl := &artifacttesting.Controller{}
	art := &artifact.Artifact{}
	art.RepositoryName = "library/myrepo"
	art.Digest = "sha256:abc123"
	mock.OnAnything(artCtl, "GetByReference").Return(art, nil).Once()

	ts := time.Now().Truncate(time.Second)
	optDAO := &stubOptDAO{
		getResult: &dockerfileoptdao.DockerfileOptimization{
			Dockerfile:          "FROM alpine",
			OptimizedDockerfile: "FROM alpine:3.21",
			CreatedAt:           ts,
		},
	}

	api := newTestDockerfileAPI(artCtl, &stubDockerfileCtl{}, optDAO)
	params := operation.GetDockerfileOptimizationParams{
		HTTPRequest:    &http.Request{},
		ProjectName:    "library",
		RepositoryName: "myrepo",
		Reference:      "latest",
	}

	responder := api.GetDockerfileOptimization(ctxWithSecurity(), params)

	rw := httptest.NewRecorder()
	responder.WriteResponse(rw, runtime.JSONProducer())
	require.Equal(t, 200, rw.Code)
}

func TestGetDockerfileOptimization_NotFound(t *testing.T) {
	artCtl := &artifacttesting.Controller{}
	art := &artifact.Artifact{}
	art.RepositoryName = "library/myrepo"
	art.Digest = "sha256:abc123"
	mock.OnAnything(artCtl, "GetByReference").Return(art, nil).Once()

	optDAO := &stubOptDAO{
		getErr: liberrors.New(nil).WithCode(liberrors.NotFoundCode).WithMessage("not found"),
	}

	api := newTestDockerfileAPI(artCtl, &stubDockerfileCtl{}, optDAO)
	params := operation.GetDockerfileOptimizationParams{
		HTTPRequest:    &http.Request{},
		ProjectName:    "library",
		RepositoryName: "myrepo",
		Reference:      "latest",
	}

	responder := api.GetDockerfileOptimization(ctxWithSecurity(), params)

	rw := httptest.NewRecorder()
	responder.WriteResponse(rw, runtime.JSONProducer())
	require.Equal(t, 404, rw.Code)
}

func TestOptimizeDockerfile_NoAttestation(t *testing.T) {
	artCtl := &artifacttesting.Controller{}
	art := &artifact.Artifact{}
	art.RepositoryName = "library/myrepo"
	art.Digest = "sha256:abc123"
	mock.OnAnything(artCtl, "GetByReference").Return(art, nil).Once()

	dockerCtl := &stubDockerfileCtl{
		err: fmt.Errorf("attestation manifest not found in OCI index"),
	}

	api := newTestDockerfileAPI(artCtl, dockerCtl, &stubOptDAO{})
	params := operation.OptimizeDockerfileParams{
		HTTPRequest:    &http.Request{},
		ProjectName:    "library",
		RepositoryName: "myrepo",
		Reference:      "latest",
	}

	responder := api.OptimizeDockerfile(ctxWithSecurity(), params)

	rw := httptest.NewRecorder()
	responder.WriteResponse(rw, runtime.JSONProducer())
	require.Equal(t, 412, rw.Code)
}

func TestOptimizeDockerfile_ArtifactNotFound(t *testing.T) {
	artCtl := &artifacttesting.Controller{}
	mock.OnAnything(artCtl, "GetByReference").
		Return(nil, liberrors.NotFoundError(nil).WithMessage("artifact not found")).Once()

	api := newTestDockerfileAPI(artCtl, &stubDockerfileCtl{}, &stubOptDAO{})
	params := operation.OptimizeDockerfileParams{
		HTTPRequest:    &http.Request{},
		ProjectName:    "library",
		RepositoryName: "myrepo",
		Reference:      "latest",
	}

	responder := api.OptimizeDockerfile(ctxWithSecurity(), params)

	rw := httptest.NewRecorder()
	responder.WriteResponse(rw, runtime.JSONProducer())
	require.Equal(t, 404, rw.Code)
}

func TestOptimizeDockerfile_LLMNotConfigured(t *testing.T) {
	t.Setenv("LLMGW_API_KEY", "")

	artCtl := &artifacttesting.Controller{}
	art := &artifact.Artifact{}
	art.RepositoryName = "library/myrepo"
	art.Digest = "sha256:abc123"
	mock.OnAnything(artCtl, "GetByReference").Return(art, nil).Once()

	dockerCtl := &stubDockerfileCtl{
		result: &buildkitdockerfilectl.Result{
			Dockerfile:                "FROM scratch",
			AttestationManifestDigest: "sha256:att",
			StatementDigest:           "sha256:stmt",
		},
	}

	api := newTestDockerfileAPI(artCtl, dockerCtl, &stubOptDAO{})
	params := operation.OptimizeDockerfileParams{
		HTTPRequest:    &http.Request{},
		ProjectName:    "library",
		RepositoryName: "myrepo",
		Reference:      "latest",
	}

	responder := api.OptimizeDockerfile(ctxWithSecurity(), params)

	rw := httptest.NewRecorder()
	responder.WriteResponse(rw, runtime.JSONProducer())
	// OptimizeWithEnvConfig returns "LLM optimization is not configured" → 500
	require.Equal(t, 500, rw.Code)
}

// fakeLLMServer starts an httptest.Server that returns a minimal SSE stream
// containing the provided optimized Dockerfile content, then closes.
func fakeLLMServer(t *testing.T, optimizedDockerfile string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		chunk := fmt.Sprintf(`data: {"choices":[{"delta":{"content":%s}}]}`,
			func() string { b, _ := json.Marshal(optimizedDockerfile); return string(b) }())
		fmt.Fprintln(w, chunk)
		fmt.Fprintln(w, "data: [DONE]")
	}))
}

func TestOptimizeDockerfile_HappyPath(t *testing.T) {
	const wantOptimized = "FROM alpine:3.21\nRUN apk add --no-cache bash"

	srv := fakeLLMServer(t, wantOptimized)
	defer srv.Close()
	t.Setenv("LLMGW_API_KEY", "test-key")
	t.Setenv("LLMGW_API_BASE_URL", srv.URL)

	artCtl := &artifacttesting.Controller{}
	art := &artifact.Artifact{}
	art.RepositoryName = "library/myrepo"
	art.Digest = "sha256:abc123"
	mock.OnAnything(artCtl, "GetByReference").Return(art, nil).Once()

	dockerCtl := &stubDockerfileCtl{
		result: &buildkitdockerfilectl.Result{
			Dockerfile:                "FROM scratch",
			AttestationManifestDigest: "sha256:att000",
			StatementDigest:           "sha256:stmt000",
		},
	}
	optDAO := &stubOptDAO{}

	api := newTestDockerfileAPI(artCtl, dockerCtl, optDAO)
	params := operation.OptimizeDockerfileParams{
		HTTPRequest:    &http.Request{},
		ProjectName:    "library",
		RepositoryName: "myrepo",
		Reference:      "latest",
	}

	responder := api.OptimizeDockerfile(ctxWithSecurity(), params)

	rw := httptest.NewRecorder()
	responder.WriteResponse(rw, runtime.JSONProducer())
	require.Equal(t, 200, rw.Code)

	// Decode response payload.
	var body struct {
		Dockerfile                string `json:"dockerfile"`
		OptimizedDockerfile       string `json:"optimized_dockerfile"`
		AttestationManifestDigest string `json:"attestation_manifest_digest"`
		StatementDigest           string `json:"statement_digest"`
	}
	require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &body))
	require.Equal(t, "FROM scratch", body.Dockerfile)
	require.Equal(t, wantOptimized, body.OptimizedDockerfile)
	require.Equal(t, "sha256:att000", body.AttestationManifestDigest)
	require.Equal(t, "sha256:stmt000", body.StatementDigest)

	// Verify the DAO received the correct record for persistence.
	require.NotNil(t, optDAO.upsertedRec)
	require.Equal(t, "library/myrepo", optDAO.upsertedRec.RepositoryName)
	require.Equal(t, "sha256:abc123", optDAO.upsertedRec.ArtifactDigest)
	require.Equal(t, "FROM scratch", optDAO.upsertedRec.Dockerfile)
	require.Equal(t, wantOptimized, optDAO.upsertedRec.OptimizedDockerfile)
}

func TestOptimizeDockerfile_UpsertError(t *testing.T) {
	srv := fakeLLMServer(t, "FROM alpine")
	defer srv.Close()
	t.Setenv("LLMGW_API_KEY", "test-key")
	t.Setenv("LLMGW_API_BASE_URL", srv.URL)

	artCtl := &artifacttesting.Controller{}
	art := &artifact.Artifact{}
	art.RepositoryName = "library/myrepo"
	art.Digest = "sha256:abc123"
	mock.OnAnything(artCtl, "GetByReference").Return(art, nil).Once()

	dockerCtl := &stubDockerfileCtl{
		result: &buildkitdockerfilectl.Result{
			Dockerfile:                "FROM scratch",
			AttestationManifestDigest: "sha256:att",
			StatementDigest:           "sha256:stmt",
		},
	}
	optDAO := &stubOptDAO{upsertErr: fmt.Errorf("db write failed")}

	api := newTestDockerfileAPI(artCtl, dockerCtl, optDAO)
	params := operation.OptimizeDockerfileParams{
		HTTPRequest:    &http.Request{},
		ProjectName:    "library",
		RepositoryName: "myrepo",
		Reference:      "latest",
	}

	responder := api.OptimizeDockerfile(ctxWithSecurity(), params)

	rw := httptest.NewRecorder()
	responder.WriteResponse(rw, runtime.JSONProducer())
	require.Equal(t, 500, rw.Code)
}
