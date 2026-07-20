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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOptimizeDockerfile_StreamingDelta(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"FROM "}}]}`)
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"alpine:3.21"}}]}`)
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "data: [DONE]")
	}))
	defer ts.Close()

	result, err := OptimizeDockerfile(context.Background(), ts.URL, "test-key", "test-model", "FROM scratch\n")
	require.NoError(t, err)
	require.Equal(t, "FROM alpine:3.21", result)
}

func TestOptimizeDockerfile_NonStreamingMessage(t *testing.T) {
	// Some gateway implementations return message.content instead of delta.content.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `data: {"choices":[{"message":{"content":"FROM debian:12"}}]}`)
		fmt.Fprintln(w, "data: [DONE]")
	}))
	defer ts.Close()

	result, err := OptimizeDockerfile(context.Background(), ts.URL, "key", "model", "FROM scratch\n")
	require.NoError(t, err)
	require.Equal(t, "FROM debian:12", result)
}

func TestOptimizeDockerfile_Non2xxStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "internal error from gateway")
	}))
	defer ts.Close()

	_, err := OptimizeDockerfile(context.Background(), ts.URL, "key", "model", "FROM scratch\n")
	require.Error(t, err)
	require.Contains(t, err.Error(), "500")
	require.Contains(t, err.Error(), "internal error from gateway")
}

func TestOptimizeDockerfile_EmptyContent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":""}}]}`)
		fmt.Fprintln(w, "data: [DONE]")
	}))
	defer ts.Close()

	_, err := OptimizeDockerfile(context.Background(), ts.URL, "key", "model", "FROM scratch\n")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no optimized content")
}

func TestOptimizeDockerfile_MalformedSSELinesIgnored(t *testing.T) {
	// Lines that are not prefixed with "data:" or are blank should be skipped.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, ": keep-alive")
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "event: message")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"FROM scratch"}}]}`)
		fmt.Fprintln(w, "data: [DONE]")
	}))
	defer ts.Close()

	result, err := OptimizeDockerfile(context.Background(), ts.URL, "key", "model", "FROM scratch\n")
	require.NoError(t, err)
	require.Equal(t, "FROM scratch", result)
}
