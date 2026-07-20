#!/usr/bin/env bash
set -euo pipefail

# Generates a synthetic OCI archive with BuildKit provenance attestation
# Usage: ./gen_example.sh /tmp/image-example.oci

OUT=${1:-/tmp/image-example.oci}
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

mkdir -p "$TMPDIR/blobs/sha256"

# The Dockerfile to embed
DOCKERFILE=$'FROM alpine:3.20\nRUN echo hello\n'

# Base64 encode using python3 for portability
ENCODED=$(printf "%s" "$DOCKERFILE" | python3 -c 'import sys,base64; print(base64.b64encode(sys.stdin.read().encode()).decode())')

# index.json pointing to an attestation-manifest
cat > "$TMPDIR/index.json" <<JSON
{
  "manifests": [
    {
      "digest": "sha256:attestation",
      "annotations": {
        "vnd.docker.reference.type": "attestation-manifest"
      }
    }
  ]
}
JSON

# attestation manifest referencing the in-toto statement
cat > "$TMPDIR/blobs/sha256/attestation" <<JSON
{
  "layers": [
    { "digest": "sha256:statement" }
  ]
}
JSON

# in-toto statement with the buildkit embedded Dockerfile
cat > "$TMPDIR/blobs/sha256/statement" <<JSON
{
  "predicate": {
    "runDetails": {
      "metadata": {
        "buildkit_metadata": {
          "source": {
            "infos": [ { "data": "${ENCODED}" } ]
          }
        }
      }
    }
  }
}
JSON

# create tar archive
(cd "$TMPDIR" && tar -cf "$OUT" .)

echo "Written synthetic OCI archive to: $OUT"
