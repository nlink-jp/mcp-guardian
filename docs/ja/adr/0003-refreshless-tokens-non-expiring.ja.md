# ADR 0003 — refresh トークンを伴わない OAuth トークンは、人為的な 1 時間期限を付けず「無期限」として扱う

- **ステータス:** Accepted
- **日付:** 2026-05-27
- **ドライバ:** `--login slack` が `refresh_token: ""` と 1 時間後の
  `expires_at` を保存し、その後プロキシがトークンを期限切れと報告する。
  だがローテーション無効で発行された Slack トークンは失効しない
- **一般化:** `refresh_token` も `expires_in` も返さず、無期限のアクセス
  トークンを発行するすべてのプロバイダ
- **関連:** ADR-0002（本 ADR はそのエラー経路の *誤った* トリガを除去する）

---

## コンテキスト

実際の Slack `--login` は次の `tokens.json` を生成する:

```json
{
  "access_token": "xoxp-…",
  "refresh_token": "",
  "token_type": "Bearer",
  "expires_at": 1779760685
}
```

`expires_at` はログイン時刻のちょうど **+1 時間**。この値は Slack が送って
きたものではなく、`--login` が付けたフォールバックである。

### Slack の実際の挙動

Slack は、アプリで**トークンローテーション**を明示的に有効化したときだけ
refresh トークンを発行する。ローテーション無効では、トークン交換レスポンス
に **`expires_in` も `refresh_token` も含まれず**、アクセストークン（ここ
では `xoxp-` ユーザトークン）は**失効しない**。したがって
`refresh_token: ""` はエラーではなく、正しく期待される状態である。

### 人為的期限がどう作られ、どう誤読されるか

1. `internal/cli/login.go` — レスポンスに `expires_in` が無い場合
   (`ExpiresIn <= 0`)、`expires_at = now + 1h` を保存する:

   ```go
   expiresAt := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Unix()
   if tokenResp.ExpiresIn <= 0 {
       expiresAt = time.Now().Add(1 * time.Hour).Unix()
   }
   ```

2. `internal/transport/authcode.go` — 1 時間後、`Token()` はトークンを
   期限切れと見なし、refresh トークンが無いため失敗する:

   ```go
   if p.tokens.RefreshToken == "" {
       return "", fmt.Errorf("access token expired and no refresh token available (run --login again)")
   }
   ```

正味の効果: **有効で無期限の Slack トークンが、ログインの 1 時間後に「死んだ」
扱いになり、1 時間ごとに不要な `--login` を強いられる。**

### ADR-0002 との関係

ADR-0002 はこの失敗を *fail fast* にした — プロキシはハングせず JSON-RPC
エラーをクライアントに返すようになった。しかし ADR-0002 は「トークンが本当に
期限切れになった」という前提で症状に対処していた。実際には期限切れではなく、
期限が人為的だった。ADR-0002 の不変条件（転送できないリクエストは必ず
JSON-RPC エラーを受け取る）は正しく、変更しない。本 ADR はその *誤った*
トリガを除去し、当該経路が**実際の失敗時のみ**発火するようにする。

## 決定

refresh 不能なトークンがまだ有効かどうかを判断できる権威は上流サーバだけで
あり、ローカルの時計はサーバが宣言しなかった期限を知り得ない。よって:

1. **サーバが `expires_in` を返さないときに 1 時間期限を捏造するのをやめる。**
   `--login` で `expires_at` を場合分けして計算する:
   - `expires_in > 0` → それに従う。
   - `expires_in` 無し **だが** `refresh_token` あり → 1 時間の探索値を保存
     （その前にプロバイダが静かに更新できる）。
   - `expires_in` 無し **かつ** `refresh_token` 無し → `0` を保存し「既知の
     期限なし」を意味させる。

2. **refresh トークンが無い場合、`Token()` は保存済み期限を無視してアクセス
   トークンをそのまま返す。** 更新手段が無い以上、ローカルの期限チェックは
   無意味である。実際の失効は上流の **401** として現れ、ADR-0002 に従って
   プロキシが JSON-RPC エラーへ変換する。アクセストークンが文字どおり空の
   ときのみエラーとする。

トークンローテーションは完全に維持される。`refresh_token` が存在する限り、
既存の期限チェック + 自動 refresh 経路は変更しない。

## 結果

- Slack（およびローテーション無効の任意のプロバイダ）は、一度 `--login`
  すれば無期限に動作する。1 時間ごとの再ログインは消える。
- **本変更前に書かれた既存 `tokens.json`** は人為的な +1h `expires_at` を
  持つ。新しい `Token()` は `refresh_token` が空のとき期限を無視するため、
  再ログイン不要で再び動作するようになる。
- **401 リトライ経路は引き続き正しく終了する。** 実際の 401 でプロキシは
  `Invalidate()` を呼んでアクセストークンをクリアし、リトライの `Token()`
  は `no access token available (run --login)` を返してプロキシがそれを
  surface する（ADR-0002 の不変条件が成立）。ループしない。
- リテラル `access token expired and no refresh token available (run
  --login again)` は no-refresh 経路から除去される。forward-failure テスト
  (`proxy_forward_failure_test.go`) はそのテキストをスタブ transport で
  注入しているので影響を受けないが、実プロバイダが人為的期限からこれを
  出すことは無くなる。

## スコープ外

- **Slack トークンローテーション対応。** ユーザがローテーションを有効化する
  と、ユーザトークンでは Slack が `refresh_token`/`expires_in` を
  `authed_user` 配下にネストして返す。`--login` は現状 top-level しか
  parse しない。`authed_user.{access_token,refresh_token,expires_in}` の
  配線は別変更とし、実際にローテーションが要求されたときに対応する。
- プロキシ起動時の認証状態の事前 surface（ADR-0002 から変更なし）。

## 実装

- `internal/cli/login.go` — `<= 0 → +1h` フォールバックを 3 分岐の
  `switch` に置換。`expires_in` 無し かつ `refresh_token` 無しのとき
  `expires_at = 0`。
- `internal/transport/authcode.go` — `Token()`: `RefreshToken == ""` の
  とき短絡（アクセストークンを返す。アクセストークンも空のときのみエラー）。
  不要になった「expired and no refresh token」分岐を削除。
- テスト:
  - `authcode_test.go` — refresh 無しトークンで過去/ゼロの `expires_at` が
    あっても、**トークンエンドポイントに接触せず**アクセストークンを返す
    （接触したらテストを失敗させるサーバを `TokenURL` に向ける）。アクセス
    トークンが空の refresh 無しトークンはエラーになる。
  - `login_test.go` — `expires_in` 無し・`refresh_token` 無しのレスポンスは
    `expires_at == 0` を保存。`refresh_token` あり・`expires_in` 無しは
    約 1h を保存。
- `README.md` / `README.ja.md` — Slack/手動セットアップ節に、ローテーション
  無効では Slack が無期限トークンと refresh トークン無しを返すのは想定どおり
  であり、mcp-guardian はそれを無期限に使い、実際の 401 のときだけ認証失敗を
  報告する旨を追記。
- `CHANGELOG.md` — `0.8.3` の Fixed エントリ。
- `AGENTS.md` — gotcha メモ。

検証: `make test`。手動 — `--login slack` を再実行し、`tokens.json` に
`expires_at: 0` が出ること、ログインの 1 時間以上後でも再ログインなしで
プロキシがリクエストを処理することを確認する。
