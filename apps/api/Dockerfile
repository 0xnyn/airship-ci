# syntax=docker/dockerfile:1
# Build from the repo root:
#   docker build -f apps/api/Dockerfile apps/api

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/ ./cmd/api ./cmd/migrate

FROM gcr.io/distroless/static-debian12:nonroot AS api
COPY --from=build /out/api /out/migrate /usr/local/bin/
ENV APP_ENV=production PORT=8080
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/usr/local/bin/api"]
