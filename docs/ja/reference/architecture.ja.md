# mcp-guardian アーキテクチャ

本ドキュメントは、コントリビュータおよびセキュリティレビュア向けに mcp-guardian の内部アーキテクチャを記述する。

## システム概要

mcp-guardian は MCP (Model Context Protocol) サーバ向けの透過 MITM ガバナンスプロキシである。AI クライアントと MCP サーバの間を流れる全 JSON-RPC 2.0 メッセージをインターセプトし、ガバナンスチェックを適用したうえで、改竄検知可能な監査レシートを記録する。

```
                       mcp-guardian
                  +--------------------+
                  |                    |
Agent (Claude) -->| Agent    Transport |<-- Upstream MCP Server
   stdin/stdout   | Side     Layer     |    (stdio or HTTP/SSE)
                  |          |         |
                  |    +-----+-----+   |
                  |    | JSON-RPC  |   |
                  |    | Router    |   |
                  |    +-----+-----+   |
                  |          |         |
                  |    +-----+-----+   |
                  |    | Governance |   |
                  |    | Pipeline   |   |
                  |    +-----+-----+   |
                  |          |         |
                  |    +-----+-----+   |
                  |    |  Receipt  |   |
                  |    |  Ledger   |   |
                  |    +-----------+   |
                  +--------------------+
                       |         |
                  Webhooks   OTLP Export
```

## パッケージ構成

```
internal/
+-- transport/        # トランスポート抽象化レイヤ
|   +-- transport.go  #   Transport インターフェース
|   +-- process.go    #   stdio プロセストランスポート
|   +-- sse.go        #   HTTP/SSE クライアントトランスポート
|   +-- auth.go       #   OAuth2 client_credentials + コマンド系トークンプロバイダ
|   +-- authcode.go   #   OAuth2 authorization_code: 保存済みトークン + リフレッシュ
|
+-- export/           # テレメトリエクスポータインターフェース
|   +-- export.go     #   Exporter インターフェース (Export + Shutdown)
|
+-- proxy/            # コアプロキシループ
|   +-- proxy.go      #   メッセージルーティング、リクエスト-レスポンス対応付け
|   +-- splunkhec.go  #   Splunk HEC エクスポータドライバ
|
+-- jsonrpc/          # JSON-RPC 2.0 型定義とパース
|   +-- jsonrpc.go    #   Message, Parse, Marshal, ToolCallParams
|
+-- governance/       # 5 ゲートのガバナンスパイプライン (純粋関数)
|   +-- gates.go      #   RunGates() エントリーポイント
|   +-- budget.go     #   Gate 1: 呼出回数バジェット
|   +-- schema.go     #   Gate 2: JSON スキーマバリデーション
|   +-- constraint.go #   Gate 3: 学習済み失敗制約
|   +-- authority.go  #   Gate 4: エポック権限チェック
|   +-- convergence.go#   Gate 5: ループ/枯渇検知
|
+-- classify/         # ミューテーション分類
|   +-- mutation.go   #   読取専用 vs 変更系の分類
|   +-- target.go     #   引数からのターゲット抽出
|   +-- signature.go  #   エラーフィンガープリント
|
+-- metatool/         # 5 つのガバナンスメタツール
|   +-- metatool.go   #   エージェント自己ガバナンス用注入ツール
|
+-- receipt/          # SHA-256 ハッシュチェーン監査台帳
|   +-- receipt.go    #   Ledger: 追記専用ログ
|   +-- hash.go       #   ハッシュチェイニング
|   +-- types.go      #   Record 構造体
|
+-- state/            # .governance/ 状態ファイル
|   +-- state.go      #   ディレクトリ初期化
|   +-- authority.go  #   エポック管理
|   +-- constraints.go#   TTL ありの制約 CRUD
|   +-- controller.go #   UUID + セッショントラッキング
|   +-- intent.go     #   宣言されたエージェント目標
|   +-- atomic.go     #   アトミックファイル書込 (tmp+rename)
|
+-- config/           # 設定
|   +-- config.go     #   Config 構造体、バリデーション
|   +-- file.go       #   GlobalConfig, ServerConfig, JSON ロード
|
+-- webhook/          # 投げっぱなし HTTP 通知
+-- otlp/             # OTLP/HTTP エクスポータ (Logs + Traces)
+-- mask/             # ツール名 glob マッチング
+-- cli/              # セッション後 CLI コマンド
    +-- login.go      #   --login: OAuth2 authorization_code フロー
    +-- discover.go   #   MCP Authorization Discovery + Dynamic Client Registration
```

