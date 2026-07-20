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
	"context"
	"encoding/json"
	"net/http"
	"testing"

	buildkitdockerfilectl "github.com/goharbor/harbor/src/controller/buildkitdockerfile"
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
	t.Cleanup(func() {
		buildkitDockerfileCtl = oldCtl
	})

	buildkitDockerfileCtl = &stubBuildkitDockerfileController{
		result: &buildkitdockerfilectl.Result{
			Dockerfile:                "FROM scratch\n",
			AttestationManifestDigest: "sha256:attestation",
			StatementDigest:           "sha256:statement",
		},
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
}

type stubBuildkitDockerfileController struct {
	result *buildkitdockerfilectl.Result
	err    error
}

func (s *stubBuildkitDockerfileController) ExtractDockerfile(_ context.Context, _ string) (*buildkitdockerfilectl.Result, error) {
	return s.result, s.err
}
