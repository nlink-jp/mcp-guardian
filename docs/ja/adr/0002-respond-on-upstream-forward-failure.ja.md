# ADR 0002 — クライアントリクエストを上流へ転送できない場合は JSON-RPC エラーを返す

- **Status:** Accepted
- **Date:** 2026-05-26
- **Driver:** ログイン必須 MCP（例 Slack）の OAuth トークン期限切れ時に shell-agent-v2 が15秒ハングする
- **一般化:** クライアントリクエスト処理中の上流 send/connect/auth 失敗全般

---

## 背景

プロキシがクライアントの**リクエスト**（応答を期待する）を上流へ転送し、
その転送が失敗した場合、クライアントは応答を受け取れず自身のタイムアウトが
発火するまで待たされる。

再現（Slack MCP、`authorization_code` トークン期限切れ・refresh トークン無し）:

```
# 診断モードはエラーを表示して終了:
$ mcp-guardian --inspect --profile slack
mcp-guardian: OAuth2 authorization_code enabled (stored tokens)
error: send initialize: obtain auth token: access token expired and no refresh token available (run --login again)

# proxy モードは握り潰してハング:
$ mcp-guardian --profile slack
mcp-guardian: OAuth2 authorization_code enabled (stored tokens)
mcp-guardian: proxy started (controller=…, transport=sse)
# … クライアントが initialize を送るが応答が一切返らない
```

下流クライアント（shell-agent-v2）は stdout 読み取りで自身の15秒
guardian-start タイムアウトまでブロックし、最終的に理由不明の
`guardian start timed out after 15s` を表示 — ユーザは再ログインが必要だと
気づけない。

### 根本原因

`forwardRequest`（`internal/proxy/proxy.go`）は上流へ送信しリクエストごとの
チャネルで待つ。**上流タイムアウト**は既に JSON-RPC エラー*応答*へ変換している:

```go
case <-timer.C:
    …
    return jsonrpc.NewErrorResponse(msg.ID, -32603, "upstream timeout"), nil
```

しかし**上流 send 失敗**は応答無しで素のエラーを返す:

```go
if err := p.upstream.Send(raw); err != nil {
    …
    return nil, fmt.Errorf("send to upstream: %w", err)   // ← クライアントに応答しない
}
```

Slack のケースでは SSE トランスポートの `Send` が `obtain auth token`
（`internal/transport/sse.go:130` → `authcode.go:110`）を起動し、
`access token expired and no refresh token available (run --login again)`
を返す。このエラーは `handleInitialize` → `routeAgentMessage` →
`readAgent` へ伝播するが、`readAgent` はログするだけ:

```go
if err := p.routeAgentMessage(msg, line); err != nil {
    logStderr("mcp-guardian: route error: %v\n", err)   // ← ログのみ、クライアント未応答
}
```

つまり挙動が非対称: **上流タイムアウト→クライアントはエラー応答を得る;
上流 send/auth 失敗→クライアントがハング**。プロキシは他のクライアント可視な
失敗（tool-not-found、governance ブロック、internal error）では全て
`jsonrpc.NewErrorResponse` を返している。リクエスト転送失敗の経路だけ抜けている。

## 決定

不変条件を確立する: **ルーティングがエラーを返したクライアントリクエストは
常に JSON-RPC エラー応答を受け取る**。`readAgent` に集約実装する:
`routeAgentMessage` がエラーを返し、かつメッセージがリクエスト（id あり）なら、
元のメッセージを載せた JSON-RPC エラーを返す。通知（id 無し）は従来通りログのみ。

```go
if err := p.routeAgentMessage(msg, line); err != nil {
    logStderr("mcp-guardian: route error: %v\n", err)
    if msg.IsRequest() {
        _ = writeMessage(jsonrpc.NewErrorResponse(msg.ID, -32603, err.Error()))
    }
}
```

`-32603`（Internal error）はタイムアウト経路が既に使うコードと一致。
`err.Error()` は実行可能なテキスト
（`…access token expired … (run --login again)`）を既に含むので、クライアントは
本当の理由を surface できる。

### per-handler ではなく集約（readAgent）にする理由

全リクエスト経路は `readAgent` に集約される。かつ `forwardRequest` 以外で
上流に到達する経路もある（`tools/call` のパース不能パラメータ fallthrough と
未知メッセージ default はどちらも `p.upstream.Send(raw)` を直接呼ぶ）。
`readAgent` の単一キャッチで、現在・将来の全リクエスト転送エラーを一箇所で
カバーする。

二重応答の懸念なし: リクエストハンドラの監査により、応答を書いてから
エラーを返すものは無い — `forwardRequest` は `(resp, nil)`（呼び出し側が書く）
か `(nil, err)`（何も書かない）のいずれか; masked/governance/meta 経路は
`writeMessage(...)` して `nil` を返す。`readAgent` のキャッチは未応答で
伝播したエラーのみで発火する。

## 影響

- shell-agent-v2（および任意の MCP クライアント）は `initialize` で即座に
  JSON-RPC エラーを得てハングしない; その `call()` は既にこれを理由付きの
  高速失敗にマップし、MCP 設定に `…access token expired … (run --login again)`
  と表示される。15秒待ちも理由不明メッセージも両方解消。
- 任意の理由（auth/transport/connect）で転送失敗したリクエストが常に応答を
  得る — Slack/auth トリガを超えた堅牢性向上。
- 通知・成功リクエスト・既存の上流タイムアウト経路の挙動は不変。

## スコープ外

- 期限切れトークンの自動リフレッシュ/再ログイン（別フロー; メッセージが既に
  `--login` 実行を促す）。
- プロキシ起動時の認証状態の先行表示（プロキシは最初のリクエスト前に起動する;
  最初のリクエストを高速失敗させれば十分）。
- shell-agent-v2 側の変更 — 既に JSON-RPC エラーを高速 surface するので本 fix
  には不要。（並列 guardian spawn / start-timeout 短縮の backstop は*他の*
  ハング要因への別個の hardening として残る。）

## 実装

- `internal/proxy/proxy.go` — `readAgent`: `routeAgentMessage` がリクエストで
  エラーした場合に `jsonrpc.NewErrorResponse(msg.ID, -32603, err.Error())` を返す。
- `internal/proxy/proxy_test.go` — テスト: 上流 `Send` が失敗するリクエスト
  （send エラーを返すトランスポート stub を注入）が、出力無しではなく
  クライアントへの JSON-RPC エラー応答（id 一致・code -32603・元エラーを載せた
  メッセージ）を生む; 失敗する通知は応答を生まない。
- `README.md` / `README.ja.md` — トラブルシューティング/挙動セクションに、
  リクエストの上流/auth 失敗はクライアントへ JSON-RPC エラーを返す（例:
  トークン期限切れ→「run --login again」）と記す。

検証: `make test`; 手動 — shell-agent-v2 を期限切れトークンのプロファイルに
向け、15秒ハングでなく ~1秒で MCP 設定に再ログインメッセージが出ることを確認。
