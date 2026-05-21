# ADR 0001 — 事前登録済み OAuth2 Confidential Client への対応

- **Status:** Accepted
- **Date:** 2026-05-21
- **Driver:** Slack 公式 MCP サーバ (`https://mcp.slack.com/mcp`)
- **一般化対象:** RFC 7591 Dynamic Client Registration を実装してい
  ない認可サーバを持つすべての MCP サーバ — Slack、GitHub Apps、
  Microsoft Entra ID、多くのエンタープライズ SaaS。

---

## 背景

mcp-guardian の `--login` フロー（RFC 6749 authorization_code +
PKCE）は、上流 MCP サーバの認可サーバが RFC 7591 (Dynamic Client
Registration, DCR) の `registration_endpoint` を公開していることを
前提にしている。`internal/cli/discover.go:73` がログインのたびに
そこへ POST して `client_id` を都度発行している。

Slack 公式 MCP エンドポイントは明示的に DCR を**サポートしない**。
Slack のドキュメント:

> We do not support SSE-based connections or Dynamic Client
> Registration at this time. MCP clients must be backed by a
> registered Slack app with a fixed app ID and hardcode that app ID.

再現:

```
$ mcp-guardian --login slack
Discovering OAuth2 configuration from MCP server...
error: OAuth2 discovery failed: client registration:
       authorization server does not support dynamic client registration
```

これは Slack 固有の話ではない。GitHub Apps、Microsoft Entra ID、
Okta 管理のカスタムアプリなど主要な OAuth プロバイダの多くは同じ
「アプリを事前登録し、client_id を埋め込む」モデルを要求する。
汎用的に解決すれば、今後同じ経路に乗るすべてのプロバイダに恩恵
がある。

## 本 ADR 以前から既に動いていた部分

事前登録 client 経路の構成要素はほぼ揃っていた（file:line）:

| 機能 | 場所 |
|---|---|
| profile に `auth.oauth2` が明示されていれば discovery をスキップ | `internal/cli/login.go:52-74` |
| token exchange で `client_secret` 送出 | `internal/cli/login.go:191-193` |
| refresh request で `client_secret` 送出 | `internal/transport/authcode.go:146-148` |
| PKCE 常時有効 (S256) | `internal/cli/login.go:88-89,149-150` |
| authorize URL の `scopes` + `extraParams` | `internal/cli/login.go:138-143` |
| Streamable HTTP transport（内部名 `sse`） | `internal/transport/sse.go:14-23` |

したがって、`authorizeUrl` / `tokenUrl` / `clientId` /
`clientSecret` を含む `auth.oauth2` ブロックを明示した profile
であれば、原理的には discovery を経由せず code 交換まで動くはず
だった。

## 決定事項

`OAuth2Block` に 2 フィールド、CLI に 1 フラグ、内部に共有クライ
アント認証ヘルパーを追加する。特定されたすべてのプロバイダを解消
する最小サーフェスは次の通り:

### 1. 固定コールバックポート

`internal/cli/login.go:44` は `127.0.0.1:0` で listen しているため、
`--login` のたびに redirect_uri が変わる。事前登録 OAuth アプリは
プロバイダ側の許可リストとの **完全一致** を要求する。

RFC 8252 §7.3 は loopback redirect でのポート可変を推奨しており、
一部プロバイダはこれを受け入れる。Slack は受け入れない — アプリ
登録 UI が完全修飾 URI を要求する。GitHub、Microsoft Entra ID も
同様。

**決定:** `OAuth2Block` に `callbackPort` フィールドと、profile
値を上書きする `--callback-port` CLI フラグを追加する。CLI フラグ
を優先することで、profile を編集せずにコマンドラインから一時的に
別ポートに切り替えるデバッグ運用が可能になる。

### 2. トークンエンドポイントのクライアント認証方式

現状コードは `client_id` + `client_secret` をフォームパラメタとして
送信する（`client_secret_post`）。Slack を含む大多数で受け入れら
れる方式だが、Microsoft Entra ID や一部 Okta テナントは
HTTP Basic 認証（`client_secret_basic`）を要求する。オプトインがない
と、そうした認可サーバに対して 401 で失敗する。

**決定:** `OAuth2Block` に `clientAuthMethod` を追加（値は
`"post"` (default) / `"basic"` / `"none"`）。実装は
`applyClientAuth(req, form, cfg)` ヘルパー1本に集約し、token
exchange (`login.go`) と refresh (`authcode.go`) の両方から呼ぶ。

| `clientAuthMethod` | 挙動 |
|---|---|
| `post` (default、現行挙動) | `form.Set("client_id", id); if secret != "" { form.Set("client_secret", secret) }` |
| `basic` | `req.SetBasicAuth(id, secret)` を使い、form には client_id/secret を入れない。RFC 6749 §2.3.1 により、値は URL form-encode してから Basic ヘッダ用に percent-encode する。 |
| `none` | `form.Set("client_id", id)` のみ。secret は送らない。バリデーションで `clientSecret` 同時指定を禁止する。 |

