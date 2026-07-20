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
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/docker/distribution"
	"github.com/stretchr/testify/require"

	registrytesting "github.com/goharbor/harbor/src/testing/pkg/registry"
)

// stubManifest is a minimal distribution.Manifest for tests.
type stubManifest struct {
	data    []byte
	payErr  error
}

func (s *stubManifest) References() []distribution.Descriptor { return nil }
func (s *stubManifest) Payload() (string, []byte, error) {
	return "application/json", s.data, s.payErr
}

func TestRegistryBlobSource_IndexSuccess(t *testing.T) {
	payload := []byte(`{"manifests":[]}`)
	cli := &registrytesting.Client{}
	cli.On("PullManifest", "library/repo", "latest").Return(&stubManifest{data: payload}, "d1", nil)

	src := &RegistryBlobSource{Client: cli, Repository: "library/repo", Reference: "latest"}
	got, err := src.Index(context.Background())
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

func TestRegistryBlobSource_IndexError(t *testing.T) {
	cli := &registrytesting.Client{}
	cli.On("PullManifest", "library/repo", "latest").Return(nil, "", errors.New("registry unavailable"))

	src := &RegistryBlobSource{Client: cli, Repository: "library/repo", Reference: "latest"}
	_, err := src.Index(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "pull index manifest")
}

func TestRegistryBlobSource_BlobViaManifest(t *testing.T) {
	blobData := []byte(`{"layers":[]}`)
	cli := &registrytesting.Client{}
	cli.On("PullManifest", "library/repo", "sha256:abc").Return(&stubManifest{data: blobData}, "sha256:abc", nil)

	src := &RegistryBlobSource{Client: cli, Repository: "library/repo", Reference: "latest"}
	got, err := src.Blob(context.Background(), "sha256:abc")
	require.NoError(t, err)
	require.Equal(t, blobData, got)
}

func TestRegistryBlobSource_BlobFallbackToBlob(t *testing.T) {
	blobData := []byte(`some raw blob content`)
	cli := &registrytesting.Client{}
	// PullManifest fails, so Blob falls back to PullBlob.
	cli.On("PullManifest", "library/repo", "sha256:def").Return(nil, "", errors.New("not a manifest"))
	cli.On("PullBlob", "library/repo", "sha256:def").
		Return(int64(len(blobData)), io.NopCloser(bytes.NewReader(blobData)), nil)

	src := &RegistryBlobSource{Client: cli, Repository: "library/repo", Reference: "latest"}
	got, err := src.Blob(context.Background(), "sha256:def")
	require.NoError(t, err)
	require.Equal(t, blobData, got)
}

func TestRegistryBlobSource_BlobPayloadErrorFallback(t *testing.T) {
	blobData := []byte(`fallback blob`)
	cli := &registrytesting.Client{}
	// PullManifest succeeds but Payload() fails — should fall through to PullBlob.
	cli.On("PullManifest", "library/repo", "sha256:ghi").
		Return(&stubManifest{payErr: errors.New("payload error")}, "sha256:ghi", nil)
	cli.On("PullBlob", "library/repo", "sha256:ghi").
		Return(int64(len(blobData)), io.NopCloser(bytes.NewReader(blobData)), nil)

	src := &RegistryBlobSource{Client: cli, Repository: "library/repo", Reference: "latest"}
	got, err := src.Blob(context.Background(), "sha256:ghi")
	require.NoError(t, err)
	require.Equal(t, blobData, got)
}

func TestRegistryBlobSource_BlobBothFail(t *testing.T) {
	cli := &registrytesting.Client{}
	cli.On("PullManifest", "library/repo", "sha256:jkl").Return(nil, "", errors.New("not found"))
	cli.On("PullBlob", "library/repo", "sha256:jkl").Return(int64(0), nil, errors.New("blob not found"))

	src := &RegistryBlobSource{Client: cli, Repository: "library/repo", Reference: "latest"}
	_, err := src.Blob(context.Background(), "sha256:jkl")
	require.Error(t, err)
	require.Contains(t, err.Error(), "pull blob")
}
