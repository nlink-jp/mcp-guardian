#!/usr/bin/env bash
# Start an OpenTelemetry Collector container for integration testing.
# Usage: eval "$(scripts/otel-up.sh)"
#
# Exports:
#   OTEL_ENDPOINT   http://localhost:<mapped-port>
#   OTEL_OUTPUT_DIR  host path where file exporter writes
set -euo pipefail

CONTAINER_NAME="otel-test"
IMAGE="docker.io/otel/opentelemetry-collector-contrib:0.120.0"
READY_TIMEOUT=60

# ── helpers ──────────────────────────────────────────────────────────────────

log() { printf '[otel-up] %s\n' "$*" >&2; }
die() { printf '[otel-up] ERROR: %s\n' "$*" >&2; exit 1; }

# ── detect container runtime ─────────────────────────────────────────────────

if command -v podman &>/dev/null; then
    RUNTIME=podman
elif command -v docker &>/dev/null; then
    RUNTIME=docker
else
    die "Neither podman nor docker found."
fi
log "Using $RUNTIME"

# ── check for existing container ─────────────────────────────────────────────

if $RUNTIME container exists "$CONTAINER_NAME" 2>/dev/null; then
    STATE=$($RUNTIME inspect --format '{{.State.Status}}' "$CONTAINER_NAME")
    if [[ "$STATE" == "running" ]]; then
        log "Container '$CONTAINER_NAME' is already running — skipping start."
    else
        log "Removing stopped container '$CONTAINER_NAME'."
        $RUNTIME rm "$CONTAINER_NAME" >/dev/null
        STATE=""
    fi
fi

if ! $RUNTIME container exists "$CONTAINER_NAME" 2>/dev/null; then
    # Pick a random available port in 18000-18999
    PORT=$(python3 -c "
import socket, random
for _ in range(100):
    p = random.randint(18000, 18999)
    with socket.socket() as s:
        if s.connect_ex(('127.0.0.1', p)) != 0:
            print(p); break
")
    [[ -n "$PORT" ]] || die "Could not find a free port."

    # Create output dir and collector config
    OUTPUT_DIR=$(mktemp -d)
    CONFIG_FILE=$(mktemp)
    cat > "$CONFIG_FILE" <<'YAML'
receivers:
  otlp:
    protocols:
      http:
        endpoint: "0.0.0.0:4318"

exporters:
  file/logs:
    path: /output/logs.jsonl
    flush_interval: 1s
  file/traces:
    path: /output/traces.jsonl
    flush_interval: 1s

service:
  pipelines:
    logs:
      receivers: [otlp]
      exporters: [file/logs]
    traces:
      receivers: [otlp]
      exporters: [file/traces]
YAML

    log "Starting OTel Collector on localhost:${PORT}..."
    $RUNTIME run -d \
        --name "$CONTAINER_NAME" \
        -p "${PORT}:4318" \
        -v "${CONFIG_FILE}:/etc/otelcol-contrib/config.yaml:ro,z" \
        -v "${OUTPUT_DIR}:/output:z" \
        "$IMAGE" >/dev/null
fi

# Resolve the mapped port
PORT=$($RUNTIME port "$CONTAINER_NAME" 4318/tcp | head -1 | cut -d: -f2)
ENDPOINT="http://localhost:${PORT}"

# Resolve the output dir from container mount
OUTPUT_DIR=$($RUNTIME inspect --format '{{range .Mounts}}{{if eq .Destination "/output"}}{{.Source}}{{end}}{{end}}' "$CONTAINER_NAME")

# ── wait for collector to be ready ───────────────────────────────────────────

log "Waiting for OTel Collector at ${ENDPOINT} (up to ${READY_TIMEOUT}s)..."
deadline=$(( $(date +%s) + READY_TIMEOUT ))
until curl -sf "${ENDPOINT}/v1/logs" \
        -H 'Content-Type: application/json' \
        -d '{"resourceLogs":[]}' \
        -o /dev/null 2>/dev/null; do
    if (( $(date +%s) >= deadline )); then
        $RUNTIME logs "$CONTAINER_NAME" >&2
        die "OTel Collector did not become ready within ${READY_TIMEOUT}s."
    fi
    sleep 1
done
log "OTel Collector is ready."

# ── emit export statements ───────────────────────────────────────────────────

printf 'export OTEL_ENDPOINT="%s"\n'  "$ENDPOINT"
printf 'export OTEL_OUTPUT_DIR="%s"\n' "$OUTPUT_DIR"
log "Run:  eval \"\$(scripts/otel-up.sh)\"  to set env vars in your shell."
