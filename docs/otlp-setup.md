# OTLP Telemetry Setup Guide

mcp-guardian exports audit data (Logs + Traces) via OTLP/HTTP with JSON encoding. This guide covers setup for major cloud backends and self-hosted collectors.

## How It Works

```
mcp-guardian
    | OTLP/HTTP (JSON)
    | POST /v1/logs, /v1/traces
    v
OpenTelemetry Collector (or native OTLP endpoint)
    |
    v
Cloud Backend (CloudWatch, Cloud Logging, Datadog, etc.)
```

mcp-guardian sends two types of telemetry:

- **Logs** (`/v1/logs`): Each tool call receipt as a structured log record
- **Traces** (`/v1/traces`): Each tool call as a span with duration and attributes

## Quick Reference

| Backend | Collector Required? | Notes |
|---------|-------------------|-------|
| AWS CloudWatch | SigV4 proxy or ADOT Collector | Native OTLP endpoint exists but requires SigV4 signing |
| GCP Cloud Logging / Cloud Trace | Not required (short sessions) | Native OTLP via `telemetry.googleapis.com` with Bearer token |
| Grafana Cloud | Not required | Native OTLP endpoint with Basic auth |
| Datadog | Not required | Native OTLP endpoint with API key |
| Splunk (HEC) | Not required | Built-in HEC driver, no OTLP needed |
| Splunk Observability | Not required | Native OTLP endpoint |
| Self-hosted (Loki, Tempo, etc.) | OTel Collector | Routes to Loki (logs) + Tempo (traces) |

---

## AWS CloudWatch Logs + X-Ray

AWS CloudWatch provides native OTLP endpoints:

```
https://logs.{region}.amazonaws.com/v1/logs       (CloudWatch Logs)
https://xray.{region}.amazonaws.com/v1/traces      (X-Ray)
```

However, these endpoints require **AWS SigV4 request signing**, which mcp-guardian's static-header OTLP exporter cannot perform directly. Two approaches are available:

### Option A: SigV4 Proxy (lightweight)

Use AWS's `aws-sigv4-proxy` as a local sidecar to add SigV4 signing to plain OTLP/HTTP requests.

```bash
# Run SigV4 proxy
docker run --rm -p 4318:8080 \
  -e AWS_ACCESS_KEY_ID \
  -e AWS_SECRET_ACCESS_KEY \
  -e AWS_REGION=ap-northeast-1 \
  public.ecr.aws/aws-observability/aws-sigv4-proxy:latest \
  --name logs --region ap-northeast-1 \
  --host "logs.ap-northeast-1.amazonaws.com" \
  --port 8080
```

Configure mcp-guardian to send through the proxy:

```json
{
  "telemetry": {
    "otlp": {
      "endpoint": "http://localhost:4318"
    }
  }
}
```

> Note: A single SigV4 proxy instance handles one upstream host. For both logs and traces, run two instances or use Option B.

### Option B: ADOT Collector (full-featured)

The **AWS Distro for OpenTelemetry (ADOT) Collector** handles SigV4 internally and can route logs and traces to separate AWS services in a single process.

```yaml
# otel-collector-config.yaml
receivers:
  otlp:
    protocols:
      http:
        endpoint: "0.0.0.0:4318"

exporters:
  awscloudwatchlogs:
    log_group_name: "/mcp-guardian/audit"
    log_stream_name: "receipts"
    region: "ap-northeast-1"

  awsxray:
    region: "ap-northeast-1"

service:
  pipelines:
    logs:
      receivers: [otlp]
      exporters: [awscloudwatchlogs]
    traces:
      receivers: [otlp]
      exporters: [awsxray]
```

```bash
docker run --rm -p 4318:4318 \
  -v $(pwd)/otel-collector-config.yaml:/etc/otel/config.yaml \
  -e AWS_ACCESS_KEY_ID \
  -e AWS_SECRET_ACCESS_KEY \
  -e AWS_REGION=ap-northeast-1 \
  public.ecr.aws/aws-observability/aws-otel-collector:latest \
  --config /etc/otel/config.yaml
```

Configure mcp-guardian:

```json
{
  "telemetry": {
    "otlp": {
      "endpoint": "http://localhost:4318"
    }
  }
}
```

### IAM Permissions (both options)

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "logs:CreateLogGroup",
        "logs:CreateLogStream",
        "logs:PutLogEvents",
        "xray:PutTraceSegments",
        "xray:PutTelemetryRecords"
      ],
      "Resource": "*"
    }
  ]
}
```

### Verify

```bash
# Check CloudWatch Logs
aws logs filter-log-events \
  --log-group-name /mcp-guardian/audit \
  --limit 5

# Check X-Ray traces
aws xray get-trace-summaries \
  --start-time $(date -u -d '1 hour ago' +%s) \
  --end-time $(date -u +%s)
