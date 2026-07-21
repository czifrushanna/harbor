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
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// generatedDockerfileHeader is prepended to every reconstructed Dockerfile so
// consumers (LLM optimizer, portal, reviewers) can immediately tell it apart
// from a Dockerfile extracted verbatim from BuildKit provenance.
const generatedDockerfileHeader = `# Reconstructed from the image's config history because no BuildKit provenance
# attestation was found on this artifact. This is a best-effort approximation of
# the original build source, not the original Dockerfile: instruction ordering
# and RUN commands are recovered from the image config, but ADD/COPY sources
# cannot be recovered exactly. Review before relying on it.
`

// shellExecPrefixes strips the shell wrapper Docker/BuildKit put around every
// RUN instruction's created_by string before it is turned back into a plain
// "RUN <command>" line.
var shellExecPrefixes = []string{
	"/bin/sh -c ",
	"/bin/bash -c ",
	"cmd /S /C ",
	"powershell -Command ",
}

// addCopyPattern matches the synthetic "#(nop) ADD/COPY <kind>:<digest> in <dest>"
// form Docker writes into history for ADD/COPY instructions. The original
// source path isn't preserved anywhere in image config, only a digest of the
// content that got added, so it can't be recovered exactly.
var addCopyPattern = regexp.MustCompile(`(?s)^(ADD|COPY)\s+(?:file|dir|multi):([0-9a-fA-F]+)\s+in\s+(.+?)\s*$`)

type ociContainerConfig struct {
	Env          []string          `json:"Env"`
	Entrypoint   []string          `json:"Entrypoint"`
	Cmd          []string          `json:"Cmd"`
	WorkingDir   string            `json:"WorkingDir"`
	User         string            `json:"User"`
	ExposedPorts map[string]any    `json:"ExposedPorts"`
	Volumes      map[string]any    `json:"Volumes"`
	Labels       map[string]string `json:"Labels"`
	StopSignal   string            `json:"StopSignal"`
}

type ociHistoryEntry struct {
	CreatedBy  string `json:"created_by"`
	EmptyLayer bool   `json:"empty_layer"`
}

type ociImageConfig struct {
	Config  ociContainerConfig `json:"config"`
	History []ociHistoryEntry  `json:"history"`
}

// manifestProbe is used both for the top-level index/manifest and for a
// resolved image manifest: it recognizes a plain image manifest via the
// "config" digest field, and a manifest list via "manifests".
type manifestProbe struct {
	Config *struct {
		Digest string `json:"digest"`
	} `json:"config"`
	Manifests []struct {
		Digest      string            `json:"digest"`
		Annotations map[string]string `json:"annotations"`
	} `json:"manifests"`
}

// GenerateDockerfileFromSource reconstructs a best-effort Dockerfile from the
// image config's `history` metadata, which every OCI/Docker image carries
// regardless of how it was built. It is the fallback used when
// ExtractDockerfileFromSource cannot find a BuildKit provenance attestation
// (e.g. images built with the classic builder, or pulled/re-pushed without
// provenance): unlike the provenance path, this never recovers the exact
// original Dockerfile, only an approximation good enough for the LLM
// optimizer and human review.
func GenerateDockerfileFromSource(ctx context.Context, src BlobSource) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	configDigest, err := resolveImageConfigDigest(ctx, src)
	if err != nil {
		return nil, err
	}

	configBytes, err := src.Blob(ctx, configDigest)
	if err != nil {
		return nil, fmt.Errorf("pull image config %s: %w", configDigest, err)
	}

	var cfg ociImageConfig
	if err := json.Unmarshal(configBytes, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal image config %s: %w", configDigest, err)
	}

	if len(cfg.History) == 0 {
		return nil, fmt.Errorf("image config %s has no history metadata to reconstruct a Dockerfile from", configDigest)
	}

	return &Result{
		Dockerfile: renderGeneratedDockerfile(&cfg),
		Generated:  true,
	}, nil
}

