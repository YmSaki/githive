---
paths: ["internal/core/**"]
---

# 決定性の不変条件（internal/core 配下で作業するとき）

このリポジトリの正しさは「同じイベント集合からは byte 単位で同一の実体化ツリーが得られる」（[docs/02-data-model.md](../../docs/02-data-model.md)「決定性の不変条件」）に集約される。`internal/core` はこの不変条件を守る最終防衛線であり、ここでの変更は特に慎重に行う。

## canonical JSON は internal/core/event に一元化する

- 唯一の実装は `internal/core/event/canonical.go`（`Encode` / `EncodeString` / `EncodeCompactLine` / `DecodeGeneric`）と `internal/core/event/envelope.go`（`Envelope.CanonicalJSON` / `Decode` / `FromMap`）である。
- githive が書く JSON（実体化ツリー、コミットメッセージ内のイベント JSON、notify の jsonl）はすべてここを経由する。`internal/cliout/cliout.go` の JSON 出力も独自実装を持たず `event.Encode` を再利用している（`internal/cliout/cliout.go:77`）。新しく JSON を書く箇所を追加するときも同様に、ここを呼ぶだけにする。
- `encoding/json` を `internal/core/event` の外（テストファイルを除く）から直接 import してはならない。この規則は `scripts/check-canonical-json.sh` が機械的に検証し、`.go` ファイル編集後の PostToolUse フックが自動実行する。フックが FAIL を報告したら、その場で `event.Encode`/`event.Decode` 経由に書き換える。
- 仕様の正は [docs/02-data-model.md](../../docs/02-data-model.md)「canonical JSON」節、Python 版リファレンス実装は `spec/reference/canonical_json.py`。Go とのズレは golden テスト（`spec/vectors/canonical/*.json` を読む Go 側テスト）が検出する。

## fold 意味論を変える変更は schema_version の昇格が必須

「fold 意味論を変える」とは、同じイベント集合から生成される実体化ツリーが変わる変更を指す（例：ステータス遷移表の追加・変更、フィールドの初期値変更、コレクションの畳み込み順序の変更）。フィールドの追加のみで既存イベント集合の実体化結果が変わらない変更は対象外。

昇格対象になったら、次を同一コミット群で行う。

1. `internal/core/event/envelope.go` の `SchemaVersion` 定数（現在 1）を上げる。
2. `refs/projects/meta/config` の `config.json` が持つ `schema_version`（[docs/02-data-model.md](../../docs/02-data-model.md)「meta/config ref」）との関係を確認する。ツールは自分の対応バージョンより新しい `schema_version` を見たら操作を拒否する規則（[docs/01-architecture.md](../../docs/01-architecture.md)「バージョニングと互換性」）を壊さないこと。
3. 変更前後の fold 結果が異なることを示す golden ベクタ（旧版で FAIL する／新版でのみ PASS するケース）を追加し、後方非互換を意図的なものとして記録する。
4. `docs/adr/` に ADR を追加し、なぜ意味論を変えたか・後方互換性への影響を残す（`/adr` スキル参照）。

未知の `v` は読み取り拒否が原則（forward compatible な無視ではなく拒否）。バージョンを上げずに意味論だけ変えると、新旧ツール混在チームで実体化ツリーが分岐し、`verify` が偽の改竄検出をする（[docs/13-roadmap.md](../../docs/13-roadmap.md)「手戻りリスクと先回りの手当て」）。

## チェックポイントは fold で常に無視する

`".checkpoint"` で終わる kind（例 `issue.checkpoint`）は、`internal/core/materialize/materialize.go` の `Registry.Fold` が汎用ルールとして常にスキップする（`materialize.go:68-70`）。個々の feature の reducer（`chat.go`、`notify.go`、`issue.go` 等）側で checkpoint kind を特別扱いするコードを書かないこと。汎用エンジンが既に処理している。

チェックポイントは「読み出しの近道」（[docs/03-sync-and-concurrency.md](../../docs/03-sync-and-concurrency.md)）であり、fold の結果に影響してはならない。新しい kind を追加するときも、この既存の汎用スキップに乗るだけでよい。checkpoint 透過性はプロパティテストで機械的に守る（下記）。

## 決定性テストの書き方

- **golden byte 一致**：canonical JSON の出力は `spec/vectors/canonical/*.json` の `expected` と byte 単位で一致させる。`event.EncodeString` の結果を直接文字列比較する。
- **fold 構造一致**：`spec/vectors/fold-*/*.json` の `expected_meta` / コレクション（`expected_comments`、`expected_notes` 等）と構造として一致すればよい（キー順序ではなく値の等価性）。`internal/core/materialize/materialize_test.go` の `runFoldVectorDir` ヘルパーが、両方を canonical JSON 化してから文字列比較する形でこれを実装している。単一コレクションの feature（issue/comments、task/notes、chat/messages、notify/posts）はこのヘルパーにそのまま乗せられる。複数コレクションを持つ feature（users の `users`/`groups`）は個別に両方を比較するテストを書く。
- **順序不変性**：同じイベント集合をシャッフルしても同一結果になることを、固定シードの `rand.New(rand.NewSource(N))` で複数トライアル（既存コードは 3〜20 回）検証する。`materialize_test.go` の `TestFoldOrderInvariance`、`chat_test.go` の `TestChatFoldOrderInvariance`（`canonicalStateSignature` ヘルパーで meta + 全コレクションを一つの canonical JSON にして比較）が実例。
- **checkpoint 透過性**：checkpoint イベントの有無で fold 結果が変わらないことを確認する。`materialize_test.go` の `TestFoldCheckpointTransparency`、`chat_test.go` の `TestChatCheckpointIgnored` が実例。

新しい kind / reducer を追加するときは、この 3 種類（golden、順序不変性、checkpoint 透過性）を必ず一緒に追加する。spec/ 側のベクタ配線が必要な場合は `/add-fold-vector` スキルの手順に従う（[[spec-sync]]）。

参照：[docs/02-data-model.md](../../docs/02-data-model.md)、[docs/14-testing.md](../../docs/14-testing.md)、[docs/01-architecture.md](../../docs/01-architecture.md)。
