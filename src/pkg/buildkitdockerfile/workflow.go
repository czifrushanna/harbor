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
	Dockerfile               string
	AttestationManifestDigest string
	StatementDigest          string
}

// Workflow defines the Dockerfile extraction workflow.
type Workflow interface {
	ExtractDockerfile(ctx context.Context, ociArchivePath string) (*Result, error)
}

// NewWorkflow returns the default Dockerfile extraction workflow.
func NewWorkflow() Workflow {
	return &workflow{}
}

type workflow struct{}

type archiveEntries map[string][]byte

type index struct {
	Manifests []struct {
		Digest     string            `json:"digest"`
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	entries, err := readArchiveEntries(ociArchivePath)
	if err != nil {
		return nil, err
	}

	idx, err := readIndex(entries)
	if err != nil {
		return nil, err
	}

	attestationDigest, err := findAttestationManifestDigest(idx, entries, 5)
	if err != nil {
		return nil, err
	}

	attestationManifestBytes, err := readBlob(entries, attestationDigest)
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
	statementBytes, err := readBlob(entries, statementDigest)
	if err != nil {
		return nil, err
	}

	dockerfile, err := decodeDockerfile(statementBytes)
	if err != nil {
		return nil, err
	}

	return &Result{
		Dockerfile:               dockerfile,
		AttestationManifestDigest: attestationDigest,
		StatementDigest:          statementDigest,
	}, nil
}

func readArchiveEntries(ociArchivePath string) (archiveEntries, error) {
	file, err := os.Open(ociArchivePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	entries := archiveEntries{}
	reader := tar.NewReader(file)
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

func readIndex(entries archiveEntries) (*index, error) {
	content, ok := entries[indexFileName]
	if !ok {
		return nil, fmt.Errorf("%s not found in OCI archive", indexFileName)
	}

	var idx index
	if err := json.Unmarshal(content, &idx); err != nil {
		return nil, fmt.Errorf("unmarshal index.json: %w", err)
	}

	return &idx, nil
}

func findAttestationManifestDigest(idx *index, entries archiveEntries, depth int) (string, error) {
	if depth <= 0 {
		return "", fmt.Errorf("attestation manifest not found (max recursion reached)")
	}

	for _, manifest := range idx.Manifests {
		if manifest.Annotations["vnd.docker.reference.type"] == attestationManifestType {
			return manifest.Digest, nil
		}
	}

	// Not found in current index; try to inspect referenced manifest blobs which might be manifest lists
	for _, manifest := range idx.Manifests {
		// read the blob for this manifest and try to unmarshal as an index (manifest list)
		content, err := readBlob(entries, manifest.Digest)
		if err != nil {
			// skip if blob not present
			continue
		}
		var nested index
		if err := json.Unmarshal(content, &nested); err != nil {
			// not an index/manifest-list, skip
			continue
		}
		if len(nested.Manifests) == 0 {
			continue
		}
		if d, err := findAttestationManifestDigest(&nested, entries, depth-1); err == nil {
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