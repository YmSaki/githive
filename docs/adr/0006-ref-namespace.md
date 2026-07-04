# ADR-0006: ref 名前空間を refs/projects/ に統一する

- 状態：採用
- 日付：2026-07-04

## 文脈

初期構想では `refs/projects/issue`、`refs/projects/task`、`refs/projects/notify`、`refs/projects/users/` と `refs/project/chat`（単数形）、`refs/project/wiki`（単数形）が混在していた。
また、単一 ref（notify、users、wiki）と子を持つ名前空間（issue/<id>）の使い分けに一貫性がなかった。

## 決定

全機能を `refs/projects/<kind>/<name>` の 2 階層に統一する。

```
refs/projects/meta/config
refs/projects/issue/<ulid>
refs/projects/task/<ulid>
refs/projects/notify/stream        # 将来 <yyyy-mm> シャードを並置可能
refs/projects/users/registry
refs/projects/chat/<ulid>
refs/projects/wiki/main            # 将来 draft 等を並置可能
```

## 理由

- 単数形と複数形の混在は、fetch refspec を 2 本にし、ツールと文書の全域に分岐を生む。
- git では `refs/projects/notify` という ref と `refs/projects/notify/xxx` という ref は共存できない（ディレクトリとファイルの衝突）。単一 ref の機能も `<kind>/<name>` 形式にしておけば、後からのシャード追加・並置が ref 名の変更なしにできる。
- `refs/projects/` は他ツールとの衝突が知られておらず、ホスティングの予約名前空間（refs/pull 等）とも重ならない。

## 帰結

- fetch refspec は 1 本のワイルドカード（`refs/projects/*` を左辺とする）で全機能を覆う。追跡先の分離は [ADR-0008](0008-remote-tracking-separation.md) を参照。
- 名前空間プレフィックスは v1 では固定とする。可変にする要求（1 リポジトリに複数プロジェクト等）が出たら、meta/config での宣言を検討する。
