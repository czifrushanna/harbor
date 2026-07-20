package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

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

func optimizeDockerfile(ctx context.Context, apiBaseURL *string, apiKey, model, dockerfile string) (string, error) {
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, *apiBaseURL, bytes.NewReader(body))
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