## トランスポートレイヤ

トランスポートレイヤは mcp-guardian と上流 MCP サーバ間の通信チャネルを抽象化する。

### Transport インターフェース

```go
type Transport interface {
    Send(data []byte) error
    ReadLine() ([]byte, bool)
    Close() error
}
```

すべてのトランスポートはこの最小インターフェースを実装する。プロキシコア (`proxy.go`) は具体のトランスポート型ではなくこのインターフェースのみに依存する。

### stdio トランスポート (ProcessTransport)

デフォルトのトランスポート。MCP サーバを子プロセスとして起動し、パイプ stdin/stdout で通信する。

```
mcp-guardian プロセス
    |
    +-- exec.Command(upstream, args...)
    |       |
    |       +-- stdin パイプ  --> Send()
    |       +-- stdout パイプ --> bufio.Scanner で ReadLine()
    |       +-- stderr パイプ --> os.Stderr へ転送
    |
    +-- プロセスライフサイクルはトランスポート管理
```

- 大きな JSON-RPC メッセージ用に 1MB スキャナバッファ
- シェル介さず直接バイナリ実行 (コマンドインジェクション防止)
- プロセスの stderr はプロキシの stderr へ転送してログ化

### SSE トランスポート (sseClientTransport)

MCP 仕様で定義された stdio の HTTP 代替である Streamable HTTP トランスポートを話す MCP サーバに接続する。

```
mcp-guardian
    |
    +-- HTTP POST  --> Send() (本体に JSON-RPC リクエスト)
    |
    +-- レスポンス処理:
    |   +-- Content-Type: application/json --> 単一レスポンス
    |   +-- Content-Type: text/event-stream --> SSE ストリーム
    |       +-- event: message --> JSON-RPC メッセージ
    |       +-- バッチ: JSON 配列 --> 個別メッセージに分割
    |
    +-- Mcp-Session-Id ヘッダ追跡
    +-- Close() 時に DELETE でセッション終了
```

主要な設計判断:

- **非同期 incoming チャネル**: SSE レスポンスは非同期で到着する。バッファ付きチャネル (`chan []byte`, 容量 64) が SSE パースとプロキシメッセージルーティングを疎結合化する。
- **混在レスポンスモード**: 同じエンドポイントが、リクエストによって JSON を返したり SSE を返したりする。両方を透過的に扱う。
- **セッションアフィニティ**: `Mcp-Session-Id` ヘッダを追跡し、全リクエストに付与する。

### 認証

SSE トランスポートは `TokenProvider` インターフェース経由で 3 種の認証メカニズムをサポートする。いずれも 401 時に自動的にトークンをリフレッシュする。

```
+-- TokenProvider インターフェース
    |
    +-- oauth2Provider (client_credentials grant)
    |   +-- token_url へ client_id + client_secret を POST
    |   +-- 有効期限 30 秒前までトークンキャッシュ
    |   +-- Invalidate() で再取得を強制
    |
    +-- storedTokenProvider (authorization_code フロー)
    |   +-- stateDir/tokens.json からトークン読込 (--login で書込)
    |   +-- 期限切れ時に refresh_token で自動更新
    |   +-- 更新後トークンをディスクに永続化
    |
    +-- commandProvider (外部コマンド)
        +-- exec.Command(token_command, args...)
        +-- 標準出力をトリムして Bearer トークンとして使用
        +-- 任意の TTL ベースキャッシュ
```

