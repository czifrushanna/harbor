package main

import (
    "context"
    "flag"
    "fmt"
    "os"
    "path/filepath"

    "github.com/goharbor/harbor/src/pkg/buildkitdockerfile"
)

func main() {
    input := flag.String("i", "", "Path to OCI archive (.oci) produced by buildx --output=type=oci,dest=...")
    recoveredOut := flag.String("o", "", "Optional path to write recovered Dockerfile (default: stdout)")
    optimizedOut := flag.String("optimized-out", "", "Optional path to write optimized Dockerfile after LLM pass")
    outDir := flag.String("out-dir", "", "Directory to write recovered/optimized Dockerfiles together")
    optimize := flag.Bool("optimize", false, "Send the recovered Dockerfile to the LLM gateway for optimization")
    apiBaseURL := flag.String("api-base", "https://llmgw-litellm.web.cern.ch/v1/chat/completions", "Chat completions endpoint")
    apiKeyEnv := flag.String("api-key-env", "LLMGW_API_KEY", "Environment variable containing the API key")
    model := flag.String("model", "llama-3.1-8b-instruct", "Model name to send to the LLM gateway")
    flag.Parse()

    if *input == "" {
        fmt.Fprintln(os.Stderr, "error: -i <oci-archive> is required")
        flag.Usage()
        os.Exit(2)
    }

    wf := buildkitdockerfile.NewWorkflow()
    res, err := wf.ExtractDockerfile(context.Background(), *input)
    if err != nil {
        fmt.Fprintf(os.Stderr, "failed to extract Dockerfile: %v\n", err)
        os.Exit(1)
    }

    // if outDir specified, derive recovered/optimized paths from it unless explicit paths given
    if *outDir != "" {
        if err := os.MkdirAll(*outDir, 0755); err != nil {
            fmt.Fprintf(os.Stderr, "failed to create out-dir %s: %v\n", *outDir, err)
            os.Exit(1)
        }
        if *recoveredOut == "" {
            *recoveredOut = filepath.Join(*outDir, "recovered.Dockerfile")
        }
        if *optimizedOut == "" {
            *optimizedOut = filepath.Join(*outDir, "optimized.Dockerfile")
        }
    }

    if *recoveredOut == "" {
        fmt.Print(res.Dockerfile)
    } else {
        if err := os.WriteFile(*recoveredOut, []byte(res.Dockerfile), 0644); err != nil {
            fmt.Fprintf(os.Stderr, "failed to write recovered Dockerfile: %v\n", err)
            os.Exit(1)
        }
        fmt.Fprintf(os.Stdout, "recovered Dockerfile written to %s\n", *recoveredOut)
    }

    if !*optimize {
        return
    }

    apiKey := os.Getenv(*apiKeyEnv)
    if apiKey == "" {
        fmt.Fprintf(os.Stderr, "error: API key environment variable %s is empty\n", *apiKeyEnv)
        os.Exit(1)
    }

    optimized, err := optimizeDockerfile(context.Background(), apiBaseURL, apiKey, *model, res.Dockerfile)
    if err != nil {
        fmt.Fprintf(os.Stderr, "failed to optimize Dockerfile: %v\n", err)
        os.Exit(1)
    }

    if *optimizedOut == "" {
        fmt.Print(optimized)
        return
    }

    if err := os.WriteFile(*optimizedOut, []byte(optimized), 0644); err != nil {
        fmt.Fprintf(os.Stderr, "failed to write optimized Dockerfile: %v\n", err)
        os.Exit(1)
    }

    fmt.Fprintf(os.Stdout, "optimized Dockerfile written to %s\n", *optimizedOut)
}

