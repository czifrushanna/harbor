# buildkit-df

Small CLI for extracting a BuildKit-embedded Dockerfile from an OCI archive, then optionally sending it to the CERN LLM gateway for optimization.

## Recover the Dockerfile

```bash
cd src
./buildkit-df -i /tmp/image_real.oci -o /tmp/recovered_real.Dockerfile
```

To write the recovered and optimized Dockerfiles into the same directory, use `-out-dir`:

```bash
cd src
./buildkit-df -i /tmp/image_real.oci -out-dir /tmp/compare
```

## Optimize the recovered Dockerfile

Set your API key in an environment variable first:

```bash
export LLMGW_API_KEY="<YOUR_API_KEY>"
```

Then run:

```bash
cd src
./buildkit-df \
  -i /tmp/image_real.oci \
  -o /tmp/recovered_real.Dockerfile \
  -optimize \
  -optimized-out /tmp/optimized_real.Dockerfile \
  -model llama-3.1-8b-instruct
```

The CLI sends the recovered Dockerfile as the user message content and asks the model to return only an improved Dockerfile.