**MCP Authorization Discovery (明示的 OAuth2 設定なしの `--login`):**

profile に `auth` ブロックがない場合、`--login` がすべてを自動発見する:

```
mcp-guardian --login <profile>
  |
  +-- ローカルコールバックサーバをランダムポートで起動
  |
  +-- MCP サーバ URL に POST → 401
  |   +-- 試行: WWW-Authenticate ヘッダから resource_metadata
  |   +-- フォールバック: MCP ホストの .well-known/oauth-authorization-server
  |
  +-- Authorization Server Metadata (RFC 8414) 取得
  |   → authorization_endpoint, token_endpoint, registration_endpoint
  |
  +-- Dynamic Client Registration (RFC 7591)
  |   registration_endpoint へ POST:
  |     client_name: "mcp-guardian"
  |     redirect_uris: ["http://127.0.0.1:PORT/callback"]
  |     grant_types: ["authorization_code", "refresh_token"]
  |     token_endpoint_auth_method: "none"
  |   → client_id (自動生成)
  |
  +-- discovery を stateDir/oauth2-discovery.json に保存
  |
  +-- PKCE 付き Authorization Code フロー:
  |   +-- code_verifier + code_challenge (S256) 生成
  |   +-- ブラウザを開く: authorization_endpoint?client_id=...&code_challenge=...
  |   +-- ユーザがブラウザで認証
  |   +-- コールバックで authorization code を受信
  |   +-- token_endpoint で code をトークンに交換
  |
  +-- トークンを stateDir/tokens.json に保存 (mode 0600)
```

実行時には、プロキシは保存されたトークンと discovery キャッシュを読み込んでトークンリフレッシュに使う。profile に OAuth2 設定は不要。

**Authorization Code ログインフロー (明示的 OAuth2 設定):**

profile に `auth.oauth2` が明示されている場合、discovery はスキップされ、与えられたエンドポイントが直接使われる:

```
mcp-guardian --login <profile>
  |
  +-- ローカルコールバックサーバ起動
  +-- PKCE code_verifier + code_challenge (S256) 生成
  +-- ブラウザを開く: authorizeUrl?response_type=code&code_challenge=...
  +-- ユーザが認証 → コールバックで code 受信
  +-- tokenUrl で code をトークンに交換
  +-- トークンを stateDir/tokens.json に保存 (mode 0600)
```

**401 リトライフロー:**

```
Send(data)
  |
  +-- 現行トークンで doPost(data)
  |
  +-- 401? --yes--> Invalidate() でトークン無効化
  |                 新トークンで doPost(data) を再送
  |                   |
  |                   +-- もう一度 401? --> エラー (auth failed)
  |                   +-- 2xx --> handleResponse()
  |
  +-- 2xx --> handleResponse()
```

## メッセージフロー

### エージェント側 (下流)

プロキシは os.Stdin (エージェント) から読み込み、os.Stdout に書き込む。MCP クライアントがプロキシをサブプロセスとして起動するため、この経路は常に stdio。

```
os.Stdin --> bufio.Scanner --> jsonrpc.Parse() --> routeAgentMessage()
                                                       |
                                +----------------------+
                                |                      |
                          IsNotification()       IsRequest()
                                |                      |
                         そのまま転送         handleRequest()
                                                       |
                                +--------+--------+----+
                                |        |        |
                           initialize  tools/   tools/  other
                                       list     call
                                         |        |
                                    スキーマ    ガバナンス
                                    キャッシュ  パイプライン
                                    メタツール
                                    注入
```

### リクエスト-レスポンス対応付け

```go
pending map[string]chan *jsonrpc.Message  // id -> レスポンスチャネル
```

