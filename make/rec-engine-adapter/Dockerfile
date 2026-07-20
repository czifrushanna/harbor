# Build the rec-engine optimizer adapter service.
# Self-contained multi-stage build: does not depend on `make compile` or the photon
# base images. Build from the repository root:
#
#   docker build -f make/rec-engine-adapter/Dockerfile -t rec-engine-adapter:dev .
#
ARG golang_image=registry.cern.ch/docker.io/library/golang:1.25

FROM ${golang_image} AS build

ARG version=dev

WORKDIR /harbor/src
COPY src/ .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-X github.com/goharbor/harbor/src/pkg/optimizeradapter.Version=${version}" \
    -o /optimizer-adapter ./cmd/optimizer-adapter

# The adapter calls the LLM gateway over HTTPS, so CA certificates are required.
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /optimizer-adapter /optimizer-adapter

EXPOSE 8080
USER 10000
ENTRYPOINT ["/optimizer-adapter"]
