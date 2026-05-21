# OTLP テレメトリセットアップガイド

mcp-guardian は監査データ (Logs + Traces) を JSON エンコードの OTLP/HTTP でエクスポートする。本ガイドは主要なクラウドバックエンドおよびセルフホスト Collector のセットアップ手順をまとめる。

## 仕組み

```
mcp-guardian
    | OTLP/HTTP (JSON)
    | POST /v1/logs, /v1/traces
    v
OpenTelemetry Collector (またはネイティブ OTLP エンドポイント)
    |
    v
クラウドバックエンド (CloudWatch, Cloud Logging, Datadog 等)
```

mcp-guardian は 2 種類のテレメトリを送出する:

- **Logs** (`/v1/logs`): 各 tool call レシートを構造化ログレコードとして
- **Traces** (`/v1/traces`): 各 tool call を duration と属性付き span として

## クイックリファレンス

| バックエンド | Collector 要否 | 備考 |
|---|---|---|
| AWS CloudWatch | SigV4 proxy または ADOT Collector | ネイティブ OTLP エンドポイントはあるが SigV4 署名必須 |
| GCP Cloud Logging / Cloud Trace | 不要 (短時間セッション) | `telemetry.googleapis.com` のネイティブ OTLP + Bearer トークン |
| Grafana Cloud | 不要 | ネイティブ OTLP エンドポイント + Basic 認証 |
| Datadog | 不要 | ネイティブ OTLP エンドポイント + API key |
| Splunk (HEC) | 不要 | 内蔵 HEC ドライバ、OTLP 不要 |
| Splunk Observability | 不要 | ネイティブ OTLP エンドポイント |
| セルフホスト (Loki, Tempo 等) | OTel Collector | Loki (logs) + Tempo (traces) にルーティング |

---

## AWS CloudWatch Logs + X-Ray

AWS CloudWatch はネイティブ OTLP エンドポイントを提供する:

```
https://logs.{region}.amazonaws.com/v1/logs       (CloudWatch Logs)
https://xray.{region}.amazonaws.com/v1/traces      (X-Ray)
```

ただしこれらのエンドポイントは **AWS SigV4 リクエスト署名** を要求する。mcp-guardian の静的ヘッダ OTLP エクスポータでは直接署名できないため、以下 2 つの方法のいずれかを取る。

### オプション A: SigV4 Proxy (軽量)

AWS の `aws-sigv4-proxy` をローカルサイドカーとして動かし、平の OTLP/HTTP リクエストに SigV4 署名を付与する。

```bash
# SigV4 proxy 起動
docker run --rm -p 4318:8080 \
  -e AWS_ACCESS_KEY_ID \
  -e AWS_SECRET_ACCESS_KEY \
  -e AWS_REGION=ap-northeast-1 \
  public.ecr.aws/aws-observability/aws-sigv4-proxy:latest \
  --name logs --region ap-northeast-1 \
  --host "logs.ap-northeast-1.amazonaws.com" \
  --port 8080
```

mcp-guardian をプロキシ経由で送出するよう設定:

```json
{
  "telemetry": {
    "otlp": {
      "endpoint": "http://localhost:4318"
    }
  }
}
```

> 注: SigV4 proxy 1 インスタンスは 1 つの上流ホストしか扱えない。logs と traces 両方なら 2 インスタンス起動するか、オプション B を使う。

### オプション B: ADOT Collector (フル機能)

**AWS Distro for OpenTelemetry (ADOT) Collector** は SigV4 を内部処理し、logs と traces を別の AWS サービスへ単一プロセスでルーティングできる。

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

mcp-guardian の設定:

```json
{
  "telemetry": {
    "otlp": {
      "endpoint": "http://localhost:4318"
    }
  }
}
```

### IAM 権限 (両オプション共通)

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

### 確認

```bash
# CloudWatch Logs を確認
aws logs filter-log-events \
  --log-group-name /mcp-guardian/audit \
  --limit 5

# X-Ray traces を確認
aws xray get-trace-summaries \
  --start-time $(date -u -d '1 hour ago' +%s) \
  --end-time $(date -u +%s)
```

---

## GCP Cloud Logging + Cloud Trace

GCP は `telemetry.googleapis.com` でネイティブ OTLP エンドポイントを提供する。認証は Bearer トークンを使用し、mcp-guardian は `gcloud` トークンコマンドで取得できる — **Collector 不要**。

### オプション A: 直接送信 (Collector なし)

mcp-guardian の OTLP エクスポータは現在 OTLP エンドポイント向けのリクエスト単位トークンリフレッシュをサポートしていない。短時間セッションなら静的トークンを、長時間運用ならラッパースクリプトを使う。

**短時間セッション (トークン有効期限 約 1 時間):**

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

新規トークン付きで起動:

```bash
export OTLP_TOKEN=$(gcloud auth print-access-token)
mcp-guardian \
  --otlp-endpoint https://telemetry.googleapis.com \
  --otlp-header "Authorization=Bearer $OTLP_TOKEN" \
  --otlp-header "x-goog-user-project=your-gcp-project-id" \
  --profile my-server
```

> 注: CLI フラグで渡した `Authorization` ヘッダは設定ファイル値を上書きするので、セッションごとに動的トークンを使える。

### オプション B: OTel Collector (長時間運用に推奨)

トークンリフレッシュが必要な継続運用には、GCP exporter 付きの OTel Collector を使う。Collector が Application Default Credentials 経由で自動的に認証情報をリフレッシュする。

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