1. **送信**: `pending[id]` に `ch` を登録、上流へ転送
2. **受信**: `readUpstream()` ゴルーチンがレスポンスを pending チャネルにマッチング
3. **待機**: `forwardRequest()` が `select { ch, timeout }` でブロック
4. **クリーンアップ**: 受信またはタイムアウトでチャネルを pending から除去

Transport インターフェースがフレーミングを抽象化するため、stdio と SSE どちらのトランスポートでも同じ動作。

## ガバナンスパイプライン

すべての `tools/call` リクエストは 5 ゲートを順に通過する。ゲートはすべて **純粋関数** — 状態を入力に取り、結果を返すだけで副作用はない。

```go
func RunGates(input GateInput) GateResult
```

```
tools/call
    |
    v
+--------+     +--------+     +----------+     +---------+     +------------+
| Budget |---->| Schema |---->|Constraint|---->|Authority|---->|Convergence |
| Gate   |     | Gate   |     | Gate     |     | Gate    |     | Gate       |
+--------+     +--------+     +----------+     +---------+     +------------+
    |               |               |               |                |
  count >=        validate       学習済み         エポック         3+ 同じ
  maxCalls?       args vs        制約と          不一致?         失敗?
                  キャッシュ     マッチ                          2 分以内に
                  スキーマ       (TTL ベース)                    同 target が
                                                                 5+?
```

| Gate | ブロックされる条件 | モード依存 |
|------|-------------------|------------|
| Budget | `callCount >= maxCalls` | なし (設定済なら常時強制) |
| Schema | 引数が `inputSchema` 違反 | `strict` でブロック、`warn` でログのみ |
| Constraint | tool+target が学習済み失敗にマッチ | `strict` でブロック、`advisory` でログのみ |
| Authority | セッションエポック != 権限エポック | `strict` でブロック、`advisory` でログのみ |
| Convergence | ループまたは枯渇を検知 | シグナルを返すのみ、直接ブロックはしない |

## レシート台帳

すべての `tools/call` (転送、ブロック、エラーいずれも) が、`receipts.jsonl` に追記されるレシートを生成する。

```
レコード N:
  +-- タイムスタンプ
  +-- ToolName, Arguments, Target
  +-- MutationType (read/create/update/delete)
  +-- Outcome (success/error/blocked)
  +-- DurationMs
  +-- ConstraintCheck, AuthorityCheck
  +-- Hash = SHA-256(レコード N の内容 + レコード N-1 のハッシュ)
  +-- PreviousHash = レコード N-1 のハッシュ

レシートチェーン: R0 --> R1 --> R2 --> ... --> Rn
                  ^hash   ^hash   ^hash
```

過去のレシートを改変するとチェーンが破れる。これは `mcp-guardian --verify` で検出できる。

### 自動パージ

起動時、`maxReceiptAgeDays` (デフォルト 7) より古いレシートは削除される。残ったレコードは "genesis" から再チェーン化される。長期保持はテレメトリエクスポータ (OTLP、Splunk HEC) 側の責務。

### 末尾読込

`NewLedger()` は `receipts.jsonl` の最終行だけを読んで `seq` と `lastHash` を復元する。フルファイルスキャンを回避するため、起動はファイルサイズに依存せず O(1)。

## テレメトリエクスポータ

プロキシは `Exporter` インターフェース経由で複数のテレメトリバックエンドにレシートを配送する:

```go
type Exporter interface {
    Export(r *receipt.Record)
    Shutdown()
}
```

```
proxy.go
  |
  +-- []export.Exporter (複数ドライバを並行実行)
       |
       +-- otlp.Exporter         -> OTLP/HTTP (Logs + Traces)
       +-- splunkHECExporter     -> Splunk HEC (Events)
```

両ドライバはレコードをバッチし、サイズ閾値・タイマー・シャットダウンのいずれかでフラッシュする。エクスポート失敗時は stderr にログを出し、MCP トラフィックはブロックしない。

## 設定の優先度