// resolveImageConfigDigest finds the digest of the image config blob starting
// from the same top-level reference ExtractDockerfileFromSource looks at. It
// handles both a direct image manifest (config digest at the top level) and a
// manifest list (walking non-attestation entries looking for one).
func resolveImageConfigDigest(ctx context.Context, src BlobSource) (string, error) {
	top, err := src.Index(ctx)
	if err != nil {
		return "", err
	}

	var probe manifestProbe
	if err := json.Unmarshal(top, &probe); err != nil {
		return "", fmt.Errorf("unmarshal manifest: %w", err)
	}

	if probe.Config != nil && probe.Config.Digest != "" {
		return probe.Config.Digest, nil
	}

	for _, m := range probe.Manifests {
		if m.Annotations["vnd.docker.reference.type"] == attestationManifestType {
			continue
		}

		blob, err := src.Blob(ctx, m.Digest)
		if err != nil {
			continue
		}

		var nested manifestProbe
		if err := json.Unmarshal(blob, &nested); err != nil {
			continue
		}
		if nested.Config != nil && nested.Config.Digest != "" {
			return nested.Config.Digest, nil
		}
	}

	return "", fmt.Errorf("no image manifest found to generate a Dockerfile from")
}

func renderGeneratedDockerfile(cfg *ociImageConfig) string {
	var b strings.Builder
	b.WriteString(generatedDockerfileHeader)
	b.WriteString("\nFROM scratch\n")

	seen := map[string]bool{}
	for _, h := range cfg.History {
		line := renderHistoryLine(h.CreatedBy)
		if line == "" {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
		seen[firstToken(line)] = true
	}

	for _, line := range trailingConfigLines(&cfg.Config, seen) {
		b.WriteString(line)
		b.WriteString("\n")
	}

	return b.String()
}

// renderHistoryLine turns one image config history entry's created_by string
// into a Dockerfile line: metadata instructions (marked with the "#(nop)"
// convention) are passed through close to verbatim, everything else is a real
// shell command that produced a layer and becomes a RUN line.
func renderHistoryLine(createdBy string) string {
	raw := strings.TrimSpace(createdBy)
	if raw == "" {
		return ""
	}

	for _, prefix := range shellExecPrefixes {
		if strings.HasPrefix(raw, prefix) {
			raw = strings.TrimSpace(strings.TrimPrefix(raw, prefix))
			break
		}
	}

	if strings.HasPrefix(raw, "#(nop)") {
		instr := strings.TrimSpace(strings.TrimPrefix(raw, "#(nop)"))
		if instr == "" {
			return ""
		}
		return rewriteAddCopyPlaceholder(instr)
	}

	return "RUN " + raw
}

func rewriteAddCopyPlaceholder(instr string) string {
	if m := addCopyPattern.FindStringSubmatch(instr); m != nil {
		keyword, digest, dest := m[1], m[2], m[3]
		return fmt.Sprintf("%s <unresolvable-source sha256:%s> %s", keyword, digest, dest)
	}
	return instr
}

func firstToken(line string) string {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToUpper(fields[0])
}

// trailingConfigLines is a safety net for images whose history was pruned or
// never carried certain metadata (e.g. re-committed images): it emits, from
// the final config state, only the instruction kinds that history didn't
// already contribute.
func trailingConfigLines(c *ociContainerConfig, seen map[string]bool) []string {
	var lines []string

	if !seen["ENV"] {
		for _, kv := range c.Env {
			lines = append(lines, "ENV "+kv)
		}
	}

	if !seen["LABEL"] && len(c.Labels) > 0 {
		for _, k := range sortedKeys(c.Labels) {
			lines = append(lines, fmt.Sprintf("LABEL %s=%q", k, c.Labels[k]))
		}
	}

	if !seen["EXPOSE"] && len(c.ExposedPorts) > 0 {
		for _, p := range sortedKeys(c.ExposedPorts) {
			lines = append(lines, "EXPOSE "+strings.SplitN(p, "/", 2)[0])
		}
	}

	if !seen["VOLUME"] && len(c.Volumes) > 0 {
		for _, v := range sortedKeys(c.Volumes) {
			lines = append(lines, fmt.Sprintf("VOLUME [%q]", v))
		}
	}

	if !seen["WORKDIR"] && c.WorkingDir != "" {
		lines = append(lines, "WORKDIR "+c.WorkingDir)
	}

	if !seen["USER"] && c.User != "" {
		lines = append(lines, "USER "+c.User)
	}

	if !seen["STOPSIGNAL"] && c.StopSignal != "" {
		lines = append(lines, "STOPSIGNAL "+c.StopSignal)
	}

	if !seen["ENTRYPOINT"] && len(c.Entrypoint) > 0 {
		lines = append(lines, "ENTRYPOINT "+jsonArray(c.Entrypoint))
	}

	if !seen["CMD"] && len(c.Cmd) > 0 {
		lines = append(lines, "CMD "+jsonArray(c.Cmd))
	}

	return lines
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func jsonArray(items []string) string {
	b, _ := json.Marshal(items)
	return string(b)
}
