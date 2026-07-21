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
	"io"
	"os"
	"path"
	"strings"
)

const (
	attestationManifestType = "attestation-manifest"
	indexFileName           = "index.json"
	blobsPrefix             = "blobs/sha256/"
)

// Result contains the extracted Dockerfile and the OCI digests that led to it.
type Result struct {
	Dockerfile                string
	AttestationManifestDigest string
	StatementDigest           string
	// Generated is true when Dockerfile was reconstructed from image config
	// history (see generate.go) rather than extracted verbatim from a BuildKit
	// provenance attestation.
	Generated bool
}

// BlobSource abstracts how OCI index/manifest/blob bytes are fetched, allowing
// the same extraction logic to work with both a local OCI tar archive and a
// live registry.
type BlobSource interface {
	// Index returns the top-level OCI index (manifest list) JSON.
	Index(ctx context.Context) ([]byte, error)
	// Blob returns the raw bytes for a given digest (manifest or data blob).
	Blob(ctx context.Context, digest string) ([]byte, error)
}

// Workflow defines the Dockerfile extraction workflow.
type Workflow interface {
	ExtractDockerfile(ctx context.Context, ociArchivePath string) (*Result, error)
	ExtractDockerfileFromReader(ctx context.Context, archive io.Reader) (*Result, error)
	ExtractDockerfileFromSource(ctx context.Context, src BlobSource) (*Result, error)
}

// NewWorkflow returns the default Dockerfile extraction workflow.
func NewWorkflow() Workflow {
	return &workflow{}
}

type workflow struct{}

type archiveEntries map[string][]byte

type index struct {
	Manifests []struct {
		Digest      string            `json:"digest"`
		Annotations map[string]string `json:"annotations"`
	} `json:"manifests"`
}

type attestationManifest struct {
	Layers []struct {
		Digest string `json:"digest"`
	} `json:"layers"`
}

type buildKitStatement struct {
	Predicate struct {
		RunDetails struct {
			Metadata struct {
				BuildkitMetadata struct {
					Source struct {
						Infos []struct {
							Data string `json:"data"`
						} `json:"infos"`
					} `json:"source"`
				} `json:"buildkit_metadata"`
			} `json:"metadata"`
		} `json:"runDetails"`
	} `json:"predicate"`
}

func (w *workflow) ExtractDockerfile(ctx context.Context, ociArchivePath string) (*Result, error) {
	file, err := os.Open(ociArchivePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return w.ExtractDockerfileFromReader(ctx, file)
}

func (w *workflow) ExtractDockerfileFromReader(ctx context.Context, archive io.Reader) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	entries, err := readArchiveEntries(archive)
	if err != nil {
		return nil, err
	}

	return w.ExtractDockerfileFromSource(ctx, &tarBlobSource{entries: entries})
}

// ExtractDockerfileFromSource extracts a Dockerfile from any BlobSource (tar archive or registry).
func (w *workflow) ExtractDockerfileFromSource(ctx context.Context, src BlobSource) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	indexBytes, err := src.Index(ctx)
	if err != nil {
		return nil, err
	}

	var idx index
	if err := json.Unmarshal(indexBytes, &idx); err != nil {
		return nil, fmt.Errorf("unmarshal index: %w", err)
	}

	attestationDigest, err := findAttestationManifestDigestFromSource(ctx, &idx, src, 5)
	if err != nil {
		return nil, err
	}

	attestationManifestBytes, err := src.Blob(ctx, attestationDigest)
	if err != nil {
		return nil, err
	}

	manifest, err := readAttestationManifest(attestationManifestBytes)
	if err != nil {
		return nil, err
	}

	if len(manifest.Layers) == 0 {
		return nil, fmt.Errorf("attestation manifest %s has no layers", attestationDigest)
	}

	statementDigest := manifest.Layers[0].Digest
	statementBytes, err := src.Blob(ctx, statementDigest)
	if err != nil {
		return nil, err
	}

	dockerfile, err := decodeDockerfile(statementBytes)
	if err != nil {
		return nil, err
	}

	return &Result{
		Dockerfile:                dockerfile,
		AttestationManifestDigest: attestationDigest,
		StatementDigest:           statementDigest,
	}, nil
}