```
デフォルト (ハードコード)
    |
    v
グローバル設定 (自動発見 or --config) -- テレメトリ、webhook、組織デフォルト
    |
    v
サーバ profile (--profile) -- upstream, auth, governance, mask
    |
    v
CLI フラグ (最優先) -- 常に勝つ
```

### 2 階層モデル

```
~/.config/mcp-guardian/
  config.json              <- システムグローバル (Layer 1)
  profiles/
    github-mcp.json        <- サーバ profile (Layer 2)
    filesystem.json
```

| Layer | スコープ | 内容 |
|-------|----------|------|
| システムグローバル | 環境につき 1 | OTLP、webhook、組織デフォルト |
| サーバ profile | MCP サーバにつき 1 | upstream, auth, governance, mask |

グローバル設定は `~/.config/mcp-guardian/config.json` から自動発見される。profile は `~/.config/mcp-guardian/profiles/` から名前で、またはパスで読み込まれる。

## データフロー図

OAuth2 付き SSE トランスポートで `tools/call` リクエストを処理する完全なデータフロー:

```
1. Agent が os.Stdin に書込:
   {"jsonrpc":"2.0","id":"42","method":"tools/call","params":{"name":"write_file",...}}

2. proxy.readAgent() が JSON-RPC をパース

3. routeAgentMessage() -> handleRequest() -> handleToolsCall()

4. ガバナンスパイプライン: RunGates() -> 全 5 ゲート通過

5. forwardRequest():
   a. pending["42"] = channel を登録
   b. upstream.Send(raw)  [Transport インターフェース]
      |
      SSE: doPost(data)
        +-- auth.Token() -> "Bearer eyJ..."  [OAuth2 client_credentials]
        +-- Bearer トークン付きで upstream URL に HTTP POST
        +-- レスポンス: 200 + Content-Type: text/event-stream
        +-- consumeSSEStream() -> SSE パース -> incoming チャネル

6. readUpstream() ゴルーチン:
   a. upstream.ReadLine()  [incoming チャネルから読込]
   b. jsonrpc.Parse() -> msg.IsResponse() -> pending["42"] にマッチ
   c. レスポンスをチャネルへ送信

7. forwardRequest() がチャネルからレスポンスを受信

8. recordReceipt() -> ハッシュチェーン付きで receipts.jsonl に追記
   +-- 設定されていれば otlp.Export()
   +-- ブロック時に webhook.Fire()

9. writeMessage() -> os.Stdout -> Agent がレスポンスを受信
```

## セキュリティ特性

| 特性 | 仕組み |
|------|--------|
| 外部依存ゼロ | `go.mod` の `require` 行が 0 |
| コマンドインジェクションなし | `exec.Command` を直接呼びシェル介さない |
| 改竄検知可能な監査 | SHA-256 ハッシュチェーン、オフライン検証可能 |
| アトミックな状態書込 | tmp ファイル + rename パターン |
| 認証情報の非露出 | 設定ファイル経由で `ps` から見えない |
| OAuth2 トークン隔離 | トークンはメモリ内キャッシュのみ、ディスクに書かない |
| セッション終了 | SSE セッションは close で DELETE 送信 |
| 401 リトライ上限 | 最大 1 回までで無限ループ防止 |

## 並行モデル

```
メインゴルーチン:
  proxy.readAgent() -- os.Stdin でブロッキングループ

バックグラウンドゴルーチン:
  proxy.readUpstream() -- Transport.ReadLine() でブロッキングループ
  io.Copy(stderr, process.Stderr) -- プロセストランスポート時のみ
  consumeSSEStream() -- SSE レスポンスごとに 1 つ (短命)
  otlp.Exporter バッチタイマー -- 定期フラッシュ

同期:
  sync.Mutex -- pending マップと SSE トランスポート状態を保護
  チャネル -- リクエスト-レスポンス対応付け (リクエストあたり 1-buffered)
  closed チャネル -- トランスポート終了シグナル
```
