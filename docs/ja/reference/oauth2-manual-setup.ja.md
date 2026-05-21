# OAuth2 手動セットアップガイド

本ガイドは、認可サーバが RFC 7591 Dynamic Client Registration (DCR)
を**サポートしない** MCP サーバに対して mcp-guardian を設定する手順
を扱う。ユーザがプロバイダ側で OAuth アプリを事前登録し、得られた
認証情報を profile に書き込む流れになる。

`mcp-guardian --login <profile>` で次のエラーが出る場合、本ガイドの
対象である:

> authorization server does not support dynamic client registration

Slack, GitHub Apps, Microsoft Entra ID、および多くのエンタープライズ
SaaS プロバイダがこのカテゴリに該当する。

設計判断と背景は
[`docs/ja/adr/0001-pre-registered-oauth-client.ja.md`](../adr/0001-pre-registered-oauth-client.ja.md)
に記録されている。

## いつ手動経路を使うか

次のいずれかが該当する場合は手動経路:

- `--login` が前述の DCR エラーで失敗する。
- プロバイダの開発者ドキュメントに「アプリを登録して client_id をコピー
  してください」と明記されている。
- プロバイダ側のアプリ登録 UI が固定 `redirect_uri` を要求する。
- トークンエンドポイントが HTTP Basic 認証を要求する
  (Microsoft Entra ID、一部 Okta テナント)。

プロバイダが DCR をサポートしているなら、最小限の `upstream` ブロッ
クだけを持つ profile を `mcp-guardian --login <profile>` で起動すれば
discovery が自動処理する。

## 手動セットアップ 5 ステップ

### 1. コールバックポートを決める

1024-65535 の空き TCP ポートを 1 つ選ぶ。被りにくい「高め」のポート
を取るのが一般的で、本ガイドの実例では `43117` を使う。これが
redirect URI のポートになる:

```
http://127.0.0.1:43117/callback        (多くのプロバイダ)
https://localhost:43117/callback       (Slack 等 http:// redirect を拒否するプロバイダ — §HTTPS コールバック 参照)
```

### 2. プロバイダで OAuth アプリを登録する

各プロバイダに開発者コンソールがある。一般的な場所:

| プロバイダ | 登録場所 |
|---|---|
| Slack MCP | https://api.slack.com/apps → Create New App → scope 設定 → ワークスペースにインストール |
| GitHub Apps | https://github.com/settings/apps → New GitHub App |
| Microsoft Entra ID | Azure Portal → Microsoft Entra ID → App registrations → New registration |

どのプロバイダでも 3 つの設定が肝:

1. **Redirect URI** — ステップ 1 のループバック URI を完全一致で貼り付
   け (`http://127.0.0.1:43117/callback`)。1 バイトでも違うと弾かれる。
2. **Scopes** — 最低でも MCP サーバが必須としているもの。Slack は
   https://docs.slack.dev/ai/slack-mcp-server/ にツールごとの必要スコー
   プを公開している。
3. **App type** — MCP サーバが受け入れる種別を選ぶ。Slack MCP は
   ディレクトリ公開アプリまたは Internal アプリのみ受け入れる。

登録が完了するとプロバイダから次が得られる:

- `client_id` — 公開識別子 (private リポジトリならコミットしても安全
  だが、公開リポジトリでは secret 扱いが安全)。
- `client_secret` — 機密トークン (**絶対にコミットしない**。§セキュリ
  ティ 参照)。

### 3. profile を書く

`~/.config/mcp-guardian/profiles/<name>.json` を作成する。Slack 向け
の実例が `examples/profiles/slack.json` に同梱されているのでコピーし
て編集:

```json
{
  "name": "slack",
  "upstream": {
    "transport": "sse",
    "url": "https://mcp.slack.com/mcp"
  },
  "auth": {
    "oauth2": {
      "flow": "authorization_code",
      "authorizeUrl": "https://slack.com/oauth/v2_user/authorize",
      "tokenUrl":     "https://slack.com/api/oauth.v2.user.access",
      "clientId":     "<your Slack app's client_id>",
      "clientSecret": "<your Slack app's client_secret>",
      "callbackPort": 43117,
      "callbackScheme": "https",
      "clientAuthMethod": "post",
      "scopes": ["chat:write", "channels:history", "search:read"]
    }
  },
  "governance": {
    "enforcement": "strict"
  }
}
```

フィールドごとの解説:

