# 02. データモデル

このドキュメントがデータ形式の唯一の正である。
機能仕様（features/）と矛盾があれば、こちらを正とする。

## ref 名前空間

すべてのデータは `refs/projects/` 配下に置く（[ADR-0006](adr/0006-ref-namespace.md)）。

```
refs/projects/meta/config          # プロジェクト共有設定（単一 ref）
refs/projects/issue/<id>           # issue 1 件につき 1 ref
refs/projects/task/<id>            # task 1 件につき 1 ref
refs/projects/notify/stream        # 通知ストリーム（MVP は単一、将来シャード可）
refs/projects/users/registry       # ユーザー・グループ・権限の台帳（単一 ref）
refs/projects/chat/<id>            # chat スレッド 1 件につき 1 ref
refs/projects/wiki/main            # wiki 本体（ブランチ的に使う）
```

`<id>` は後述の ULID（小文字 26 文字）。
ref 名に使える文字はこれで自動的に git の制約を満たす。

### ローカル作業名前空間とリモート追跡の分離

`refs/projects/*` はローカルの作業コピーであり、githive（と規約を理解した人間）だけが更新する。
リモートの状態は追跡名前空間 `refs/githive-remote/*` に置く。
`githive init` は次を設定する。

```
git config --add remote.origin.fetch '+refs/projects/*:refs/githive-remote/*'
git config core.logAllRefUpdates always     # カスタム ref にも reflog を付け、事故復旧を可能にする
```

fetch 先をローカル作業名前空間と同じにしてはならない（[ADR-0008](adr/0008-remote-tracking-separation.md)）。
同じにすると、IDE の自動 fetch（refspec の `+` による強制更新）が未 push のローカルチェーンを上書きし、`fetch.prune = true` の利用者では未 push の新規 ref が削除される。
分離構成なら `git fetch` は追跡側だけを進め、ローカルへの反映は sync のマージ（[03](03-sync-and-concurrency.md)）が行う。

ツールを持たない読み手（読み取り専用）は、clone 後に次の一行で `refs/projects/*` を直接取得してよい。

```
git fetch origin '+refs/projects/*:refs/projects/*'
```

この直接 fetch はローカルで書き込みを行わない限り安全である。
書き込みも行う場合はツールを使うか、追跡分離を自分で維持する。

## エンティティ = 追記専用コミットチェーン

wiki を除く各 ref は、**イベントソーシング**で管理する（[ADR-0003](adr/0003-event-sourcing-commit-chain.md)）。
1 コミット = 1 イベントであり、コミットは次の二重構造を持つ。

- **コミットメッセージ**：1 行目に人間向け要約、空行を挟んで機械可読なイベント JSON。
- **コミットツリー**：そのイベントまでを適用した現在状態（実体化ツリー）。

```
commit 9f3ab2...
    issue.status: open -> in_progress

    {"v":1,"kind":"issue.status","id":"01j8xq4d3nbz9k7w2m5e8h1t6a", ...}

tree:
    meta.json
    body.md
    comments/
        01j8xq0c....md
```

この構造により、履歴は `git log`、現在状態は `git show <ref>:<path>` で読める。
また shallow fetch（`--depth 1`）でも先頭コミットのツリーに完全な現在状態が含まれる。

wiki だけは通常の git ブランチと同じ運用（自由なツリー編集、通常マージ）とする。
理由と詳細は [features/wiki.md](features/wiki.md) に置く。

## イベント封筒

すべてのイベントは共通の封筒を持つ。

```json
{
  "v": 1,
  "kind": "issue.comment",
  "id": "01j8xq4d3nbz9k7w2m5e8h1t6a",
  "ts": "2026-07-04T12:34:56.789Z",
  "actor": "yuumiya",
  "entity": "01j8x0a2b3c4d5e6f7g8h9j0ka",
  "data": { }
}
```

- **v**：封筒スキーマのバージョン。現在は 1。
- **kind**：`<機能>.<動詞>` 形式のイベント種別。正規表現 `^[a-z]+\.[a-z_]+$` に従う。各機能仕様で定義する。
- **id**：イベント ID。ULID。リポジトリ全体で一意。
- **ts**：発生時刻。RFC 3339、UTC、ミリ秒精度。ULID 内の時刻と同値を書く。
- **actor**：users 台帳上のユーザー名。文字集合はユーザー名規則（`[a-z0-9][a-z0-9-]{1,38}`）に常に従う。解決順は `githive.user` 設定、署名鍵からの台帳逆引き、の 2 段のみ。どちらでも解決できない場合はエラー（終了コード 5）とし、`githive.user` の設定を促す。git の `user.name` へのフォールバックは行わない（日本語名等が制約に反し、後から台帳と突合できない actor を作ってしまうため）。
- **entity**：対象エンティティの ID。ref 名から自明でも冗長に持つ（ログ単体で自己完結させるため）。
- **data**：kind ごとのペイロード。各機能仕様で定義する。

読み手は未知フィールドを無視し、書き換え時も保存する。

## canonical JSON