```

---

## GCP Cloud Logging + Cloud Trace

GCP provides a native OTLP endpoint at `telemetry.googleapis.com`. Authentication uses a Bearer token, which mcp-guardian can obtain via `gcloud` token command — **no Collector needed**.

### Option A: Direct (no Collector)

The OTLP exporter in mcp-guardian does not currently support per-request token refresh for the OTLP endpoint. Use a static token for short sessions, or a wrapper script for long-running use.

**Short-lived session (token valid for ~1 hour):**

```json
{
  "telemetry": {
    "otlp": {
      "endpoint": "https://telemetry.googleapis.com",
      "headers": {
        "x-goog-user-project": "your-gcp-project-id"
      }
    }
  }
}
```

Then start with a fresh token:

```bash
export OTLP_TOKEN=$(gcloud auth print-access-token)
mcp-guardian \
  --otlp-endpoint https://telemetry.googleapis.com \
  --otlp-header "Authorization=Bearer $OTLP_TOKEN" \
  --otlp-header "x-goog-user-project=your-gcp-project-id" \
  --profile my-server
```

> Note: The `Authorization` header set via CLI flag overrides the config file value, so dynamic tokens work per session.

### Option B: OTel Collector (recommended for long-running)

For continuous operation where token refresh is needed, use the OTel Collector with the GCP exporter. The Collector handles credential refresh automatically via Application Default Credentials.

```yaml
# otel-collector-config.yaml
receivers:
  otlp:
    protocols:
      http:
        endpoint: "0.0.0.0:4318"

exporters:
  googlecloud:
    project: "your-gcp-project-id"

  googlecloud/logging:
    project: "your-gcp-project-id"
    log:
      default_log_name: "mcp-guardian-audit"

service:
  pipelines:
    logs:
      receivers: [otlp]
      exporters: [googlecloud/logging]
    traces:
      receivers: [otlp]
      exporters: [googlecloud]
```

```bash
docker run --rm -p 4318:4318 \
  -v $(pwd)/otel-collector-config.yaml:/etc/otel/config.yaml \
  -v $HOME/.config/gcloud:/root/.config/gcloud:ro \
  -e GOOGLE_APPLICATION_CREDENTIALS=/root/.config/gcloud/application_default_credentials.json \
  otel/opentelemetry-collector-contrib:latest \
  --config /etc/otel/config.yaml
```

Configure mcp-guardian:

```json
{
  "telemetry": {
    "otlp": {
      "endpoint": "http://localhost:4318"
    }
  }
}
```

### IAM Roles (both options)

- `roles/logging.logWriter` — write to Cloud Logging
- `roles/cloudtrace.agent` — write to Cloud Trace

```bash
gcloud projects add-iam-policy-binding your-project-id \
  --member="serviceAccount:your-sa@your-project-id.iam.gserviceaccount.com" \
  --role="roles/logging.logWriter"

gcloud projects add-iam-policy-binding your-project-id \
  --member="serviceAccount:your-sa@your-project-id.iam.gserviceaccount.com" \
  --role="roles/cloudtrace.agent"
```

### Verify

```bash
# Check Cloud Logging
gcloud logging read 'logName="projects/your-project-id/logs/mcp-guardian-audit"' \
  --limit 5 --format json

# Check Cloud Trace
# Open: https://console.cloud.google.com/traces/list?project=your-project-id
```

---

## Grafana Cloud (Direct OTLP)

Grafana Cloud accepts OTLP/HTTP natively — no collector needed.

### 1. Get OTLP Endpoint

From Grafana Cloud portal → your stack → **OpenTelemetry** → copy the OTLP endpoint and instance ID.

### 2. Configure mcp-guardian

```json
{
  "telemetry": {
    "otlp": {
      "endpoint": "https://otlp-gateway-prod-ap-northeast-0.grafana.net/otlp",
      "headers": {
        "Authorization": "Basic <base64(instanceId:apiKey)>"
      }
    }
  }
}
```

Generate the Basic auth value:

```bash
echo -n "INSTANCE_ID:API_KEY" | base64
```

---

## Datadog (Direct OTLP)

Datadog accepts OTLP/HTTP natively.

### 1. Configure mcp-guardian

```json
{
  "telemetry": {
    "otlp": {
      "endpoint": "https://http-intake.logs.datadoghq.com/api/v2/otlp",
      "headers": {
        "DD-API-KEY": "<your-datadog-api-key>"
      }
    }
  }
}
```

Replace the endpoint domain for your Datadog site:

| Site | Endpoint |
|------|----------|
| US1 | `https://http-intake.logs.datadoghq.com` |
| US3 | `https://http-intake.logs.us3.datadoghq.com` |
| US5 | `https://http-intake.logs.us5.datadoghq.com` |
| EU1 | `https://http-intake.logs.datadoghq.eu` |
| AP1 | `https://http-intake.logs.ap1.datadoghq.com` |

---

## Splunk HEC (Direct, no Collector)

