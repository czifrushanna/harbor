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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateDockerfileFromSource_DirectManifest(t *testing.T) {
	src := &fakeBlobSource{
		index: []byte(`{"config":{"digest":"sha256:cfg"},"layers":[{"digest":"sha256:layer1"}]}`),
		blobs: map[string][]byte{
			"sha256:cfg": []byte(`{
				"config": {"Env": ["PATH=/usr/local/bin"], "Cmd": ["/bin/sh"]},
				"history": [
					{"created_by": "/bin/sh -c #(nop)  ENV PATH=/usr/local/bin", "empty_layer": true},
					{"created_by": "/bin/sh -c apk add --no-cache curl", "empty_layer": false},
					{"created_by": "/bin/sh -c #(nop) WORKDIR /app", "empty_layer": true},
					{"created_by": "/bin/sh -c #(nop) COPY file:abc123 in /app/hello.sh ", "empty_layer": false},
					{"created_by": "/bin/sh -c #(nop)  CMD [\"/bin/sh\"]", "empty_layer": true}
				]
			}`),
		},
	}

	result, err := GenerateDockerfileFromSource(context.Background(), src)
	require.NoError(t, err)
	require.True(t, result.Generated)
	require.Contains(t, result.Dockerfile, "FROM scratch")
	require.Contains(t, result.Dockerfile, "ENV PATH=/usr/local/bin")
	require.Contains(t, result.Dockerfile, "RUN apk add --no-cache curl")
	require.Contains(t, result.Dockerfile, "WORKDIR /app")
	require.Contains(t, result.Dockerfile, `COPY <unresolvable-source sha256:abc123> /app/hello.sh`)
	require.Contains(t, result.Dockerfile, `CMD ["/bin/sh"]`)
	require.Empty(t, result.AttestationManifestDigest)
	require.Empty(t, result.StatementDigest)
}

func TestGenerateDockerfileFromSource_ManifestList(t *testing.T) {
	src := &fakeBlobSource{
		index: []byte(`{"manifests":[
			{"digest":"sha256:attestation","annotations":{"vnd.docker.reference.type":"attestation-manifest"}},
			{"digest":"sha256:platform-manifest"}
		]}`),
		blobs: map[string][]byte{
			"sha256:attestation": []byte(`{"layers":[{"digest":"sha256:statement"}]}`),
			"sha256:platform-manifest": []byte(
				`{"config":{"digest":"sha256:cfg"},"layers":[{"digest":"sha256:layer1"}]}`,
			),
			"sha256:cfg": []byte(`{
				"config": {},
				"history": [
					{"created_by": "/bin/sh -c echo hi", "empty_layer": false}
				]
			}`),
		},
	}

	result, err := GenerateDockerfileFromSource(context.Background(), src)
	require.NoError(t, err)
	require.True(t, result.Generated)
	require.Contains(t, result.Dockerfile, "RUN echo hi")
}

func TestGenerateDockerfileFromSource_NoManifestFound(t *testing.T) {
	src := &fakeBlobSource{index: []byte(`{"manifests":[]}`)}

	_, err := GenerateDockerfileFromSource(context.Background(), src)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no image manifest found")
}

func TestGenerateDockerfileFromSource_NoHistory(t *testing.T) {
	src := &fakeBlobSource{
		index: []byte(`{"config":{"digest":"sha256:cfg"}}`),
		blobs: map[string][]byte{
			"sha256:cfg": []byte(`{"config": {}, "history": []}`),
		},
	}

	_, err := GenerateDockerfileFromSource(context.Background(), src)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no history metadata")
}

func TestGenerateDockerfileFromSource_TrailingConfigFallback(t *testing.T) {
	// History carries no metadata instructions at all (e.g. a re-committed
	// image); the final config state must still surface as Dockerfile lines.
	src := &fakeBlobSource{
		index: []byte(`{"config":{"digest":"sha256:cfg"}}`),
		blobs: map[string][]byte{
			"sha256:cfg": []byte(`{
				"config": {
					"Env": ["FOO=bar"],
					"WorkingDir": "/srv",
					"User": "nobody",
					"Labels": {"team": "sre"},
					"ExposedPorts": {"8080/tcp": {}},
					"Cmd": ["/entrypoint.sh"]
				},
				"history": [
					{"created_by": "/bin/sh -c make build", "empty_layer": false}
				]
			}`),
		},
	}

	result, err := GenerateDockerfileFromSource(context.Background(), src)
	require.NoError(t, err)
	require.Contains(t, result.Dockerfile, "RUN make build")
	require.Contains(t, result.Dockerfile, "ENV FOO=bar")
	require.Contains(t, result.Dockerfile, "WORKDIR /srv")
	require.Contains(t, result.Dockerfile, "USER nobody")
	require.Contains(t, result.Dockerfile, `LABEL team="sre"`)
	require.Contains(t, result.Dockerfile, "EXPOSE 8080")
	require.Contains(t, result.Dockerfile, `CMD ["/entrypoint.sh"]`)
}

func TestRenderHistoryLine(t *testing.T) {
	cases := []struct {
		name      string
		createdBy string
		want      string
	}{
		{"plain RUN", "/bin/sh -c echo hi", "RUN echo hi"},
		{"nop metadata passthrough", "/bin/sh -c #(nop) WORKDIR /app", "WORKDIR /app"},
		{"nop with double space", "/bin/sh -c #(nop)  CMD [\"/bin/sh\"]", `CMD ["/bin/sh"]`},
		{"empty created_by", "", ""},
		{
			"ADD file placeholder",
			"/bin/sh -c #(nop) ADD file:9d48c3bd43c520 in /app/hello.sh ",
			"ADD <unresolvable-source sha256:9d48c3bd43c520> /app/hello.sh",
		},
		{
			"no shell prefix at all",
			"echo raw",
			"RUN echo raw",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, renderHistoryLine(tc.createdBy))
		})
	}
}
