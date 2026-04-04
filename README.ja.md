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
# --upstream フラグ使用
mcp-guardian --upstream "npx -y @modelcontextprotocol/server-filesystem /tmp"

# -- セパレータ使用
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

## ガバナンスパイプライン

全ての `tools/call` は5段階のゲートを通過します:

1. **バジェット** -- コール数が `--max-calls` を超えた場合拒否
2. **スキーマ** -- キャッシュされた `inputSchema` に対して引数を検証
3. **制約** -- ツール+ターゲットが過去の失敗にマッチする場合ブロック（TTL: 1時間）
4. **権限** -- セッションエポックが権限エポックと一致するか検証
5. **収束** -- ループ検知（同一失敗3回以上、同一ツール+ターゲット2分間で5回以上）

`strict` モードではゲート失敗時にコールをブロック。`advisory` モードでは違反をログに記録しつつ転送。

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
