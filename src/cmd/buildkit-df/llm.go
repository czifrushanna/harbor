package main

import (
	"context"

	"github.com/goharbor/harbor/src/pkg/buildkitdockerfile"
)

func optimizeDockerfile(ctx context.Context, apiBaseURL *string, apiKey, model, dockerfile string) (string, error) {
	return buildkitdockerfile.OptimizeDockerfile(ctx, *apiBaseURL, apiKey, model, dockerfile)
}
