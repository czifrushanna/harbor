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
	"archive/tar"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeTarEntry(t *testing.T, tw *tar.Writer, name string, content []byte) {
	t.Helper()

	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name: name,
		Mode: 0600,
		Size: int64(len(content)),
	}))
	_, err := tw.Write(content)
	require.NoError(t, err)
}

func createOCIArchive(t *testing.T, dockerfile string) string {
	t.Helper()

	encodedDockerfile := base64.StdEncoding.EncodeToString([]byte(dockerfile))
	indexJSON := map[string]any{
		"manifests": []map[string]any{
			{
				"digest": "sha256:attestation",
				"annotations": map[string]string{
					"vnd.docker.reference.type": attestationManifestType,
				},
			},
		},
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

	file, err := os.CreateTemp(t.TempDir(), "image-*.oci")
	require.NoError(t, err)
	defer file.Close()

	tw := tar.NewWriter(file)
	writeTarEntry(t, tw, "index.json", indexContent)
	writeTarEntry(t, tw, filepath.Join("blobs", "sha256", "attestation"), attestationContent)
	writeTarEntry(t, tw, filepath.Join("blobs", "sha256", "statement"), statementContent)
	require.NoError(t, tw.Close())
	require.NoError(t, file.Close())

	return file.Name()
}

func TestExtractDockerfile(t *testing.T) {
	archivePath := createOCIArchive(t, "FROM alpine:3.20\nRUN echo hello\n")

	result, err := NewWorkflow().ExtractDockerfile(context.Background(), archivePath)
	require.NoError(t, err)
	require.Equal(t, "FROM alpine:3.20\nRUN echo hello\n", result.Dockerfile)
	require.Equal(t, "sha256:attestation", result.AttestationManifestDigest)
	require.Equal(t, "sha256:statement", result.StatementDigest)
}

func TestExtractDockerfileMissingAttestation(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "image-*.oci")
	require.NoError(t, err)
	defer file.Close()

	tw := tar.NewWriter(file)
	writeTarEntry(t, tw, "index.json", []byte(`{"manifests":[]}`))
	require.NoError(t, tw.Close())
	require.NoError(t, file.Close())

	_, err = NewWorkflow().ExtractDockerfile(context.Background(), file.Name())
	require.Error(t, err)
	require.Contains(t, err.Error(), "attestation manifest not found")
}