mcp-guardian has a built-in Splunk HTTP Event Collector (HEC) driver. No OpenTelemetry Collector needed.

### 1. Enable HEC in Splunk

In Splunk Web: **Settings → Data inputs → HTTP Event Collector → New Token**.

Note the token and the HEC endpoint URL (typically `https://splunk:8088/services/collector/event`).

### 2. Configure mcp-guardian

```json
{
  "telemetry": {
    "splunk": {
      "endpoint": "https://splunk:8088/services/collector/event",
      "token": "<your-hec-token>",
      "index": "mcp-audit",
      "batchSize": 10,
      "batchTimeout": 5000
    }
  }
}
```

| Setting | Default | Description |
|---------|---------|-------------|
| `endpoint` | (required) | Splunk HEC endpoint URL |
| `token` | (required) | HEC authentication token |
| `index` | (default index) | Target Splunk index |
| `batchSize` | 10 | Flush after N events |
| `batchTimeout` | 5000 | Flush after N ms |

### 3. Using with OTLP simultaneously

OTLP and Splunk HEC can run in parallel — events are sent to both:

```json
{
  "telemetry": {
    "otlp": {
      "endpoint": "http://otel-collector:4318"
    },
    "splunk": {
      "endpoint": "https://splunk:8088/services/collector/event",
      "token": "<token>"
    }
  }
}
```

### 4. Verify

```bash
# Search in Splunk
index=mcp-audit source=mcp-guardian | head 10
```

---

## Self-Hosted: OTel Collector + Loki + Tempo

A common self-hosted stack for logs (Loki) and traces (Tempo).

### 1. Docker Compose

```yaml
version: "3"
services:
  otel-collector:
    image: otel/opentelemetry-collector-contrib:latest
    ports:
      - "4318:4318"
    volumes:
      - ./otel-config.yaml:/etc/otel/config.yaml
    command: ["--config", "/etc/otel/config.yaml"]

  loki:
    image: grafana/loki:latest
    ports:
      - "3100:3100"

  tempo:
    image: grafana/tempo:latest
    ports:
      - "3200:3200"
    volumes:
      - ./tempo-config.yaml:/etc/tempo/config.yaml
    command: ["-config.file=/etc/tempo/config.yaml"]

  grafana:
    image: grafana/grafana:latest
    ports:
      - "3000:3000"
    environment:
      - GF_AUTH_ANONYMOUS_ENABLED=true
      - GF_AUTH_ANONYMOUS_ORG_ROLE=Admin
```

### 2. OTel Collector Config

```yaml
# otel-config.yaml
receivers:
  otlp:
    protocols:
      http:
        endpoint: "0.0.0.0:4318"

exporters:
  loki:
    endpoint: "http://loki:3100/loki/api/v1/push"

  otlp/tempo:
    endpoint: "http://tempo:4317"
    tls:
      insecure: true

service:
  pipelines:
    logs:
      receivers: [otlp]
      exporters: [loki]
    traces:
      receivers: [otlp]
      exporters: [otlp/tempo]
```

### 3. Configure mcp-guardian

```json
{
  "telemetry": {
    "otlp": {
      "endpoint": "http://localhost:4318"
    }
  }
}
```

### 4. View in Grafana

1. Open `http://localhost:3000`
2. Add Loki data source → URL: `http://loki:3100`
3. Add Tempo data source → URL: `http://tempo:3200`
4. Explore → Loki → `{service_name="mcp-guardian"}`

---

## Tuning

### Batch Settings

```json
{
  "telemetry": {
    "otlp": {
      "endpoint": "http://localhost:4318",
      "batchSize": 10,
      "batchTimeout": 5000
    }
  }
}
```

| Setting | Default | Description |
|---------|---------|-------------|
| `batchSize` | 10 | Flush after N records |
| `batchTimeout` | 5000 | Flush after N ms (even if batch not full) |

For high-throughput environments, increase `batchSize` to reduce HTTP requests. For low-latency auditing, decrease `batchTimeout`.

### Receipt Retention

Local receipts are working data, not the source of truth — OTLP export is the durable store.

```json
{
  "defaults": {
    "maxReceiptAgeDays": 7
  }
}
```

Set `maxReceiptAgeDays: 0` to disable auto-purge if you need local receipts indefinitely.

---

## Troubleshooting

### No data arriving

1. Check mcp-guardian stderr for `OTLP export enabled` on startup
2. Verify the collector is reachable: `curl -s http://localhost:4318/v1/logs -d '{}' -H 'Content-Type: application/json'`
3. Check collector logs for errors

### Authentication failures

- Ensure headers are set correctly in `config.json` (not in the profile)
- For AWS: verify IAM credentials are available to the collector
- For GCP: verify Application Default Credentials or service account key

### High latency

- Increase `batchSize` to reduce HTTP round-trips
- Ensure the collector is network-close to mcp-guardian
- For cloud backends, use the region-local OTLP endpoint
