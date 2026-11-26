# Tolarix server image (web mode).
# Build from the repository root:
#   docker build -f deploy/Dockerfile -t tolarix --build-arg VERSION=1.0.0 .
# Multi-arch (the build stages run natively and cross-compile, no emulation):
#   docker buildx build -f deploy/Dockerfile --platform linux/amd64,linux/arm64 .

# ---- Web UI ----
FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS web
WORKDIR /src
RUN corepack enable
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY web/package.json web/
RUN pnpm install --frozen-lockfile --filter web
COPY web/ web/
# The build copies itself into core/internal/webui/dist for embedding.
RUN mkdir -p core/internal/webui/dist && pnpm --filter web build

# ---- Go binary ----
FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS core
WORKDIR /src/core
COPY core/go.mod core/go.sum ./
RUN go mod download
COPY core/ ./
COPY --from=web /src/core/internal/webui/dist/ internal/webui/dist/
ARG VERSION=dev
ARG COMMIT=""
ARG DATE=""
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
      -ldflags "-s -w \
        -X github.com/davidemaggi/tolarix/core/internal/buildinfo.Version=${VERSION} \
        -X github.com/davidemaggi/tolarix/core/internal/buildinfo.Commit=${COMMIT} \
        -X github.com/davidemaggi/tolarix/core/internal/buildinfo.Date=${DATE}" \
      -o /out/tolarix ./cmd/tolarix \
 && mkdir -p /out/data

# ---- Runtime ----
# Static binary, no shell, runs as uid 65532.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=core /out/tolarix /tolarix
# Owned by the runtime user, so a new named volume inherits writable ownership.
COPY --from=core --chown=65532:65532 /out/data /data
ENV TOLARIX_MODE=web \
    TOLARIX_DATA_DIR=/data \
    TOLARIX_PORT=8080
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/tolarix", "healthcheck"]
ENTRYPOINT ["/tolarix"]
CMD ["serve"]