mcp-guardian の設定:

```json
{
  "telemetry": {
    "otlp": {
      "endpoint": "http://localhost:4318"
    }
  }
}
```

### IAM ロール (両オプション共通)

- `roles/logging.logWriter` — Cloud Logging への書込
- `roles/cloudtrace.agent` — Cloud Trace への書込

```bash
gcloud projects add-iam-policy-binding your-project-id \
  --member="serviceAccount:your-sa@your-project-id.iam.gserviceaccount.com" \
  --role="roles/logging.logWriter"

gcloud projects add-iam-policy-binding your-project-id \
  --member="serviceAccount:your-sa@your-project-id.iam.gserviceaccount.com" \
  --role="roles/cloudtrace.agent"
```

### 確認

```bash
# Cloud Logging を確認
gcloud logging read 'logName="projects/your-project-id/logs/mcp-guardian-audit"' \
  --limit 5 --format json

# Cloud Trace を確認
# 開く: https://console.cloud.google.com/traces/list?project=your-project-id
```

---

## Grafana Cloud (Direct OTLP)

Grafana Cloud は OTLP/HTTP をネイティブ受信する — Collector 不要。

### 1. OTLP エンドポイントの取得

Grafana Cloud ポータル → 該当 stack → **OpenTelemetry** で OTLP エンドポイントと instance ID をコピー。

### 2. mcp-guardian の設定

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

Basic 認証値の生成:

```bash
echo -n "INSTANCE_ID:API_KEY" | base64
```

---

## Datadog (Direct OTLP)

Datadog は OTLP/HTTP をネイティブ受信する。

### 1. mcp-guardian の設定

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

Datadog サイトに応じてエンドポイントドメインを差し替える:

| サイト | エンドポイント |
|---|---|
| US1 | `https://http-intake.logs.datadoghq.com` |
| US3 | `https://http-intake.logs.us3.datadoghq.com` |
| US5 | `https://http-intake.logs.us5.datadoghq.com` |
| EU1 | `https://http-intake.logs.datadoghq.eu` |
| AP1 | `https://http-intake.logs.ap1.datadoghq.com` |

---

## Splunk HEC (直接、Collector なし)

mcp-guardian には Splunk HTTP Event Collector (HEC) ドライバが内蔵されている。OpenTelemetry Collector 不要。

### 1. Splunk 側で HEC を有効化

Splunk Web で: **Settings → Data inputs → HTTP Event Collector → New Token**。

トークンと HEC エンドポイント URL (通常 `https://splunk:8088/services/collector/event`) を控える。

### 2. mcp-guardian の設定

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

| 設定 | デフォルト | 説明 |
|---|---|---|
| `endpoint` | (必須) | Splunk HEC エンドポイント URL |
| `token` | (必須) | HEC 認証トークン |
| `index` | (デフォルト index) | 送信先 Splunk index |
| `batchSize` | 10 | N 件溜まったらフラッシュ |
| `batchTimeout` | 5000 | N ms 経過でフラッシュ |

### 3. OTLP と併用

OTLP と Splunk HEC は並行動作可能 — イベントは両方に送られる:

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

### 4. 確認

```bash
# Splunk で検索
index=mcp-audit source=mcp-guardian | head 10
```

---

## セルフホスト: OTel Collector + Loki + Tempo

logs (Loki) と traces (Tempo) のための一般的なセルフホストスタック。

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

### 2. OTel Collector 設定

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

### 3. mcp-guardian の設定

```json
{
  "telemetry": {
    "otlp": {
      "endpoint": "http://localhost:4318"
    }
  }
}
```

### 4. Grafana での閲覧

1. `http://localhost:3000` を開く
2. Loki data source を追加 → URL: `http://loki:3100`
3. Tempo data source を追加 → URL: `http://tempo:3200`
4. Explore → Loki → `{service_name="mcp-guardian"}`

---

## チューニング

### バッチ設定

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

| 設定 | デフォルト | 説明 |
|---|---|---|
| `batchSize` | 10 | N 件のレコードでフラッシュ |
| `batchTimeout` | 5000 | N ms 経過でフラッシュ (バッチが満たなくても) |

ハイスループット環境では `batchSize` を上げて HTTP リクエスト数を減らす。低レイテンシ監査には `batchTimeout` を下げる。

### レシート保持

ローカルレシートは作業データであり、信頼性の源ではない — OTLP エクスポートこそが永続ストア。

```json
{
  "defaults": {
    "maxReceiptAgeDays": 7
  }
}
```

ローカルレシートを無期限保持したい場合は `maxReceiptAgeDays: 0` で自動パージを無効化する。

---

## トラブルシューティング

### データが届かない

1. mcp-guardian の stderr に起動時 `OTLP export enabled` が出ているか確認
2. collector が到達可能か確認: `curl -s http://localhost:4318/v1/logs -d '{}' -H 'Content-Type: application/json'`
3. collector のログでエラー確認

### 認証エラー

- ヘッダが (profile ではなく) `config.json` 側に正しく設定されているか確認
- AWS の場合: IAM 認証情報が collector から見えているか確認
- GCP の場合: Application Default Credentials または service account key を確認

### レイテンシが高い

- `batchSize` を上げて HTTP ラウンドトリップを減らす
- collector を mcp-guardian とネットワーク的に近い場所に配置
- クラウドバックエンドはリージョンローカルの OTLP エンドポイントを使う
