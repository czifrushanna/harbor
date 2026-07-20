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

package buildkitdockerfile

import (
	"context"
	"io"

	"github.com/goharbor/harbor/src/pkg/buildkitdockerfile"
)

// Result is the extracted Dockerfile and OCI digest metadata.
type Result = buildkitdockerfile.Result

// Controller provides the BuildKit Dockerfile extraction entry point.
type Controller interface {
	ExtractDockerfile(ctx context.Context, ociArchivePath string) (*Result, error)
	ExtractDockerfileFromReader(ctx context.Context, archive io.Reader) (*Result, error)
}

// DefaultController is the default adapter implementation.
var DefaultController = New()

// New returns a default BuildKit Dockerfile controller.
func New() Controller {
	return &controller{
		workflow: buildkitdockerfile.NewWorkflow(),
	}
}

type controller struct {
	workflow buildkitdockerfile.Workflow
}

func (c *controller) ExtractDockerfile(ctx context.Context, ociArchivePath string) (*Result, error) {
	return c.workflow.ExtractDockerfile(ctx, ociArchivePath)
}

func (c *controller) ExtractDockerfileFromReader(ctx context.Context, archive io.Reader) (*Result, error) {
	return c.workflow.ExtractDockerfileFromReader(ctx, archive)
}
