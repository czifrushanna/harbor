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
	"fmt"
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

// fakeBlobSource implements BlobSource from an in-memory map for testing.
type fakeBlobSource struct {
	index []byte
	blobs map[string][]byte
}

func (f *fakeBlobSource) Index(_ context.Context) ([]byte, error) { return f.index, nil }
func (f *fakeBlobSource) Blob(_ context.Context, digest string) ([]byte, error) {
	b, ok := f.blobs[digest]
	if !ok {
		return nil, fmt.Errorf("blob %s not found", digest)
	}
	return b, nil
}

func TestExtractDockerfileFromSource(t *testing.T) {
	const expected = "FROM alpine:3.20\nRUN echo hello\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(expected))

	indexJSON, _ := json.Marshal(map[string]any{
		"manifests": []map[string]any{
			{
				"digest": "sha256:attestation",
				"annotations": map[string]string{
					"vnd.docker.reference.type": attestationManifestType,
				},
			},
		},
	})
	attestationJSON, _ := json.Marshal(map[string]any{
		"layers": []map[string]string{{"digest": "sha256:statement"}},
	})
	statementJSON, _ := json.Marshal(map[string]any{
		"predicate": map[string]any{
			"runDetails": map[string]any{
				"metadata": map[string]any{
					"buildkit_metadata": map[string]any{
						"source": map[string]any{
							"infos": []map[string]string{{"data": encoded}},
						},
					},
				},
			},
		},
	})

	src := &fakeBlobSource{
		index: indexJSON,
		blobs: map[string][]byte{
			"sha256:attestation": attestationJSON,
			"sha256:statement":   statementJSON,
		},
	}

	result, err := NewWorkflow().ExtractDockerfileFromSource(context.Background(), src)
	require.NoError(t, err)
	require.Equal(t, expected, result.Dockerfile)
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

func TestExtractDockerfileFromReaderDirect(t *testing.T) {
	archivePath := createOCIArchive(t, "FROM golang:1.23\nRUN go build .\n")

	f, err := os.Open(archivePath)
	require.NoError(t, err)
	defer f.Close()

	result, err := NewWorkflow().ExtractDockerfileFromReader(context.Background(), f)
	require.NoError(t, err)
	require.Equal(t, "FROM golang:1.23\nRUN go build .\n", result.Dockerfile)
}

func TestExtractDockerfileContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	archivePath := createOCIArchive(t, "FROM scratch\n")

	f, err := os.Open(archivePath)
	require.NoError(t, err)
	defer f.Close()

	_, err = NewWorkflow().ExtractDockerfileFromReader(ctx, f)
	require.ErrorIs(t, err, context.Canceled)
}

func TestAttestationManifestNoLayers(t *testing.T) {
	indexJSON, _ := json.Marshal(map[string]any{
		"manifests": []map[string]any{
			{
				"digest": "sha256:nolay",
				"annotations": map[string]string{
					"vnd.docker.reference.type": attestationManifestType,
				},
			},
		},
	})
	// Attestation manifest with an empty layers array.
	attestationJSON, _ := json.Marshal(map[string]any{"layers": []any{}})

	src := &fakeBlobSource{
		index: indexJSON,
		blobs: map[string][]byte{
			"sha256:nolay": attestationJSON,
		},
	}

	_, err := NewWorkflow().ExtractDockerfileFromSource(context.Background(), src)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no layers")
}

func TestNestedAttestationManifest(t *testing.T) {
	const expected = "FROM ubuntu:24.04\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(expected))

	// Top-level index points to a nested manifest list (not directly annotated).
	indexJSON, _ := json.Marshal(map[string]any{
		"manifests": []map[string]any{
			{"digest": "sha256:outer"},
		},
	})
	// The nested manifest list contains the real attestation manifest.
	nestedIndexJSON, _ := json.Marshal(map[string]any{
		"manifests": []map[string]any{
			{
				"digest": "sha256:attest",
				"annotations": map[string]string{
					"vnd.docker.reference.type": attestationManifestType,
				},
			},
		},
	})
	attestationJSON, _ := json.Marshal(map[string]any{
		"layers": []map[string]string{{"digest": "sha256:stmt"}},
	})
	statementJSON, _ := json.Marshal(map[string]any{
		"predicate": map[string]any{
			"runDetails": map[string]any{
				"metadata": map[string]any{
					"buildkit_metadata": map[string]any{
						"source": map[string]any{
							"infos": []map[string]string{{"data": encoded}},
						},
					},
				},
			},
		},
	})

	src := &fakeBlobSource{
		index: indexJSON,
		blobs: map[string][]byte{
			"sha256:outer":  nestedIndexJSON,
			"sha256:attest": attestationJSON,
			"sha256:stmt":   statementJSON,
		},
	}

	result, err := NewWorkflow().ExtractDockerfileFromSource(context.Background(), src)
	require.NoError(t, err)
	require.Equal(t, expected, result.Dockerfile)
	require.Equal(t, "sha256:attest", result.AttestationManifestDigest)
}

func TestDecodeDockerfileRawBase64(t *testing.T) {
	// Raw base64 omits padding; StdEncoding rejects it, RawStdEncoding accepts it.
	rawEncoded := base64.RawStdEncoding.EncodeToString([]byte("FROM scratch\n"))

	indexJSON, _ := json.Marshal(map[string]any{
		"manifests": []map[string]any{
			{
				"digest": "sha256:a1",
				"annotations": map[string]string{
					"vnd.docker.reference.type": attestationManifestType,
				},
			},
		},
	})
	attestationJSON, _ := json.Marshal(map[string]any{
		"layers": []map[string]string{{"digest": "sha256:s1"}},
	})
	statementJSON, _ := json.Marshal(map[string]any{
		"predicate": map[string]any{
			"runDetails": map[string]any{
				"metadata": map[string]any{
					"buildkit_metadata": map[string]any{
						"source": map[string]any{
							"infos": []map[string]string{{"data": rawEncoded}},
						},
					},
				},
			},
		},
	})

	src := &fakeBlobSource{
		index: indexJSON,
		blobs: map[string][]byte{
			"sha256:a1": attestationJSON,
			"sha256:s1": statementJSON,
		},
	}

	result, err := NewWorkflow().ExtractDockerfileFromSource(context.Background(), src)
	require.NoError(t, err)
	require.Equal(t, "FROM scratch\n", result.Dockerfile)
}

func TestExtractDockerfileBlobNotInArchive(t *testing.T) {
	indexJSON, _ := json.Marshal(map[string]any{
		"manifests": []map[string]any{
			{
				"digest": "sha256:missing",
				"annotations": map[string]string{
					"vnd.docker.reference.type": attestationManifestType,
				},
			},
		},
	})

	src := &fakeBlobSource{
		index: indexJSON,
		blobs: map[string][]byte{},
	}

	_, err := NewWorkflow().ExtractDockerfileFromSource(context.Background(), src)
	require.Error(t, err)
}