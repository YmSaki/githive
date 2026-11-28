# githive 開発規範（全エージェント共通）

この文書は、githive リポジトリで作業するすべての人間・AI エージェント（Claude Code、Codex、Gemini 等）に共通する判断基準である。
個別の実行手順・フック・スキル運用は [CLAUDE.md](CLAUDE.md) 等のツール固有文書に置くが、設計・実装の判断が衝突した場合は本書を優先する。

## 設計・実装の基本原則

1. **全体最適化**
   個別の Issue やタスクの達成よりも、システム全体の設計整合性・正しさ・保守性を優先する。変更量の少なさ、実装の容易さ、既存コードとの表面的な類似性だけを理由に実装方法を選ばない。
2. **根本原因の解決**
   問題の発生箇所と原因の所在を区別する。原因が別の層・コンポーネントの設計やデータ契約にある場合は、その原因を所有する側で解決する。不適切な設計を温存するための補償処理、場当たり的なフォールバック、状態変換の重ね掛けを追加しない（分散・並行処理・通信障害・外部入力に起因する本質的な障害処理は維持する）。
3. **責務の一意性**
   同じドメイン規則、不変条件、状態遷移、利用者操作の意味論を、複数の箇所で独立に決定しない。共通化の判断はコードの見た目や出現回数ではなく、責務と変更理由の同一性に基づく。
4. **データと状態の整合性**
   各データの正本、所有者、生成方法、更新条件、検証条件を明確にする（[docs/04-domain-model.md](docs/04-domain-model.md)）。派生データ、キャッシュ、閲覧用スナップショットを、根拠なく正本や検証済みデータとして扱わない。不存在・不明・不正・未検証・失敗・部分成功を区別し、情報の欠落を成功に変換しない。
5. **アーキテクチャの遵守**
   [docs/01-architecture.md](docs/01-architecture.md) に定義された依存方向と層内部の責務分担に従う。依存方向を守るだけで責務分担を守ったとみなさない。1 つの利用者操作の調停と部分結果の管理は `internal/app` が所有し、クライアント層（`cmd/githive`、MCP 等）でドメインロジックを再実装しない。
6. **設計変更**
   既存設計のままでは要求を素直に実現できない場合、無理に既存構造へ押し込まず、先に設計変更案（ADR 等）を出す（[docs/17-design-and-change-policy.md](docs/17-design-and-change-policy.md)）。未承認の設計変更（状態が「検討中」の ADR や未採用の監査提案）を確定事項として扱わない。
7. **影響範囲の確認**
   変更するファイルだけでなく、同じ責務を共有する機能、呼び出し元、状態の生成元、データの利用先、CLI/MCP 出力への影響を確認する。並列作業では、ファイルパスの重複だけでなく、同じドメイン規則・データ契約に触れる変更も競合とみなす。
8. **完了条件**
   Issue の受け入れ条件やテスト通過は完了の必要条件であり十分条件ではない。不変条件、ドメイン整合性、責務分担、既存機能との互換性が保たれていることをもって完了とする。

## 文書と仕様の権威順位

文書間で記述が食い違う場合は、上位から順に正とする。

1. **製品の目的・確定要求**：[docs/00-vision.md](docs/00-vision.md)
2. **承認された仕様・設計契約**：[docs/01-architecture.md](docs/01-architecture.md)〜[docs/04-domain-model.md](docs/04-domain-model.md)、[docs/10-cli-spec.md](docs/10-cli-spec.md)、[docs/11-security.md](docs/11-security.md)、`docs/features/*.md`
3. **採用済み ADR**：`docs/adr/*.md` のうち状態が「採用」のもの（既存仕様を変える場合は対象文書も同時に更新する）
4. **実行可能仕様・テストベクタ**：[spec/README.md](spec/README.md)、`spec/schemas/`、`spec/reference/`、`spec/vectors/`
5. **実装コード**：`cmd/`、`internal/`
6. **テスト結果・過去の監査記録**：`docs/audits/` 等の記録（未採用の提案を確定仕様とみなさない）

## 絶対に守る不変条件

- **決定的実体化**：同じ正当なイベント集合からは、誰がいつ計算しても byte 単位で同一の実体化ツリーが得られる（[docs/02-data-model.md](docs/02-data-model.md)）。
- **版管理**：fold 意味論を変える変更は、封筒 `v`（`event.SchemaVersion`）とリポジトリ `schema_version`（`initapp.SchemaVersion`）の両方の昇格を必須とする（[ADR-0018](docs/adr/0018-schema-version-bump-rule.md)）。
- **canonical JSON 一元化**：エンコード・デコード実装は `internal/core/event` に集約し、本体コードから `encoding/json` を直接 import しない（`scripts/check-canonical-json.sh`）。
- **改行コード**：LF 固定（`.gitattributes`）。

## 提出前の検証ゲート

変更の種類を問わず、完了報告・PR 作成の前に以下をすべて PASS させる。

- `go vet ./...`
- `go test ./...`（層依存テスト `internal/layering_test.go` を含む）
- `python spec/validate.py`
- `bash scripts/check-canonical-json.sh`（または Windows 上の同等確認）
- `gofmt -l .` に差分がないこと