| フィールド | 必須? | 注意 |
|---|---|---|
| `flow` | yes | ブラウザ経由のユーザ OAuth では必ず `"authorization_code"`。 |
| `authorizeUrl` | yes | プロバイダの認可エンドポイント。Slack: `https://slack.com/oauth/v2_user/authorize` (`v2` ではなく `v2_user`)。 |
| `tokenUrl` | yes | プロバイダのトークン交換エンドポイント。Slack: `https://slack.com/api/oauth.v2.user.access`。 |
| `clientId` | yes | 登録アプリの値。 |
| `clientSecret` | 通常 yes | Confidential client のみ。PKCE 専用 public client のときは省略し `clientAuthMethod: "none"` を使う。 |
| `callbackPort` | 手動経路では yes | プロバイダ側に登録した redirect URI のポートと一致させる。0 / 省略 = エフェメラル (DCR 対応プロバイダ向けの旧挙動)。 |
| `callbackScheme` | プロバイダ次第 | `"http"` (デフォルト — RFC 8252 §7.3 のループバック推奨、多くのプロバイダで受け入れ) または `"https"` (Slack 等 `http://` redirect を拒否するプロバイダ向け)。§HTTPS コールバック を参照。 |
| `clientAuthMethod` | no | デフォルト `"post"`。§clientAuthMethod の選び方 を参照。 |
| `scopes` | プロバイダ次第 | authorize URL に space 区切りで載る。プロバイダによっては尊重、別のプロバイダでは登録済み scope で上書き。 |
| `extraParams` | no | プロバイダ固有の追加クエリパラメータ (例: `audience`, `prompt=consent`)。 |

### 4. `--login` を実行

```sh
mcp-guardian --login slack
```

挙動:

1. mcp-guardian が `127.0.0.1:43117` (設定したコールバックポート) に
   bind。
2. ブラウザが開き、プロバイダの authorize URL に `redirect_uri` =
   `http://127.0.0.1:43117/callback` と PKCE challenge 付きで遷移。
3. ブラウザでプロバイダに認証。
4. プロバイダが authorization code 付きで mcp-guardian にリダイレクト
   バック。
5. mcp-guardian が設定された `clientAuthMethod` に従ってトークンエン
   ドポイントで code を交換。
6. アクセストークン + リフレッシュトークンを
   `~/.config/mcp-guardian/state/slack/tokens.json` にモード 0600 で
   保存。

profile を編集せずポートだけ一時的に上書きしたいとき (例: ポート競合
のデバッグ):

```sh
mcp-guardian --login slack --callback-port 43118
```

CLI フラグが profile 値に勝つ。

### 5. 動作確認

プロキシを起動して MCP サーバのツール一覧を確認:

```sh
mcp-guardian --inspect slack
```

プロバイダの MCP ツールが見えるはず。アクセストークンは期限切れ時に
保存済みリフレッシュトークンで自動更新される。

## HTTPS コールバック

プロバイダ (特に **Slack**) によっては、アプリ登録時に `https://`
以外の redirect URI を一切拒否する。RFC 8252 §7.3 はループバック
に `http://` を**推奨**するが、プロバイダ側に受け入れ義務はない。

profile に `"callbackScheme": "https"` を設定すると `--login` は:

1. `127.0.0.1`, `::1`, DNS `localhost` をカバーするエフェメラルな
   自己署名 ECDSA 証明書を生成 (有効期限 1 時間、メモリ内のみ、
   ディスクには書かない)。
2. TCP コールバックリスナーをその証明書で TLS ラップする。
3. `https://localhost:<port>/callback` を使う (IP リテラルではな
   く DNS 名 — プロバイダの UI によっては IP リテラルの redirect
   URI を弾く)。

プロバイダ側にもこの URI を登録する:

```
https://localhost:43117/callback
```

**ブラウザ警告は想定内**。`https://localhost:43117/...` を初め
て開くと、証明書が自己署名のため「保護されていない通信」「接続は
プライベートではありません」が出る。「詳細設定」→「localhost に
アクセスする (安全ではありません)」をクリック。接続はループバック
限定で証明書はプロセス外に出ない。現実的な MITM リスクはない。

警告自体を回避したい場合は、プロバイダが `http://` を受け入れるか
確認する (多くは受け入れる)。その経路では証明書すら不要。

## `clientAuthMethod` の選び方

| 方式 | いつ使う | 何が送られる |
|---|---|---|
| `post` (デフォルト) | Slack 含む多くのプロバイダ | `client_id`, `client_secret` を form body のパラメタとして送信 |
| `basic` | Microsoft Entra ID、一部 Okta | `Authorization: Basic <base64(client_id:client_secret)>` ヘッダ。form body には認証情報を入れない |
| `none` | PKCE のみの public client (稀) | `client_id` のみ送信、secret は絶対に送らない。バリデーションで `clientSecret` 同時指定を禁止 |

どれが要るか不明なら、まず `post` で試す。HTTP 401 で
`client_authentication` 関連のエラーが返ったら `basic` に切替。

## プロバイダ別メモ

### Slack MCP

- **App type**: directory-published または Internal のみ。それ以外の
  third-party カスタムアプリは MCP エンドポイントを使えない。
- **redirect URI は `https://` 必須**: Slack のアプリ登録 UI は
  ループバックでも `http://` を弾く。profile に
  `"callbackScheme": "https"` を設定し、Slack アプリには
  `https://localhost:<callbackPort>/callback` を登録。`--login`
  時にブラウザに自己署名証明書の警告が出るのでクリックして進める
  — §HTTPS コールバック 参照。
