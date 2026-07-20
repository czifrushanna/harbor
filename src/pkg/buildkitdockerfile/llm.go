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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	DefaultLLMAPIBaseURL = "https://llmgw-litellm.web.cern.ch/v1/chat/completions"
	DefaultLLMModel      = "llama-3.1-8b-instruct"
	DefaultLLMAPIKeyEnv  = "LLMGW_API_KEY"
)

// OptimizeWithEnvConfig resolves LLM credentials from environment variables and
// calls OptimizeDockerfile. Returns an error if the API key is not configured.
func OptimizeWithEnvConfig(ctx context.Context, dockerfile string) (string, error) {
	apiKeyEnv := os.Getenv("LLMGW_API_KEY_ENV")
	if apiKeyEnv == "" {
		apiKeyEnv = DefaultLLMAPIKeyEnv
	}
	apiKey := os.Getenv(apiKeyEnv)
	if apiKey == "" {
		return "", fmt.Errorf("LLM optimization is not configured")
	}

	apiBaseURL := os.Getenv("LLMGW_API_BASE_URL")
	if apiBaseURL == "" {
		apiBaseURL = DefaultLLMAPIBaseURL
	}
	model := os.Getenv("LLMGW_MODEL")
	if model == "" {
		model = DefaultLLMModel
	}

	return OptimizeDockerfile(ctx, apiBaseURL, apiKey, model, dockerfile)
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
		Delta   chatMessage `json:"delta"`
	} `json:"choices"`
}

// OptimizeDockerfile sends the recovered Dockerfile to an LLM gateway and returns the optimized result.
func OptimizeDockerfile(ctx context.Context, apiBaseURL, apiKey, model, dockerfile string) (string, error) {
	prompt := "Optimize this Dockerfile for readability, maintainability, and build quality without changing its behavior unless explicitly helpful. Return only the improved Dockerfile, no commentary.\n\nDockerfile:\n" + dockerfile
	reqBody := chatRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "user", Content: prompt},
		},
		Stream: true,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBaseURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("llm gateway returned %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	var optimized strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}

		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}

		var parsed chatResponse
		if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
			continue
		}
		if len(parsed.Choices) == 0 {
			continue
		}

		choice := parsed.Choices[0]
		if choice.Delta.Content != "" {
			optimized.WriteString(choice.Delta.Content)
			continue
		}
		if choice.Message.Content != "" {
			optimized.WriteString(choice.Message.Content)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}

	result := strings.TrimSpace(optimized.String())
	if result == "" {
		return "", fmt.Errorf("llm gateway returned no optimized content")
	}

	return result, nil
}
