# githive 設計ドキュメント

githive は、git リポジトリの refs にプロジェクト記憶（issue、task、notify、users、chat、wiki）をテキストで保存するシステムである。
`git clone` した時点で、人間も AI Agent も専用ツールなしで同じ記憶を読める。
専用ツール（`githive` CLI とライブラリ）があれば、より速く読み書きできる。

## ドキュメント構成と読む順序

設計を理解するには番号順に読む。
実装を始める前に、少なくとも 00〜03 と対象機能の仕様、13（ロードマップ）を読むこと。

| # | ファイル | 内容 |
|---|---------|------|
| 00 | [00-vision.md](00-vision.md) | 目的、原則、非目標、動作モード |
| 01 | [01-architecture.md](01-architecture.md) | 全体構成、パッケージ設計、依存規則 |
| 02 | [02-data-model.md](02-data-model.md) | ref 名前空間、イベント形式、実体化ツリー、ID |
| 03 | [03-sync-and-concurrency.md](03-sync-and-concurrency.md) | fetch/push、競合解決、チェックポイント |
| - | [features/issue.md](features/issue.md) | issue 管理の仕様 |
| - | [features/task.md](features/task.md) | task 管理の仕様 |
| - | [features/notify.md](features/notify.md) | notify 管理の仕様 |
| - | [features/users.md](features/users.md) | users 管理（グループ、権限、公開鍵）の仕様 |
| - | [features/chat.md](features/chat.md) | chat 管理の仕様 |
| - | [features/wiki.md](features/wiki.md) | wiki 管理の仕様 |
| 10 | [10-cli-spec.md](10-cli-spec.md) | CLI コマンド仕様、JSON 出力、終了コード |
| 11 | [11-security.md](11-security.md) | SSH 署名、権限モデル、脅威モデル |
| 15 | [15-clients.md](15-clients.md) | 公式クライアント群（TUI、MCP、VSCode 拡張）と配布形態 |
| 12 | [12-forge-server.md](12-forge-server.md) | 自作 GitForge（LFS、WebHook、CI）の設計 |
| 13 | [13-roadmap.md](13-roadmap.md) | 実装順序、マイルストーン、MVP 定義 |
| 14 | [14-testing.md](14-testing.md) | テスト戦略 |
| ADR | [adr/](adr/) | 主要な設計決定の記録 |

## 確定済みの前提

- 実装言語は Go。将来の自作 GitForge サーバーも Go で書く。
- MVP は CLI とライブラリのみ。GitHub 等の既存ホスティングに refs を push するだけで動く。
- 自作サーバーは「git bare リポジトリ + α」の構成とし、後続フェーズで実装する。
- ref 名前空間は `refs/projects/` に統一する（[ADR-0006](adr/0006-ref-namespace.md)）。

## 実装者（AI Agent）への指示

- 実装順序は [13-roadmap.md](13-roadmap.md) のフェーズ定義に従う。フェーズを飛ばさない。
- データ形式（イベント封筒、canonical JSON、ツリー配置）は 02 の定義が唯一の正とする。機能仕様と食い違いを見つけたら、02 を正として修正を提案する。
- 決定的実体化（同じイベント集合から同じ状態が得られること）はシステム全体の不変条件である。これを壊す変更は行わない。
- 設計変更が必要になったら、コードを変える前に ADR を追加して理由を残す。