- **OAuth エンドポイント**: `oauth.v2.access` ではなく
  `oauth.v2.user.access` を使う — MCP は bot トークンではなく *user*
  トークンを取得する。
- **既知の issuer ミスマッチ**:
  `https://mcp.slack.com/.well-known/oauth-authorization-server` の
  discovery メタデータは `issuer: "https://slack.com"` を返す。
  issuer を厳密検証する client しか影響を受けない。手動経路は
  discovery を経由しないので mcp-guardian には無関係。
- **scope リスト**: https://docs.slack.dev/ai/slack-mcp-server/ —
  必要スコープはツールごとに異なる。狭く始めて必要に応じて広げる。
- **Transport**: Slack MCP は Streamable HTTP のみ対応。
  mcp-guardian の `"transport": "sse"` は実態 Streamable HTTP
  (名前は旧仕様の名残) なので自然に噛み合う。

### GitHub Apps

- https://github.com/settings/apps → New GitHub App で登録。
- Callback URL に `http://127.0.0.1:<port>/callback` を設定。
- アプリ設定画面で client secret を生成。
- `clientAuthMethod`: `"post"` で動く。

### Microsoft Entra ID (旧 Azure AD)

- Azure Portal → Entra ID → App registrations → New registration。
- Public client/native (mobile & desktop) タイプの redirect URI と
  して `http://127.0.0.1:<port>/callback` を追加。
- Web プラットフォーム + client secret の構成では
  `clientAuthMethod: "basic"` を既定にするのが安全。

## セキュリティ

- `clientSecret` は **機密情報**。パスワードと同等に扱う。
- **実 secret を含む profile をパブリックリポジトリに絶対コミットし
  ない**。同梱の `examples/profiles/slack.json` が `<replace-...>` プ
  レースホルダを使っている理由はまさにこれ。
- profile は `~/.config/mcp-guardian/profiles/` に置く。ユーザの
  umask に従う。state directory
  (`~/.config/mcp-guardian/state/<profile>/`) は mcp-guardian がモード
  0700 で作成、`tokens.json` はモード 0600 で書き出す。
- どうしてもチーム共有目的で profile をコミットしたい場合は、
  `clientSecret` を別の `.env` または secret manager に置き、ラッパー
  スクリプトで環境変数展開する。

## リフレッシュと有効期限

`tokens.json` は `access_token`, `refresh_token`, `expires_at` を保
持する。各リクエストで mcp-guardian は:

1. `access_token` が有効 (expiry > now + 30 秒) ならそのまま使う。
2. そうでなければ設定された `clientAuthMethod` で `tokenUrl` に
   `grant_type=refresh_token` を POST。新しいトークンを保存。
3. リフレッシュトークンが期限切れまたは取り消されている場合、次のリ
   クエストはエラーを返すので `mcp-guardian --login <profile>` を再
   実行する。

## トラブルシューティング

| 症状 | 原因の候補 |
|---|---|
| `parse profile: json: unknown field "callbackSchema"` (等) | フィールド名のタイポ。profile 読込は strict なので即エラーになる。正しい名前は `callbackScheme` (`Schema` ではない — URL スキーム由来)、`callbackPort`、`clientAuthMethod`。 |
| `--login` が走るがブラウザの redirect URI が `http://…` のまま (`callbackScheme: "https"` を設定済みなのに) | フィールド名のタイポ (上記) または旧バージョンの mcp-guardian。`cat <profile>.json \| jq '.auth.oauth2.callbackScheme'` で確認。 |
| `start callback server on port N: bind: address already in use` | 別のプロセスがそのポートを掴んでいる。`lsof -i :N` で確認するか、別の `callbackPort` を選んでプロバイダ側 redirect URI リストを更新。 |
| `authorization error: invalid_redirect_uri` | mcp-guardian が送る `redirect_uri` がプロバイダの許可リストと一致しない。ポートが完全一致しているか確認。プロトコル (`http`)、ホスト (`127.0.0.1`)、パス (`/callback`) も寄与する。 |
| `client_authentication` 系で `token exchange failed (HTTP 401)` | `clientAuthMethod` を `"post"` ↔ `"basic"` で切り替えて試す。 |
| `token exchange failed (HTTP 400) invalid_grant` | authorization code は使い回し不可かつ短寿命。`--login` を再実行。client とプロバイダ間の時刻ずれでも起こる。 |
| プロキシ起動時に `no stored tokens found (run --login first)` | この profile の state directory に `tokens.json` がない。まず `--login` を実行。 |
| ブラウザが開くがコールバックが返らない | profile と provider 側登録でコールバックポートが一致しているか確認。firewall が 127.0.0.1 をブロックしていないかも要確認。 |

詳細診断にはグローバル config の `logLevel` を上げて再現させる。
