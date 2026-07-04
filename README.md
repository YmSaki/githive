# githive

**git リポジトリの refs にプロジェクト記憶を保存するシステム。**

issue、タスク、通知、ユーザー情報、チャット、wiki を `refs/projects/` 配下に追記専用のコミットチェーンとして格納する。`git clone` した時点で、人間も AI Agent も専用ツールなしで同じ記憶を読める。

---

## なぜ作るのか

GitHub や GitLab のプロジェクト情報（issue、コメント、タスク）はサービスの DB に閉じており、clone してもコードしか手に入らない。AI Agent はリポジトリを clone した直後に文脈を持てず、各サービス固有の API・認証・レート制限に個別対応する必要がある。

githive はこの記憶を git そのものに移す。記憶は refs 配下のコミット履歴とテキストファイルとして保存され、fetch 設定 1 行だけで手元に揃う。

## 仕組み

`refs/projects/` 配下に機能ごとの ref を作り、各 ref を追記専用のコミットチェーンとして使う。

- **コミットメッセージ**：機械可読なイベント（canonical JSON）
- **コミットのツリー**：人間可読な現在状態（markdown + JSON ファイル）

この二重化により、`git log` でイベント履歴が、`git show <ref>:<path>` で現在状態が、専用ツールなしで読める。

## 設計原則

- **git ネイティブ**：外部 DB なし。すべて git オブジェクトとテキスト
- **ツールなしで読める**：素の git コマンドだけで全データを閲覧できる
- **オフラインファースト**：全操作はローカルで完結し、同期は fetch/push のみ
- **決定的実体化**：同じイベント集合からは、誰がいつ計算しても byte 単位で同じツリーが得られる
- **人間と Agent の対等**：`--json` 出力と通常出力を等価に提供。Agent 専用の裏口 API を作らない
- **既存ホスティング第一**：GitHub 等に refs を push するだけで中核が動く

## 動作モード

| 機能 | hosted モード（GitHub 等） | forge モード（自作サーバー） |
|------|--------------------------|---------------------------|
| issue / task / chat / wiki | 完全動作 | 完全動作 |
| notify の記録 | 完全動作（配信は fetch 時に検出） | push 時に配信 |
| 権限制御 | 助言的（ローカル・署名検証のみ） | 強制（pre-receive で拒否） |
| 署名検証 | クライアント側で `githive verify` | サーバー側で push 時に強制 |

## ロードマップ

**MVP（Horizon 1）は P0〜P2 + P5（MCP）。**
「Agent が clone 直後に issue を読み、task を進め、通知を残せる」体験を MCP 経由で最短に成立させる。

| フェーズ | 内容 |
|---------|------|
| P0 | コア機構（イベント封筒、canonical JSON、chain、materialize/merge） |
| P1 | issue 縦切り + sync |
| P2 | task / chat / notify |
| P5 | MCP サーバー（P2 直後に実施） |
| P3 | users / SSH 署名 / verify → v0.1 公開リリース |
| P4 | wiki / 運用コマンド |
| P6〜P11 | TUI、VSCode 拡張、forge、LFS、CI（需要駆動） |

## ドキュメント

設計は `docs/` 以下にある。番号順に読む。

| # | 内容 |
|---|------|
| [00](docs/00-vision.md) | ビジョン・原則・非目標・動作モード |
| [01](docs/01-architecture.md) | 全体構成・パッケージ設計・依存規則 |
| [02](docs/02-data-model.md) | ref 名前空間・イベント形式・実体化ツリー・ID |
| [03](docs/03-sync-and-concurrency.md) | fetch/push・競合解決・チェックポイント |
| [10](docs/10-cli-spec.md) | CLI コマンド仕様・JSON 出力・終了コード |
| [11](docs/11-security.md) | SSH 署名・権限モデル・脅威モデル |
| [13](docs/13-roadmap.md) | 実装順序・マイルストーン・MVP 定義 |
| [14](docs/14-testing.md) | テスト戦略 |
| [ADR](docs/adr/) | 主要な設計決定の記録 |

機能仕様は `docs/features/` 以下（issue / task / notify / users / chat / wiki）。

## 実行可能スペック

`spec/` に言語中立の実行可能スペックがある。

```sh
python3 spec/validate.py
```

全 PASS が常に維持される。Go 実装は `spec/vectors/` のゴールデンベクタと出力が一致すること。

## 制約

- hosted モードではリポジトリへの write 権限が必要（issue コメントも push のため）
- 書いた内容は事実上削除できない（イベント履歴は全 clone に複製され、改変は検証違反）
- 同期はリアルタイムではない（粒度は push/fetch）
- 想定規模は 1〜50 人程度のプロジェクト

## 実装言語

Go。
自作 GitForge サーバーも Go(+React) で実装予定。
