#!/usr/bin/env bash
# validate-collector-config.sh — O11y-4 (ISI-7161) "validate-ocb-manifest" check.
#
# The persona/RAG Collector (deploy/overlays/k8squad-test/collector/) runs the
# stock opentelemetry-collector-contrib distro, so no custom OCB build is needed
# (strategy §8). This script PROVES that by running the contrib binary's own
# `validate` subcommand against the exact config the overlay ships: `validate`
# parses the config AND instantiates every referenced component, failing with a
# non-zero exit if any one (memory_limiter, batch, k8sattributes, transform,
# redaction, tail_sampling, prometheus, k8s_cluster, otlp, otlphttp, health_check)
# is absent from the build. A green run == all required components present.
#
# Usage: .github/scripts/validate-collector-config.sh
#   Runs from anywhere; resolves paths relative to the repo. Needs curl + tar
#   (CI: ubuntu-latest has both). Downloads the pinned contrib release binary —
#   NOT the container image: the image entrypoint/path differs across releases,
#   whereas the release tarball always ships a plain `otelcol-contrib` binary.
#
# The Collector version is read from the Deployment manifest so there is a single
# source of truth for the pinned version — bump it there and this follows.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CONFIG="${ROOT}/deploy/overlays/k8squad-test/collector/otel-collector-config.yaml"
DEPLOY="${ROOT}/deploy/overlays/k8squad-test/collector/deployment.yaml"

[[ -f "$CONFIG" ]] || { echo "ERROR: collector config not found at $CONFIG" >&2; exit 1; }

# Pull the pinned version out of the Deployment image tag (…contrib:X.Y.Z), so
# validation always runs against the version actually deployed.
VERSION="$(grep -oE 'opentelemetry-collector-contrib:[0-9]+\.[0-9]+\.[0-9]+' "$DEPLOY" | head -n1 | cut -d: -f2)"
[[ -n "$VERSION" ]] || { echo "ERROR: could not read Collector version from $DEPLOY" >&2; exit 1; }

ARCH="$(uname -m)"; case "$ARCH" in x86_64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; esac
TARBALL="otelcol-contrib_${VERSION}_linux_${ARCH}.tar.gz"
URL="https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/v${VERSION}/${TARBALL}"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
echo "Validating collector config against otelcol-contrib v${VERSION} (${ARCH})"
curl -sSfL "$URL" | tar -xz -C "$TMP" otelcol-contrib

# The config interpolates ${env:DT_OTLP_ENDPOINT} / ${env:DT_API_TOKEN}; supply
# throwaway values so `validate` can expand them (it never dials anything —
# validation is purely static component instantiation + config-schema checks).
DT_OTLP_ENDPOINT="https://validate-only.example.com/api/v2/otlp" \
DT_API_TOKEN="validate-only-dummy" \
  "$TMP/otelcol-contrib" validate --config="$CONFIG"

echo "OK: all configured Collector components are present in the contrib distro."
