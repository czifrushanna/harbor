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
	"errors"
	"io"
	"testing"

	workflow "github.com/goharbor/harbor/src/pkg/buildkitdockerfile"
	"github.com/stretchr/testify/require"
)

type stubWorkflow struct {
	result *workflow.Result
	err    error
}

func (s *stubWorkflow) ExtractDockerfile(ctx context.Context, ociArchivePath string) (*workflow.Result, error) {
	return s.result, s.err
}

func (s *stubWorkflow) ExtractDockerfileFromReader(_ context.Context, _ io.Reader) (*workflow.Result, error) {
	return s.result, s.err
}

func (s *stubWorkflow) ExtractDockerfileFromSource(_ context.Context, _ workflow.BlobSource) (*workflow.Result, error) {
	return s.result, s.err
}

func TestControllerForwardsToWorkflow(t *testing.T) {
	expected := &workflow.Result{Dockerfile: "FROM scratch"}
	ctl := &controller{workflow: &stubWorkflow{result: expected}}

	result, err := ctl.ExtractDockerfile(context.Background(), "/tmp/image.oci")
	require.NoError(t, err)
	require.Same(t, expected, result)
}

func TestControllerReturnsWorkflowError(t *testing.T) {
	boom := errors.New("boom")
	ctl := &controller{workflow: &stubWorkflow{err: boom}}

	result, err := ctl.ExtractDockerfile(context.Background(), "/tmp/image.oci")
	require.ErrorIs(t, err, boom)
	require.Nil(t, result)
}

func TestControllerFromReaderForwards(t *testing.T) {
	expected := &workflow.Result{Dockerfile: "FROM scratch"}
	ctl := &controller{workflow: &stubWorkflow{result: expected}}

	result, err := ctl.ExtractDockerfileFromReader(context.Background(), nil)
	require.NoError(t, err)
	require.Same(t, expected, result)
}

func TestControllerFromSourceForwards(t *testing.T) {
	expected := &workflow.Result{Dockerfile: "FROM alpine"}
	ctl := &controller{workflow: &stubWorkflow{result: expected}}

	result, err := ctl.ExtractDockerfileFromSource(context.Background(), nil)
	require.NoError(t, err)
	require.Same(t, expected, result)
}