### 3. DCR 失敗時のエラーメッセージ

`registration_endpoint` が無い（または registration POST が失敗
する）場合、現状ユーザは `authorization server does not support
dynamic client registration` だけを見せられ、手動経路の存在を知る
手がかりがない。

**決定:** ユーザ向けセットアップガイド（ADR §解決済み Open
Questions 第 3 項）へ誘導するメッセージに書き換え、ワークフロー
を発見可能にする。

### 4. HTTPS ループバックコールバック (2026-05-21 追記)

本 ADR 初稿は「特定したプロバイダはすべて
`http://127.0.0.1:<port>/callback` を受け入れる」前提で
「HTTPS loopback」を Non-goal に挙げていた。Slack に対する初回検証
でこれが誤りと判明した — Slack の OAuth アプリ登録 UI は
`https://` 以外の redirect URI をすべて拒否する。RFC 8252 §7.3 は
ループバックに `http://` を**推奨**するが、プロバイダ側に受け入
れ義務は課していない。

**決定:** `OAuth2Block` に `callbackScheme` を追加（値は
`"http"` (デフォルト、現行挙動) または `"https"`)。`"https"` を選
ぶと `--login` は:

1. これまで通り TCP リスナーを `127.0.0.1:<port>` に bind。
2. `generateLoopbackCert()` を呼び、`127.0.0.1`, `::1` の IP SAN
   と `localhost` の DNS SAN を含むエフェメラルな自己署名 ECDSA
   P-256 証明書を生成。証明書はメモリ内のみで保持しディスクには
   一切書かない。`--login` のたびに再生成する。
3. `tls.NewListener` でその証明書を使って TCP リスナーをラップ。
4. `redirect_uri` を `https://localhost:<port>/callback` として
   構築 (IP リテラルではなく DNS 名)。プロバイダのアプリ登録 UI
   によっては IP リテラルの redirect URI を弾くため、cert SAN で
   `localhost` までカバーする。
5. 「ブラウザに not-secure 警告が出ます」というメッセージを 1 行
   出して、ユーザを驚かせない。

ブラウザ警告は public CA を経由しない限り回避不能 — ループバック
IP に public 証明書は発行できない。脅威モデルは良性: ループバック
リスナーへの at-rest TLS に現実的な MITM はなく、証明書はプロセス
外に出ない。ユーザは `--login` のたびに 1 回クリックする。

実装: 証明書生成は `internal/cli/tlscert.go`、リスナーのラップは
`internal/cli/login.go`。

## 命名

レビュー後に確定:

- ループバックポートのフィールド名: **`callbackPort`**
  （却下: `loginPort`、`redirectPort`、`loopbackPort`）。
  根拠: フィールドは `auth.oauth2` 内に居るので、CLI サブコマンド
  名に紐づけるより OAuth 用語（"callback URL" ≒ "redirect URI"）
  の方が自然に読める。
- クライアント認証方式のフィールド名: **`clientAuthMethod`**
  （却下: `tokenAuthMethod`、`tokenEndpointAuthMethod`）。
  根拠: RFC 6749 ではこれを "client authentication" と呼ぶ。
  認証するのは *client* であり token ではない。
- CLI フラグ: **`--callback-port`**。フィールド名と対称。
- `clientAuthMethod` の値: `"post"` / `"basic"` / `"none"` —
  RFC 6749 §2.3.1 と OpenID Connect Core §9 の正規識別子
  (`client_secret_post`, `client_secret_basic`, `none`) に対応。

## スキーマ

`internal/config/profile.go`:

```go
type OAuth2Block struct {
    // ... 既存フィールド ...
    CallbackPort     int    `json:"callbackPort,omitempty"`     // 1-65535。--login コールバックの固定ループバックポート。0 / 未設定 = エフェメラル（現行動作）。プロバイダが redirect_uri の事前登録を要求するときに必要。
    CallbackScheme   string `json:"callbackScheme,omitempty"`   // "http" (デフォルト) または "https"。一部のプロバイダ (Slack) はアプリ登録時に http:// ループバック URI を拒否する。
    ClientAuthMethod string `json:"clientAuthMethod,omitempty"` // "post" (default), "basic", "none"。トークンエンドポイントへの client 認証情報の送り方。
}
```

`Profile.Validate()` に追加するバリデーション:

- `callbackPort`: 0 (未設定) または 1..65535。
- `callbackScheme`: 空 (= `http`) または `http|https` のいずれか。
- `clientAuthMethod`: 空 (= `post`) または `post|basic|none` のい
  ずれか。
- `clientAuthMethod=basic` は `clientSecret` を必須にする。
- `clientAuthMethod=none` は `clientSecret` 同時指定を拒否する
  （public client）。

既存フィールドのセマンティクスは変えない。新フィールドのない
profile はビット単位で同じ挙動を維持する。

## 後方互換性

すべての新フィールドはゼロ値が現行挙動を保つよう設計:

