# Build the manager binary
FROM golang:1.26.10 AS builder
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /workspace
COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download
COPY cmd/main.go cmd/main.go
COPY api/ api/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64     go build -a -ldflags "-X main.version=" -o manager cmd/main.go

# Runtime: alpine instead of distroless/static (ISI-7191). The recycle
# pipeline SourceFS.Commit() shells out to a2371e56132f0dd9e1fa210a72bf6f5250c65cdb in the
# reference checkout (ISI-7083; design §5 Sample-clearance needs the source
# SHA), and distroless/static ships no git binary - so every in-cluster
# recycle run failed at the analyzer stage. Alpine + git keeps the image
# small; nonroot UID 65532 preserved from the distroless layout.
FROM alpine:3.20
RUN apk add --no-cache git ca-certificates
ENV HOME=/tmp
COPY --from=builder /workspace/manager /manager
USER 65532:65532

ENTRYPOINT ["/manager"]
