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
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/goharbor/harbor/src/lib"
	"github.com/goharbor/harbor/src/lib/log"
	"github.com/goharbor/harbor/src/pkg/buildkitdockerfile"
	v1 "github.com/goharbor/harbor/src/pkg/optimizer/rest/v1"
	"github.com/goharbor/harbor/src/pkg/registry"
)

// workerTimeout bounds one optimization end to end (registry pulls + LLM call).
const workerTimeout = 5 * time.Minute

// newRegistryClient constructs the registry client used to pull artifact blobs.
// Extracted as a var (rather than calling registry.NewClientWithAuthorizer directly
// in run()) so tests can substitute a fake registry.Client instead of standing up a
// real HTTP registry to exercise run()'s error-classification branches.
var newRegistryClient = func(url string, authorizer lib.Authorizer, insecure bool) registry.Client {
	return registry.NewClientWithAuthorizer(url, authorizer, insecure, "")
}

// rawAuthorizer replays the Authorization header value Harbor put into the optimize
// request (Basic robot credential or Bearer token) verbatim on every registry request.
type rawAuthorizer struct {
	header string
}

// Modify implements lib.Authorizer / modifier.Modifier.
func (r *rawAuthorizer) Modify(req *http.Request) error {
	if r.header != "" {
		req.Header.Set("Authorization", r.header)
	}
	return nil
}

// process runs the optimization pipeline for one accepted request and stores the
// terminal report. All pipeline failures become Failed reports, never HTTP errors,
// so the polling Harbor job can always persist an outcome.
func (s *Server) process(id string, req *v1.OptimizeRequest) {
	ctx, cancel := context.WithTimeout(context.Background(), workerTimeout)
	defer cancel()

	report := s.run(ctx, req)
	s.store.complete(id, report)
	log.Infof("optimization %s finished with status %s", id, report.Status)
}

func (s *Server) run(ctx context.Context, req *v1.OptimizeRequest) *v1.OptimizationReport {
	// 1. Pull the artifact blobs from the Harbor registry with the handed credential.
	cli := newRegistryClient(req.Registry.URL, &rawAuthorizer{header: req.Registry.Authorization}, req.Registry.Insecure)

	src := &buildkitdockerfile.RegistryBlobSource{
		Client:     cli,
		Repository: req.Artifact.Repository,
		Reference:  req.Artifact.Digest,
	}

	// 2. Extract the Dockerfile from the BuildKit provenance attestation.
	result, err := buildkitdockerfile.NewWorkflow().ExtractDockerfileFromSource(ctx, src)
	if err != nil {
		if !strings.Contains(err.Error(), "attestation manifest not found") {
			return failedReport(v1.ErrorCodeExtractionFailed, err.Error())
		}

		// No BuildKit provenance attestation on this artifact (e.g. it wasn't
		// built with BuildKit, or was pulled/re-pushed without one): fall back
		// to reconstructing a best-effort Dockerfile from the image's own
		// config history rather than failing outright.
		generated, genErr := buildkitdockerfile.GenerateDockerfileFromSource(ctx, src)
		if genErr != nil {
			return failedReport(v1.ErrorCodeNoAttestation,
				fmt.Sprintf("no provenance attestation, and Dockerfile generation fallback also failed: %s", genErr.Error()))
		}
		result = generated
	}

	// 3. Optimize via the LLM gateway.
	optimized, err := buildkitdockerfile.OptimizeDockerfile(ctx, s.cfg.APIBaseURL, s.cfg.APIKey, s.cfg.Model, result.Dockerfile)
	if err != nil {
		return failedReport(v1.ErrorCodeLLMFailed, err.Error())
	}

	return &v1.OptimizationReport{
		Status:                    v1.ReportStatusSuccess,
		Dockerfile:                result.Dockerfile,
		OptimizedDockerfile:       stripCodeFences(optimized),
		AttestationManifestDigest: result.AttestationManifestDigest,
		StatementDigest:           result.StatementDigest,
		Generated:                 result.Generated,
	}
}

func failedReport(code, message string) *v1.OptimizationReport {
	return &v1.OptimizationReport{
		Status: v1.ReportStatusFailed,
		Error: &v1.ReportError{
			Code:    code,
			Message: message,
		},
	}
}

// stripCodeFences removes a leading ``` / ```dockerfile line and a trailing ```
// line that LLM gateways commonly wrap around the returned Dockerfile.
func stripCodeFences(s string) string {
	out := strings.TrimSpace(s)

	if strings.HasPrefix(out, "```") {
		if idx := strings.IndexByte(out, '\n'); idx >= 0 {
			out = out[idx+1:]
		} else {
			// A lone fence line with no content
			return ""
		}
	}

	if strings.HasSuffix(strings.TrimSpace(out), "```") {
		trimmed := strings.TrimSpace(out)
		trimmed = strings.TrimSuffix(trimmed, "```")
		out = strings.TrimRight(trimmed, " \t\n")
	}

	return strings.TrimSpace(out) + "\n"
}