- `callbackPort = 0` → エフェメラルポート（現行デフォルト）。
- `clientAuthMethod = ""` → `"post"` 扱い（現行デフォルト）。
- DCR エラーメッセージ変更は観測可能だが互換破壊ではない。
- CLI フラグ `--callback-port` は追加のみ。既存の
  `mcp-guardian --login <name>` 呼び出しは引き続き動く。

## テスト計画

### ユニット

- `Profile.Validate` 新フィールド向け（有効/境界:
  `callbackPort` 0/1/65535/65536/-1; `clientAuthMethod`
  空/post/basic/none/junk; basic + secret なし; none + secret あり）。
- `applyClientAuth` テーブル駆動: 各 method × (secret あり/なし)。

### 結合 (httptest)

- profile の固定ポートで `--login` 実行: 実際にそのポートで
  listen し、redirect_uri がそれを反映していることを確認。
- `clientAuthMethod=basic` の token exchange: stub サーバが
  `Authorization: Basic …` を受け、form 内に client_secret が
  ないことを確認。
- `clientAuthMethod=basic` の token refresh: 同じ形状を refresh
  パスで再確認。

### 手動スモーク

- 実際の Slack アプリ登録 → 実 `--login slack` → 実
  `--inspect slack`。自動化はしないが、ユーザ向けセットアップ
  ガイドの受け入れ基準として明文化する。

## リスクと緩和

| リスク | 緩和 |
|---|---|
| 固定ポートが既に使用中で `Listen` 失敗 | 明確なエラーを出す: "port N in use; pick another in `callbackPort` or pass `--callback-port`"。 |
| ユーザが profile を git にコミットして `clientSecret` を漏洩 | Slack サンプルは `<replace-me>` プレースホルダ。セットアップガイドで明示警告。state directory はモード 0600、profile directory はユーザ umask。 |
| Slack が OAuth 仕様を変えた | 手動設定経路では Slack 専用コードを持たないので、影響は example profile の更新のみ。 |
| 同じプロバイダの複数 profile が `callbackPort` を取り合う | 各 profile が独自のフィールドを持つ。プロバイダ側は複数 redirect URI を登録可能（Slack も可）なので、profile ごとに別ポートを登録できる。 |

## 非ゴール

- OAuth アプリ登録の UI/TUI。プロバイダの web コンソールで登録
  する前提。
- `token_endpoint_auth_methods_supported` から
  `clientAuthMethod` を自動判定。DCR 未対応プロバイダはこの
  フィールドの publish が誤っている事例が多いので、明示指定を
  維持する。
- SSE transport の作り直し。mcp-guardian の `sse` は既に
  Streamable HTTP を喋っている (`sse.go:14-23`)。
- Slack 既知の `issuer` ミスマッチバグ
  (https://github.com/slackapi/slack-mcp-plugin/issues/7) への
  ワークアラウンド。手動経路は discovery を経由しないので影響
  しない。
- redirect_uri のパスが `/callback` 以外のサポート。Slack /
  GitHub / Microsoft Entra いずれもデフォルトパスを受け入れる。

(HTTPS ループバックは当初同じ前提のもとで Non-goal に挙げていた
が、Slack に対する初回検証で前提が誤りと判明したため、サポート
を追加した — §決定事項 §4 を参照。)

## 解決済み Open Questions

本 ADR の前身となる提案文書は 5 件の Open Question を抱えていた。
解決状況:

1. **スキーマフィールド名。** `callbackPort` と
   `clientAuthMethod`（ADR §命名 参照）。
2. **CLI フラグ名。** `--callback-port`（フィールド名と対称）。
3. **ドキュメント構造。** Org 内の他の新規プロジェクトと同様
   `docs/{en,ja}/{adr,reference,history}/` 三層レイアウトを採用
   する（`feedback_docs_structure_updated` 参照）。既存の
   `docs/architecture.md` と `docs/otlp-setup.md` は同じコミット
   で移行し、完全な日本語翻訳を添える。`scripts/docs-mirror-check.sh`
   が `en/ja` ミラー契約を強制する。
4. **Slack profile サンプル配置。** `examples/profiles/slack.json`、
   既存の `examples/profiles/atlassian.json` 慣行に合わせる。
5. **`clientAuthMethod` のスコープ。** 今回含める。実装の追加コ
   ストは小さい（ヘルパー 1 本 + 3 分岐）うえ、将来の GitHub /
   Microsoft Entra ユーザを具体的にブロック解除する。

## 関連

- `docs/en/reference/oauth2-manual-setup.md` — ユーザ向けセット
  アップガイド。実装着地後に書き起こすことで、設計時の想定では
  なく実挙動を反映させる。
- `examples/profiles/slack.json` — 実例 profile。
- https://docs.slack.dev/ai/slack-mcp-server/ — Slack MCP サーバ
  リファレンス。
- https://github.com/slackapi/slack-mcp-plugin/issues/7 — Slack
  discovery `issuer` ミスマッチの既知問題（手動経路では非該当）。
