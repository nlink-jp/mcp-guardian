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

## 特徴

- 単一静的バイナリ（約6MB）、ランタイム依存なし
- Go 標準ライブラリのみ -- 外部モジュールゼロ
- stdio MITM プロキシ（クライアント・サーバー双方に透過的）
- ハッシュチェーンレシート台帳（JSONL、検証可能）
- 5段階ガバナンスパイプライン
- エージェント自己統治用の5つのメタツール注入
- セッション事後分析 CLI（view, verify, explain）
- Webhook 通知（汎用、Discord、Telegram）
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

### プロキシモード

```bash
mcp-guardian -- npx -y @modelcontextprotocol/server-filesystem /tmp

# オプション付き
mcp-guardian --enforcement advisory -- npx -y @modelcontextprotocol/server-filesystem /tmp
```

### 既存 MCP サーバーのラップ

```bash
# .mcp.json 内のサーバーをラップ
mcp-guardian --wrap filesystem

# 元に戻す
mcp-guardian --unwrap filesystem
```

### セッション事後分析

```bash
# レシートタイムライン表示
mcp-guardian --view
mcp-guardian --view --tool write_file --outcome error

# ハッシュチェーン整合性検証
mcp-guardian --verify

# セッション要約
mcp-guardian --explain
mcp-guardian --receipts
```

## CLI リファレンス

```
# プロキシモード
mcp-guardian [options] -- command [args...]

# オプション
--enforcement strict|advisory   実行モード（デフォルト: strict）
--schema off|warn|strict        スキーマ検証（デフォルト: warn）
--max-calls N                   バジェット上限（0 = 無制限）
--timeout ms                    上流タイムアウト（デフォルト: 300000）
--webhook url                   Webhook URL（複数指定可）
--state-dir dir                 状態ディレクトリ（デフォルト: .governance）

# 分析
--view                          レシートタイムライン
--verify                        ハッシュチェーン検証
--explain                       セッション説明
--receipts                      サマリー

# 統合
--wrap <server>                 .mcp.json にプロキシを挿入
--unwrap <server>               .mcp.json を元に戻す
--config <path>                 .mcp.json のパス

# 情報
--version                       バージョン表示
```

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

## アーキテクチャ

```
エージェント (Claude, GPT, etc.)
  | stdin/stdout (JSON-RPC 2.0)
mcp-guardian
  | stdin/stdout (JSON-RPC 2.0)
上流 MCP サーバー
```

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
make build       # dist/ にビルド
make install     # /usr/local/bin にインストール
make test        # テスト実行
make check       # lint + test
make clean       # ビルド成果物削除
make help        # 全ターゲット表示
```

## ライセンス

MIT License. Copyright (c) 2026 magifd2

## 謝辞

本プロジェクトは Born14 氏の [@sovereign-labs/mcp-proxy](https://github.com/Born14/mcp-proxy) に着想を得ています。オリジナルの Node.js 実装は MCP サーバー向けガバナンスプロキシの概念を確立しました。この Go 再実装は、外部依存ゼロの単一バイナリによるサプライチェーンセキュリティの強化を目指しています。