実体化ツリーの決定性（後述）のため、githive が書く JSON は次の正規形に固定する。

- キーは Unicode コードポイント順にソートする。
- 数値は最短表現、文字列は必要最小限のエスケープとする。
- インデントは 2 スペース、末尾に改行 1 つ。人間可読性を優先し、圧縮形は使わない。
- 文字コードは UTF-8、改行は LF。

コミットメッセージ内のイベント JSON も同じ正規形で書く。

## ID：ULID

エンティティ ID とイベント ID には **ULID** を使う（[ADR-0005](adr/0005-ulid-entity-ids.md)）。
表記は小文字 Crockford Base32 の 26 文字とする。

ULID は先頭 48 bit がミリ秒時刻なので、辞書順ソートがそのまま時刻順になる。
この性質を、イベントの全順序（後述）とファイル名の安定な並び（`comments/<ulid>.md` が自然に時系列順に並ぶ）の両方に使う。

中央の連番（issue #123 のような番号）は分散環境では発行できないため、v1 では持たない。
CLI は先頭 8 文字の短縮形（例 `01j8xq4d`）での指定を受け付け、曖昧なら候補を提示する。
forge モードでは連番の払い出しをサーバーが行える設計余地を残す（[12](12-forge-server.md)）。

## イベントの全順序と実体化

エンティティの状態は、そのチェーンに含まれる全イベントから次の純粋関数で決まる。

```
state = fold(sort_by_event_id(events))
```

- イベント ID（ULID）の辞書順を全順序とする。ほぼ時刻順であり、同時刻の衝突はランダム部で一意に決まる。
- fold の規則（各 kind が状態をどう変えるか）は機能仕様ごとに定義する。
- 基本則：スカラー値のフィールドは後のイベントが勝つ（last-write-wins）。集合（ラベル、担当者）は add/remove イベントの適用結果とする。コメント等の追記データは ID をキーに単純合併する。

時計が狂った端末のイベントは順序上ずれた位置に入るが、順序自体は全員で一致する。
正確な因果順序（hybrid logical clock 等）は複雑さに見合わないため採用しない（[ADR-0004](adr/0004-merge-by-event-union.md) の代替案の節）。

**決定性の不変条件**：同じイベント集合からは、byte 単位で同一の実体化ツリーが得られなければならない。
canonical JSON、ULID ファイル名、固定のツリー配置はすべてこの条件のためにある。
この不変条件はプロパティテストで常時検証する（[14](14-testing.md)）。

## 実体化ツリーの共通配置

各エンティティ ref のツリーは次の形を基本とする（詳細は機能仕様）。

```
meta.json           # 現在状態のスナップショット（状態機械の出力）
body.md             # 本文（ある機能のみ）
<コレクション>/      # 追記データ。ファイル名は <event-id>.md または .json
    01j8xq0c....md
```

コレクション内のファイルは、YAML front matter（author、ts、event id）+ markdown 本文の形式とする。
front matter は人間の閲覧と grep のためであり、機械処理はイベントログを正とする。

## コミットの規約

- 1 行目：`<kind>: <要約>`（例 `issue.comment: LGTM ですが一点だけ`）。72 文字で切り詰める。
- author / committer：git の設定値をそのまま使う。`actor` との対応付けは users 台帳と署名で行う。
- 親：通常は 1 親。競合解決のマージコミットのみ 2 親（[03](03-sync-and-concurrency.md)）。
- 署名：SSH 署名（`gpg.format ssh`）。hosted モードでは推奨、forge モードでは必須（[11](11-security.md)）。
- マージコミットのメッセージは `merge: event-union` とし、イベントを含まない。

## meta/config ref

`refs/projects/meta/config` は単一ファイル `config.json` を持つ registry 型 ref。

```json
{
  "schema_version": 1,
  "project": "githive",
  "features": ["issue", "task", "notify", "users", "chat", "wiki"],
  "created_at": "2026-07-04T00:00:00.000Z"
}
```

ツールは操作前に `schema_version` を確認し、自分の対応バージョンより新しければ操作を拒否する。

## サイズと成長の制約

- 1 イベントの JSON は 256 KiB を上限とする。超える本文は分割するか wiki へ誘導する。
- バイナリはツリーに置かない。添付は forge の LFS 対応まで対象外（[00](00-vision.md) の非目標）。
- チェーンの成長対策（チェックポイント）は [03](03-sync-and-concurrency.md) で定義する。

## 素の git での読み書き（互換路の保証）

読み取りは前述の `git log` / `git show` で足りる。
書き込みも、規約（このドキュメント）に従えば素の git で可能である。

```
# ツールなしで issue にコメントする例（原理の説明であり、通常は CLI を使う）
git worktree は不要。plumbing で行う:
  1. 現ツリーを取得し、comments/<新規ulid>.md を追加した tree を mktree で作る
  2. イベント JSON をメッセージにして commit-tree
  3. update-ref refs/projects/issue/<id>
```

この手順が煩雑であることが、専用ツールの存在理由である。
ツールは規約の実施者にすぎず、データに対する特権を持たない。
