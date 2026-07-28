# githive 実行可能スペック

## これは何か

これは言語中立の実行可能スペックである。
docs/ の設計をコード化し、実装が従うべき形式的な基準を提供する。
Go 実装は、canonical JSON エンコードと fold（イベント畳み込み）の出力を、ここに含まれるベクタと一致させなければならない。
一致の検証は golden テストとして Go 側の CI に組み込む。
対象は issue・task・chat・notify・users の 5 feature 全て（docs/13-roadmap.md の実装フェーズに関わらず、実装済みの reducer は必ずここに反映する。`.claude/rules/spec-sync.md` の同期義務を参照）。

## 構成

```
spec/
  schemas/     JSON Schema（draft 2020-12）。イベント封筒と kind ごとの data 形式。
  reference/   Python 3.10 によるリファレンス実装。canonical JSON と fold。
  vectors/     ゴールデンテストベクタ。JSON ファイル群。
  validate.py  上記すべてを検証するスクリプト。
```

## schemas/ の内容

envelope.schema.json はイベント封筒（v, kind, id, ts, actor, entity, data）を定義する。
data の中身は kind ごとに別スキーマで検証する。
issue.schema.json、task.schema.json、notify.schema.json、users.schema.json、chat.schema.json がそれぞれの機能の kind を持つ。
各スキーマは additionalProperties を true にしている。
これは docs/02-data-model.md の「読み手は未知フィールドを無視し、書き換え時も保存する」という forward compatibility の規則に従うためである。

## reference/ の内容

canonical_json.py は docs/02-data-model.md の canonical JSON 規則を実装する。
キーを Unicode コードポイント順にソートする。
2 スペースインデントで整形する。
UTF-8、LF、末尾改行 1 つで出力する。
非 ASCII 文字はエスケープしない。

fold_issue.py・fold_task.py・fold_chat.py・fold_notify.py・fold_users.py は、それぞれ対応する docs/features/<kind>.md の fold 規則を実装する（internal/core/materialize の Go reducer からの逐行移植。Go が正）。
いずれもイベント列を受け取り、events_common.sort_events で id（ULID）の辞書順にソートしてから畳み込む。
checkpoint kind は常に無視する。
不正なステータス遷移イベントも無視する。
notify/users はシングルトンの追記専用ログ・台帳であり create イベントを持たないため、fold の戻り値の形が issue/task/chat（meta + 1 コレクション）とは異なる（fold_notify は meta/posts/acks の 3 つ、fold_users は単一の meta を持たず users/groups/policy の 3 つを返す）。

対応表：

| feature | reference | vectors | 検証対象の collection/値 |
|---------|-----------|---------|--------------------------|
| issue   | fold_issue.py  | fold-issue/  | meta, comments |
| task    | fold_task.py   | fold-task/   | meta, notes |
| chat    | fold_chat.py   | fold-chat/   | meta, messages |
| notify  | fold_notify.py | fold-notify/ | meta, posts, acks |
| users   | fold_users.py  | fold-users/  | users, groups, policy（meta 無し） |

## vectors/ の内容

canonical/ は入力値と canonical エンコード結果のペアを持つ。
ordering/ は ULID の辞書順ソートに関するケースを持つ。events_common.sort_events（の ID 抽出版）で再検証する。
fold-issue/・fold-task/・fold-chat/・fold-notify/・fold-users/ は、イベント列と期待される fold 結果（上の対応表を参照）のペアを持つ。
イベント順をシャッフルしても同じ結果になることを、これらのベクタで確認できる。

## validate.py の実行方法

Python 3.10 以上と jsonschema が必要である。
jsonschema は draft 2020-12 に対応したバージョン（4.18 以上）を使う。

```
pip install "jsonschema>=4.18" --break-system-packages
python3 spec/validate.py
```

実行すると、スキーマ検証・canonical 一致・fold 一致・ordering 一致の結果を PASS/FAIL で一覧表示する。
schema 検証は spec/vectors/fold-issue/・fold-task/・fold-chat/・fold-notify/・fold-users/ の全イベントを対象にするため、5 schema（issue/task/chat/notify/users）すべてが実イベントで検証される。
全件 PASS なら終了コード 0 を返す。
1 件でも FAIL があれば終了コード 1 を返す。

## Go 実装への要求

Go 実装は、このディレクトリのベクタを読み込んで同じ検証を行う golden テストを持つこと（internal/core/materialize/materialize_test.go）。
canonical JSON のエンコード結果は、spec/vectors/canonical/*.json の expected と byte 単位で一致すること。
fold の結果は、上の対応表のとおり spec/vectors/fold-<kind>/*.json の expected_* と構造として一致すること。
JSON の構造比較では、キーの順序ではなく値の等価性で比較してよい。
ただし canonical JSON エンコードの出力そのものは、キー順序を含めて byte 一致が必要である。
ULID の辞書順ソートは spec/vectors/ordering/*.json を internal/core/event.SortEvents（materialize.Registry.Fold が実際に呼ぶのと同じ関数）に通して検証すること（internal/core/event/envelope_test.go の TestOrderingVectors）。
