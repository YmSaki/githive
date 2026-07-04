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
- メールアドレスは通知配信（forge モード）と表示にのみ使い、認証には使わない。認証は常に鍵で行う。