// tarBlobSource implements BlobSource on top of an in-memory tar archive map.
type tarBlobSource struct {
	entries archiveEntries
}

func (t *tarBlobSource) Index(_ context.Context) ([]byte, error) {
	content, ok := t.entries[indexFileName]
	if !ok {
		return nil, fmt.Errorf("%s not found in OCI archive", indexFileName)
	}
	return content, nil
}

func (t *tarBlobSource) Blob(_ context.Context, digest string) ([]byte, error) {
	return readBlob(t.entries, digest)
}

func readArchiveEntries(archive io.Reader) (archiveEntries, error) {
	entries := archiveEntries{}
	reader := tar.NewReader(archive)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read OCI archive: %w", err)
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}

		content, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("read OCI archive entry %s: %w", header.Name, err)
		}
		entries[path.Clean(header.Name)] = content
	}

	return entries, nil
}

func findAttestationManifestDigestFromSource(ctx context.Context, idx *index, src BlobSource, depth int) (string, error) {
	if depth <= 0 {
		return "", fmt.Errorf("attestation manifest not found (max recursion reached)")
	}

	for _, manifest := range idx.Manifests {
		if manifest.Annotations["vnd.docker.reference.type"] == attestationManifestType {
			return manifest.Digest, nil
		}
	}

	// Not found at this level; inspect referenced manifest blobs which might be manifest lists.
	for _, manifest := range idx.Manifests {
		content, err := src.Blob(ctx, manifest.Digest)
		if err != nil {
			continue
		}
		var nested index
		if err := json.Unmarshal(content, &nested); err != nil {
			continue
		}
		if len(nested.Manifests) == 0 {
			continue
		}
		if d, err := findAttestationManifestDigestFromSource(ctx, &nested, src, depth-1); err == nil {
			return d, nil
		}
	}

	return "", fmt.Errorf("attestation manifest not found in OCI index")
}

func readAttestationManifest(content []byte) (*attestationManifest, error) {
	var manifest attestationManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return nil, fmt.Errorf("unmarshal attestation manifest: %w", err)
	}
	return &manifest, nil
}

func readBlob(entries archiveEntries, digest string) ([]byte, error) {
	blobPath, err := digestToBlobPath(digest)
	if err != nil {
		return nil, err
	}

	content, ok := entries[blobPath]
	if !ok {
		return nil, fmt.Errorf("blob %s not found in OCI archive", digest)
	}

	return content, nil
}

func digestToBlobPath(digest string) (string, error) {
	parts := strings.Split(digest, ":")
	if len(parts) != 2 || parts[0] != "sha256" || parts[1] == "" {
		return "", fmt.Errorf("unsupported digest format: %s", digest)
	}

	return blobsPrefix + parts[1], nil
}

func decodeDockerfile(statement []byte) (string, error) {
	var payload buildKitStatement
	if err := json.Unmarshal(statement, &payload); err != nil {
		return "", fmt.Errorf("unmarshal in-toto statement: %w", err)
	}

	infos := payload.Predicate.RunDetails.Metadata.BuildkitMetadata.Source.Infos
	if len(infos) == 0 {
		return "", fmt.Errorf("buildkit source info not found in statement")
	}

	rawDockerfile := strings.TrimSpace(infos[0].Data)
	decoded, err := base64.StdEncoding.DecodeString(rawDockerfile)
	if err == nil {
		return string(decoded), nil
	}

	decoded, rawErr := base64.RawStdEncoding.DecodeString(rawDockerfile)
	if rawErr != nil {
		return "", fmt.Errorf("decode embedded Dockerfile: %w", err)
	}

	return string(decoded), nil
}
