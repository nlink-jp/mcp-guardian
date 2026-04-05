# mcp-guardian

MCP (Model Context Protocol) サーバー向けガバナンスプロキシ。外部依存ゼロの単一バイナリとして構築。

[@sovereign-labs/mcp-proxy](https://github.com/Born14/mcp-proxy) に着想を得て、サプライチェーンセキュリティと運用堅牢性のために Go で再実装。

## なぜ必要か

MCP ツールサーバーは AI エージェントに強力な機能を与えます。監視なしでは、エージェントは失敗した操作を繰り返し、リソースを枯渇させ、不正な変更を行う可能性があります。`mcp-guardian` は MCP クライアントとサーバーの間に透過的に配置され、以下を提供します:

- **改竄検知可能な監査証跡** -- 全ツールコールで SHA-256 ハッシュチェーンレシートを生成
- **失敗ベース制約学習** -- 同一操作の失敗再試行を自動ブロック
- **バジェット・収束制御** -- 暴走ループと過剰な API コールを防止
- **スキーマ検証** -- 転送前にツール引数を検証
- **権限追跡** -- エポックベースのセッション有効性確認
- **ツールマスキング** -- エージェントからツールを強制的に隠蔽（ワイルドカードパターン対応）
- **OpenTelemetry エクスポート** -- OTLP/HTTP Logs + Traces によるエンタープライズテレメトリ収集

## 特徴

- 単一静的バイナリ（約6MB）、ランタイム依存なし
- Go 標準ライブラリのみ -- 外部モジュールゼロ
- **デュアルトランスポート**: stdio（デフォルト）および HTTP/SSE（Streamable HTTP）
- **MCP Authorization Discovery**: OAuth2 エンドポイント自動発見 + クライアント自動登録 — 手動 OAuth アプリ設定不要
- **OAuth2 認証**: client_credentials および authorization_code（ブラウザログイン）、自動リフレッシュ
- **ブラウザログイン**: `--login` で OAuth2 自動発見 → クライアント登録 → ブラウザ認証 → トークン保存
- **外部トークンコマンド**: `gcloud`, `vault` 等の既存 CLI ツールと統合
- **401 自動リトライ**: 認証失敗時の透過的トークンリフレッシュ
- ハッシュチェーンレシート台帳（JSONL、検証可能）
- **レシート自動パージ**: 設定可能な保持期間、長期保存は OTLP/Splunk
- 5段階ガバナンスパイプライン
- エージェント自己統治用の5つのメタツール注入
- セッション事後分析 CLI（view, verify, explain）
- Webhook 通知（汎用、Discord、Telegram）
- OTLP/HTTP エクスポート（Logs + Traces、バッチ送信、外部依存ゼロ）
- glob パターンによるツールマスキング（`--mask`, `--profile`）
- 2段階設定（グローバルシステム設定 + サーバー固有設定）
- `.mcp.json` の wrap/unwrap による簡単統合

## インストール

```bash
# ソースから
git clone https://github.com/nlink-jp/mcp-guardian.git
cd mcp-guardian
make install

# プレフィックス指定
make install PREFIX=$HOME/.local
```

## クイックスタート

### サーバープロファイル（推奨）

`~/.config/mcp-guardian/profiles/filesystem.json` を作成:

```json
{
  "name": "filesystem",
  "upstream": {
    "command": "npx",
    "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
  },
  "governance": { "enforcement": "advisory" }
}
```

```bash
mcp-guardian --profile filesystem
```

OAuth2 認証付き SSE サーバー（自動発見）:

```json
{
  "name": "atlassian",
  "upstream": {
    "transport": "sse",
    "url": "https://mcp.atlassian.com/v1/mcp"
  }
}
```

OAuth2 設定不要 — `--login` が自動でエンドポイント発見 + クライアント登録:

```bash
# 初回: OAuth2 自動発見 → クライアント登録 → ブラウザ認証
mcp-guardian --login atlassian

# 以降: トークン自動更新
mcp-guardian --profile atlassian

# Claude Code に追加
claude mcp add atlassian -- mcp-guardian --profile atlassian
```

### インラインモード（プロファイルなし）

```bash
mcp-guardian -- npx -y @modelcontextprotocol/server-filesystem /tmp

# オプション付き
mcp-guardian --enforcement advisory -- npx -y @modelcontextprotocol/server-filesystem /tmp

# SSE トランスポート
mcp-guardian --transport sse --upstream-url http://localhost:8080/mcp
```

### セッション事後分析

```bash
mcp-guardian --profile atlassian --view
mcp-guardian --profile atlassian --view --tool write_file --outcome error
mcp-guardian --profile atlassian --verify
mcp-guardian --profile atlassian --explain
mcp-guardian --profile atlassian --receipts
```

## CLI リファレンス

```
# プロキシモード
mcp-guardian --profile <name|path>

# プロファイル管理
--profile <name|path>           サーバープロファイル（名前 or パス）
--profiles                      利用可能なプロファイル一覧
--login <name|path>             OAuth2 ブラウザログイン（エンドポイント自動発見）

# グローバル設定
--config <path>                 グローバル設定ファイル（テレメトリ、デフォルト値）

# 分析（--profile または --state-dir が必要）
--view                          レシートタイムライン
--verify                        ハッシュチェーン検証
--explain                       セッション説明
--receipts                      サマリー
--state-dir <dir>               状態ディレクトリ上書き
--tool <name>                   ツール名フィルタ（--view 用）
--outcome <outcome>             結果フィルタ（--view 用）
--limit <n>                     レシート数制限（--view 用）

# 情報
--version                       バージョン表示
```

トランスポート、認証、ガバナンス、マスキングの設定は全てサーバープロファイル（JSON）で定義。テンプレートは [examples/profiles/](examples/profiles/) を参照。

## ガバナンスパイプライン

全ての `tools/call` は5段階のゲートを通過します:

1. **バジェット** -- コール数が `--max-calls` を超えた場合拒否
2. **スキーマ** -- キャッシュされた `inputSchema` に対して引数を検証
3. **制約** -- ツール+ターゲットが過去の失敗にマッチする場合ブロック（TTL: 1時間）
4. **権限** -- セッションエポックが権限エポックと一致するか検証
5. **収束** -- ループ検知（同一失敗3回以上、同一ツール+ターゲット2分間で5回以上）

`strict` モードではゲート失敗時にコールをブロック。`advisory` モードでは違反をログに記録しつつ転送。

## メタツール

プロキシはエージェントが呼び出せる5つのガバナンスツールを注入します:

| ツール | 説明 |
|--------|------|
| `governance_status` | コントローラID、エポック、制約、レシート深度を確認 |
| `governance_bump_authority` | エポックを進める（現在のセッションを無効化） |
| `governance_declare_intent` | 目標+述語を宣言して帰属追跡 |
| `governance_clear_intent` | 宣言済みインテントをクリア |
| `governance_convergence_status` | ループ検知状態を確認 |

## ツールマスキング

エージェントからツールを完全に隠蔽します。マスクされたツールは `tools/list` レスポンスから除外され、呼び出し時は汎用的な "tool not found" エラーを返します。ツールの存在自体をエージェントに知らせないことで、回避行動の試行を防ぎます。

プロファイルで指定:

```json
{
  "mask": ["write_*", "delete_*"]
}
```

パターンは glob 構文を使用（`*` は任意の文字列、`?` は1文字にマッチ）。`advisory` モードではマスクせずログのみ記録。

## 設定

2層の設定でシステムテレメトリとサーバー固有ポリシーを分離:

```
~/.config/mcp-guardian/
  config.json              # システムグローバル（テレメトリ + 組織デフォルト）
  profiles/
    github-mcp.json        # サーバープロファイル
    filesystem.json
```

すぐに使えるテンプレートは [examples/](examples/) を参照。

### システムグローバル設定

`~/.config/mcp-guardian/config.json` から自動検出。`--config` で明示指定も可。

全 MCP サーバーインスタンスで共有。MDM/EMM 配布に最適。

```json
{
  "telemetry": {
    "otlp": {
      "endpoint": "http://otel-collector:4318",
      "headers": { "Authorization": "Bearer org-token" },
      "batchSize": 10,
      "batchTimeout": 5000
    },
    "webhooks": ["https://hooks.slack.com/..."]
  },
  "defaults": {
    "enforcement": "strict",
    "schema": "warn"
  }
}
```

レガシーフォーマット（トップレベル `otlp`/`webhooks`）も後方互換のためサポート。

### サーバープロファイル (`--profile`)

MCP サーバーごとの設定。`~/.config/mcp-guardian/profiles/` に配置、またはパスで指定。

```json
{
  "name": "my-server",
  "upstream": {
    "transport": "stdio",
    "command": "npx",
    "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
  },
  "governance": {
    "enforcement": "advisory",
    "schema": "strict",
    "maxCalls": 50
  },
  "mask": ["write_*", "execute_*"]
}
```

OAuth2 認証付き SSE:

```json
{
  "name": "github-mcp",
  "upstream": { "transport": "sse", "url": "http://mcp.example.com/mcp" },
  "auth": {
    "oauth2": {
      "tokenUrl": "https://auth.example.com/oauth2/token",
      "clientId": "my-client",
      "clientSecret": "my-secret",
      "scopes": ["mcp:read", "mcp:write"]
    }
  },
  "governance": { "enforcement": "strict" }
}
```

外部トークンコマンド:

```json
{
  "name": "gcp-server",
  "upstream": { "transport": "sse", "url": "http://mcp.example.com/mcp" },
  "auth": {
    "tokenCommand": { "command": "gcloud", "args": ["auth", "print-access-token"] }
  }
}
```

### 優先順位

```
デフォルト → グローバル設定（自動検出 or --config） → プロファイル（--profile） → CLI フラグ
```

CLI フラグが常に最優先。プロファイル内の機密値（OAuth2 シークレット等）は `ps` で露出しません。

## OTLP テレメトリエクスポート

OTLP/HTTP JSON エンコーディングで、OpenTelemetry 対応バックエンド（Datadog、Grafana、Splunk 等）に監査データをエクスポート。外部依存ゼロ — Go 標準ライブラリのみで実装。

```bash
mcp-guardian \
  --otlp-endpoint http://otel-collector:4318 \
  --otlp-header "Authorization=Bearer token" \
  -- npx server
```

- **Logs**: 各ツールコールレシートが構造化ログレコードに変換
- **Traces**: 各ツールコールが期間・ステータス・属性付きスパンに変換
- **バッチ送信**: サイズ、タイマー、またはシャットダウン時にフラッシュ
| ドライバ | 用途 | 設定キー |
|---------|------|---------|
| **OTLP/HTTP** | CloudWatch, GCP, Grafana Cloud, Datadog 等 | `telemetry.otlp` |
| **Splunk HEC** | Splunk Enterprise / Cloud（直接送信、Collector 不要） | `telemetry.splunk` |

両ドライバは並列動作可能。ローカルレシートは `maxReceiptAgeDays`（デフォルト: 7日）で自動パージ — テレメトリが永続保存先。

AWS、GCP、Grafana Cloud、Datadog、Splunk、セルフホスト構成の詳細は [docs/otlp-setup.md](docs/otlp-setup.md) を参照。

## アーキテクチャ

```
エージェント (Claude, GPT, etc.)
  | stdin/stdout (JSON-RPC 2.0)
mcp-guardian
  | Transport インターフェース
  +-- stdio: 子プロセスへの stdin/stdout パイプ（デフォルト）
  +-- sse:   リモートサーバーへの HTTP POST + SSE ストリーム
上流 MCP サーバー
```

詳細なアーキテクチャドキュメントは [docs/architecture.md](docs/architecture.md) を参照。

### 状態ディレクトリ (.governance/)

| ファイル | 内容 |
|---------|------|
| `receipts.jsonl` | 追記専用ハッシュチェーン監査証跡 |
| `constraints.json` | TTL 付き学習済み失敗フィンガープリント |
| `controller.json` | 安定コントローラ UUID |
| `authority.json` | エポック + セッションバインディング + ジェネシスハッシュ |
| `intent.json` | 現在の宣言済みインテント |

## ビルド

```bash
make build              # dist/ にビルド
make install            # /usr/local/bin にインストール
make test               # ユニットテスト実行
make check              # lint + test
make integration-test   # OTLP 統合テスト実行（podman/docker 必要）
make otel-up            # OTel Collector を起動（手動テスト用）
make otel-down          # OTel Collector を停止
make clean              # ビルド成果物削除
make help               # 全ターゲット表示
```

## ライセンス

MIT License. Copyright (c) 2026 magifd2

## 謝辞

本プロジェクトの核となる設計は、[Born14](https://github.com/Born14) 氏の [@sovereign-labs/mcp-proxy](https://github.com/Born14/mcp-proxy) に負っています。

オリジナルの Node.js/TypeScript 実装は、**MCP サーバー向け透過的ガバナンスプロキシ**という着想を切り拓きました。AI エージェントとツールサーバーの間に、双方に意識させることなく監査レイヤーを挿入するというアイデアです。本プロジェクトが採用した主要な概念は以下の通りです:

- **ハッシュチェーンレシート台帳** -- 全ツールコールを改竄検知可能な不変レコードとして扱う（エージェント操作の git コミットのようなもの）
- **失敗ベース制約学習** -- 失敗したコールをフィンガープリント化し、TTL ウィンドウ内での同一リトライを自動ブロック
- **エポックによる権限追跡** -- 各コール時にどのコントローラがアクティブだったかを証明する単調カウンタ
- **純粋関数によるガバナンスゲート** -- ガバナンスの判定ロジックを I/O から分離し、不変条件を独立して検証可能に

`mcp-guardian` はフォークでも移植でもなく、Go によるゼロからの再実装です。しかし、アーキテクチャの設計図と、MCP ツールコールには単なるログではなくガバナンスが必要だという洞察は、Born14 氏の仕事から直接得たものです。Go と外部依存ゼロを選択したのはセキュリティ重視環境でのサプライチェーンセキュリティのためですが、「何を作るべきか」はすでに `@sovereign-labs/mcp-proxy` が答えを出していました。

`mcp-guardian` が有用だと感じたら、それを可能にした[オリジナルプロジェクト](https://github.com/Born14/mcp-proxy)にもぜひ Star をお願いします。
