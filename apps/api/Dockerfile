# syntax=docker/dockerfile:1
# Build from the repo root:
#   docker build -f apps/api/Dockerfile apps/api                  # control plane API
#   docker build -f apps/api/Dockerfile --target agent apps/api   # data plane agent

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/ ./cmd/api ./cmd/migrate ./cmd/agent

# The agent connects out to the control plane and needs no ports. With the
# Docker provider it also needs the Docker socket, which is root-equivalent;
# run it with --user root on a host dedicated to CI.
FROM gcr.io/distroless/static-debian12:nonroot AS agent
COPY --from=build /out/agent /usr/local/bin/agent
USER nonroot
ENTRYPOINT ["/usr/local/bin/agent"]

FROM gcr.io/distroless/static-debian12:nonroot AS api
COPY --from=build /out/api /out/migrate /usr/local/bin/
ENV APP_ENV=production PORT=8080
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/usr/local/bin/api"]
