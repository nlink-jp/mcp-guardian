#!/usr/bin/env bash
# Stop and remove the OTel Collector test container.
set -euo pipefail

CONTAINER_NAME="otel-test"

log() { printf '[otel-down] %s\n' "$*" >&2; }

if command -v podman &>/dev/null; then
    RUNTIME=podman
elif command -v docker &>/dev/null; then
    RUNTIME=docker
else
    log "Neither podman nor docker found."
    exit 0
fi

if $RUNTIME container exists "$CONTAINER_NAME" 2>/dev/null; then
    log "Stopping and removing '$CONTAINER_NAME'..."
    $RUNTIME rm -f "$CONTAINER_NAME" >/dev/null
    log "Done."
else
    log "Container '$CONTAINER_NAME' not found — nothing to do."
fi
