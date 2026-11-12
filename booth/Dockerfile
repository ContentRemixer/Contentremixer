# Build the Booth web-UI server binary (ISI-7339).
#
# Booth is a SEPARATE Go module (module `booth`) that consumes the operator's
# CRD types via `replace github.com/.../app => ../app`. So unlike app/Dockerfile
# (context: app/), this build's context must be the REPO ROOT — both app/ and
# booth/ have to be present for the replace directive to resolve. The React SPA
# is committed under booth/web/dist and compiled in via `//go:embed all:dist`,
# so there is no Node build stage: Go embeds the already-built assets.
#
# Cross-compile from the native BUILDPLATFORM (Go needs no emulation to target a
# foreign GOARCH), so a multi-arch buildx run stays fast.
FROM --platform=$BUILDPLATFORM golang:1.26.10 AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /workspace
# Order: the replaced sibling module first so the module graph + go.sum resolve.
COPY app/ app/
COPY booth/ booth/
WORKDIR /workspace/booth
RUN go mod download
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -a -o /booth ./cmd/booth

# Runtime: distroless static, non-root. Booth only serves HTTP and talks to the
# API server — unlike the operator (ISI-7191) it needs no git binary — so the
# minimal static-debian distroless image is the right fit. :nonroot runs as
# UID 65532, matching the operator's nonroot layout.
FROM gcr.io/distroless/static:nonroot
COPY --from=builder /booth /booth
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/booth"]
