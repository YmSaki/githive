# githive 実装ガイド（Claude Code / 実装エージェント向け）

このリポジトリは設計完了・実装未着手の状態にある。

## 最初に読むもの

1. docs/README.md（全体の目次と読む順序）
2. docs/13-roadmap.md（実装フェーズ。P0 から順に進め、フェーズを飛ばさない）
3. 着手フェーズの対象設計書（P0 なら docs/02、docs/03、docs/14）

## 絶対に守る不変条件

- 決定的実体化：同じイベント集合からは byte 単位で同一の実体化ツリーが得られること（docs/02）。
- fold 意味論を変える変更は schema_version の昇格を必須とする（docs/01）。
- canonical JSON の実装は 1 箇所に集約し、encoding/json を直接使わない（docs/14）。
- 改行は LF 固定。.gitattributes がこれを強制する。

## 実行可能スペック（spec/）

spec/ は言語中立の実行可能スペックである。
`python3 spec/validate.py` が全 PASS することを常に維持する。
Go 実装は spec/vectors/ のゴールデンベクタと出力が一致する golden テストを持つこと（canonical JSON は byte 一致、fold は構造一致）。
仕様の解釈に迷ったら docs/02 → spec/reference/ の順に正とする。

## 作法

- コミットは実装単位で分け、メッセージは日本語（例「core/event: canonical JSON encoder を実装」）。
- 設計と異なる実装が必要になったら、先に docs/adr/ に ADR を追加して理由を残す。
- テストなしのフェーズ完了は無い。各フェーズの exit criteria は docs/13-roadmap.md に定義済み。
