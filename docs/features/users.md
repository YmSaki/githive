# 機能仕様：users 管理

## 目的

3 つの役割を 1 つの台帳で担う。

1. ユーザーとグループの定義（issue / task / notify の対象として選択できるようにする）。
2. SSH 公開鍵の登録（コミット署名の検証に使う）。
3. 権限ルールの定義（forge モードでサーバーが push 制限に使う。hosted モードでは助言的）。

## ref とツリー

```
refs/projects/users/registry          # 単一 ref（registry 型）

tree:
  users/
    yuumiya.json
    dev-agent-01.json
  groups/
    core.json
  policy.json
```

### users/<name>.json

```json
{
  "username": "yuumiya",
  "display": "ゆうみや",
  "kind": "human",
  "emails": ["staroprog1103@gmail.com"],
  "notify_email": null,
  "keys": [
    {
      "pub": "ssh-ed25519 AAAA... yuumiya@main",
      "added_at": "2026-07-04T00:00:00.000Z",
      "revoked_at": null
    }
  ],
  "status": "active",
  "roles": ["admin"]
}
```

- **kind**：`human` または `agent`。Agent はそれを運用する人間とは別ユーザーとして登録し、専用の鍵を持たせる。表示と監査で区別できることが目的で、権限体系は共通。
- **status**：`active` / `suspended`。suspended のユーザーの新規イベントは検証で警告になる。
- **emails**：identity の正であり、「email → username・表示名」のマップを成す。イベントの `actor`（= git の `user.email`）はこの配列との突合で台帳ユーザーに解決される。複数登録可（端末ごとの使い分け）。到達可能な受信箱である必要はなく、email 形式の識別子であればよい（[ADR-0009](../adr/0009-identity-user-email.md) の追記）。
- **notify_email**：forge がメール配信に使う宛先（任意）。省略時は emails[] の先頭。noreply 系と `.invalid` ドメインは配信対象から除外される。
- ユーザー名は `[a-z0-9][a-z0-9-]{1,38}` に制限する（ファイル名と target 指定に安全な範囲）。

### groups/<name>.json

```json
{
  "name": "core",
  "members": ["yuumiya", "dev-agent-01"],
  "description": "コア開発"
}
```

グループ名の文字集合はユーザー名と同じ規則（`[a-z0-9][a-z0-9-]{1,38}`）とし、ユーザー名と同一の名前空間で一意とする（`user:` / `group:` プレフィックスで区別されるが、混同事故を避ける）。

### policy.json

```json
{
  "rules": [
    { "refs": "refs/projects/users/*",  "allow": ["role:admin"],  "actions": ["push"] },
    { "refs": "refs/projects/wiki/*",   "allow": ["group:core"],  "actions": ["push"] },
    { "refs": "refs/projects/**",       "allow": ["role:member"], "actions": ["push"] }
  ],
  "default": "deny"
}
```

- ルールは上から順に評価し、最初に一致したものを適用する。
- `default` が適用されるのは `refs/projects/**` の未一致に対してのみ。それ以外の名前空間（heads、tags、notes 等）の受け入れ可否は forge 設定の `namespace_mode`（[12](../12-forge-server.md)、既定 open）が決める。
- principal は `user:<name>`、`group:<name>`、`role:admin|member|reader` で指定する。
- forge モードでは、role:reader に `refs/projects/issue/**` と `refs/projects/chat/**` の push だけを許すルールを足すことで、「コードは読むだけ、議論には参加できる」外部貢献者を実現できる（hosted モードでは不可能な構成。[00](../00-vision.md) の既知の制約）。
- actions は `push`（通常の追記）、`create`（ref 新規作成）、`delete`（ref 削除）、`force`（非 fast-forward。event-union マージは通常 push であり force を要求しない）。

## イベント定義

registry 型 ref もイベントソーシングで管理する（形式は他機能と同じ）。

| kind | data | fold 規則 |
|------|------|-----------|
| users.user_set | username, fields | ユーザーの作成または上書き（LWW） |
| users.key_add | username, pub | 鍵を追加 |
| users.key_revoke | username, pub | 該当鍵の revoked_at を設定（削除しない） |
| users.group_set | name, members[], description? | グループの作成または置換 |
| users.group_remove | name | グループを削除 |
| users.policy_set | rules[], default | policy.json 全体を置換（LWW） |
| users.checkpoint | state, count, hash | 読み出し起点 |

policy は部分編集イベントにせず全置換とする。
ルールの順序が意味を持つため、部分編集の合併で順序が壊れることを避ける。

## 信頼の根（trust anchor）

台帳自身の正しさは次で担保する。

- registry チェーンの最初のコミットが**信頼の根**である。リポジトリ管理者がプロジェクト開始時に作り、自分の鍵で署名する。
- 以後の registry への変更は、その時点の台帳で `role:admin` を持つユーザーの有効な署名を要求する（検証規則。hosted では `githive verify` が、forge では pre-receive が確認する）。
- clone した側は、初回に信頼の根のコミットハッシュを out-of-band（README への記載等）で照合することが望ましい。照合を省くと first-use 信頼（TOFU）になる。

鍵のローテーションは key_add と key_revoke の組で行う。
revoke された鍵による「revoke 時刻より後の」署名は無効と判定する（過去の署名は有効なまま）。

## Agent のセットアップ

`githive users add <name> --agent` は次を一括で行う。

1. identity の自動鋳造。既定は `<name>@agents.<プロジェクト名>.invalid`（RFC 6761 により実在しないことが保証されたドメイン。`--email` で上書き可）。
2. Agent 専用の SSH 鍵ペアを生成し、公開鍵を台帳に登録する。
3. Agent の実行環境に貼る git config スニペット（`user.name` / `user.email` / `user.signingkey` / `commit.gpgsign`）を出力する。

人間と Agent が同じマシンを共有する場合も、Agent の実行環境（コンテナ、CI、エージェントセッション）には必ず専用の設定を与える。
設定を怠ると Agent のイベントが人間の identity で記録され、kind による監査上の区別が壊れる。
作業開始時に `githive whoami` で identity を確認する手順を、plugin 同梱スキル（[15](../15-clients.md)）の運用知識に含める。

## CLI

```
githive users add <name> [--display d] [--email e] [--agent] [--role r]
githive users key add <name> --pub <key or file>
githive users key revoke <name> --pub <key>
githive users group set <name> --member a --member b
githive users list [--json]
githive users policy edit          # $EDITOR で rules を編集し policy_set を発行
githive whoami                     # 自分のユーザー特定結果と鍵の状態を表示
```

## 素の git での読み方

```
git show refs/projects/users/registry:users/yuumiya.json
git show refs/projects/users/registry:policy.json
```

## 設計判断

- ユーザーごとに ref を分ける案（`refs/projects/users/<name>`）は退けた。グループと policy が単一ビューを要求すること、hosted モードでは ref 分割しても書き込み制限にならないことが理由である。forge モードの書き込み制御は policy ルールで registry ref 全体を admin に限定すれば足りる。
- メールアドレスは identity の突合（actor 解決）と通知配信に使う。本人性の証明は常に鍵で行い、email の自己申告は署名検証（actor と鍵の持ち主の一致、[11](../11-security.md)）で裏が取れる。
