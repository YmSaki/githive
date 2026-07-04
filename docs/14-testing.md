# 14. テスト戦略

## 中核：決定性のプロパティテスト

このシステムの正しさは「同じイベント集合 → byte 同一の実体化ツリー」という不変条件（[02](02-data-model.md)）に集約される。
これをプロパティテストで常時検証する。

- **順序不変性**：ランダム生成したイベント列を、ランダムな順序・ランダムな分割でチェーンに投入し、最終ツリーのハッシュが常に一致すること。
- **マージの収束**：イベント集合を 2 分割して 2 つのチェーンを作り、双方向でマージした結果が一致すること（可換性）。3 分割で結合順を変えても一致すること（結合性）。同じマージの再実行が同一コミットツリーになること（冪等性）。
- **checkpoint 透過性**：チェックポイント有無で実体化結果が変わらないこと。

イベントのランダム生成器（各 kind の有効なペイロードを作る）は P0 で作り、全機能がジェネレータを追加していく。

## テストの層

| 層 | 対象 | 手段 |
|----|------|------|
| 単体 | event codec、reducer、policy 評価、ID 解決 | go test + golden ファイル |
| プロパティ | 決定性、マージ収束、canonical JSON の往復 | testing/quick または rapid |
| 結合 | chain + gitx + 実 git バイナリ | 一時ディレクトリに bare + clone x2 を作る test harness |
| E2E | CLI コマンドの入出力、終了コード | CLI をサブプロセス実行し JSON を検証 |
| 互換 | GitHub / GitLab / Gitea への push/fetch | 手動手順書 + オプトインの実機テスト（トークン必要） |

## 結合テストの標準シナリオ

test harness（`internal/testutil`）が次のシナリオを部品として提供する。

1. **同時更新の収束**：clone A と B が同じ issue に別イベントを書き、A push -> B push（拒否）-> B sync -> 双方の最終状態一致。
2. **notify 競合**：A と B が notify/stream に同時 post。sync 後に両方の通知が残る。
3. **shallow 読み**：depth 1 で fetch した clone で list/show が動き、履歴系が deepen を要求する。
4. **素の git 互換**：ツールで書いたデータを素の git コマンドだけで読む（各機能仕様の「素の git での読み方」を自動テスト化）。
5. **署名検証**：正しい署名、鍵不一致、revoke 後の署名、actor 偽装の 4 ケース（P3 以降）。
6. **チェーン破損の検出**：スキーマ違反イベント、非 canonical JSON、ツリー改竄を fsck / verify が報告する。
7. **自動 fetch 耐性**：未 push のローカルチェーンがある状態で素の `git fetch`（および `fetch --prune`）を実行し、`refs/projects/*` が変化しないこと（[ADR-0008](adr/0008-remote-tracking-separation.md) の追跡分離）。
8. **チェックポイント越しのマージ**：A のオフラインイベント（古い ULID）と B のチェックポイントを含むチェーンをマージし、A のイベントが実体化結果に残ること。
9. **祖先無しマージ**：独立に作られた同名 ref（meta/config）が空祖先で収束すること。registry の二重ルートが検証エラーになること。
10. **時計異常**：未来 ts のイベントが警告されること。ULID 生成器が時計巻き戻り下でも単調であること。
11. **ローカル同時書き込み**：2 プロセスが同一 ref へ同時に書き、CAS の敗者が再適用して両イベントが残ること。

## golden ファイル

- イベント封筒、各 kind のペイロード、実体化ツリー（tar 形式のスナップショット）を `testdata/golden/` に置く。
- golden の変更はスキーマ変更を意味するので、diff がレビューで見える形にする（バイナリ化しない）。
- この golden は将来 forge の pre-receive 検証（[12](12-forge-server.md)）と共有し、クライアントとサーバーの判定一致を保証する。

## CI 構成

- 3 OS（Linux / macOS / Windows）で全層を実行する。git のバージョンは最低要求（2.34）と最新の 2 系統。
- lint：gofmt、go vet、golangci-lint。encoding/json の直接使用禁止ルール（canonical JSON の一元化）を custom lint で強制する。
- 互換テスト（実サービス）は nightly のみ。トークンは CI シークレット。

## 性能の回帰検知

- ベンチマーク：1 万イベントのチェーン実体化、1000 ref の一覧、checkpoint 有無の差。
- `go test -bench` を nightly で回し、前回比 30% 超の劣化を fail とする。
- 性能目標（v0.1）：issue 1000 件のリポジトリで `issue list` が 200 ms 未満（checkpoint 済み、ローカル、キャッシュなし）。

## クライアント群のテスト

- MCP：ツール定義と CLI `--json` のスキーマ一致を自動比較する（[15](15-clients.md) の等価性をテストで固定する）。
- TUI：ロジックは app 層にあるためユニットで薄く、画面は golden スクリーンショット（teatest）を最小限。
- VSCode 拡張：CLI をモックした統合テスト。実バイナリ結合は smoke のみ。
