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
	"fmt"
	"io"

	"github.com/goharbor/harbor/src/pkg/registry"
)

// RegistryBlobSource implements BlobSource by pulling data directly from a
// Harbor registry. Index() fetches the top-level manifest for the given
// reference; Blob() tries PullManifest first (for nested manifests) and falls
// back to PullBlob (for statement layers and other data blobs).
type RegistryBlobSource struct {
	Client     registry.Client
	Repository string
	Reference  string
}

func (r *RegistryBlobSource) Index(_ context.Context) ([]byte, error) {
	m, _, err := r.Client.PullManifest(r.Repository, r.Reference)
	if err != nil {
		return nil, fmt.Errorf("pull index manifest %s@%s: %w", r.Repository, r.Reference, err)
	}
	_, payload, err := m.Payload()
	if err != nil {
		return nil, fmt.Errorf("serialize index manifest: %w", err)
	}
	return payload, nil
}

func (r *RegistryBlobSource) Blob(_ context.Context, digest string) ([]byte, error) {
	m, _, err := r.Client.PullManifest(r.Repository, digest)
	if err == nil {
		_, payload, pErr := m.Payload()
		if pErr == nil {
			return payload, nil
		}
	}

	_, blob, err := r.Client.PullBlob(r.Repository, digest)
	if err != nil {
		return nil, fmt.Errorf("pull blob %s from %s: %w", digest, r.Repository, err)
	}
	defer blob.Close()

	data, err := io.ReadAll(blob)
	if err != nil {
		return nil, fmt.Errorf("read blob %s: %w", digest, err)
	}
	return data, nil
}
