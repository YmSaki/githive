# 10. CLI 仕様

## 基本設計

- コマンド名は `githive`。cobra でサブコマンドを構成する。
- 人間向け出力と `--json` は同じ情報を持つ。Agent は `--json` を使う。
- すべての操作はローカル完結で成功し、同期の失敗は警告として分離報告する（[01](01-architecture.md)）。
- 書き込み系コマンドは既定で書き込み後に対象 ref を sync する。`--no-sync` で抑止。
- リポジトリの特定：`--repo <path>` またはカレントディレクトリから `.git` を探索。

## グローバルフラグ

| フラグ | 意味 |
|--------|------|
| --json | JSON 出力（後述の封筒形式） |
| --repo <path> | 対象リポジトリ |
| --no-sync | 書き込み後の自動 sync を行わない |
| --remote <name> | 同期先 remote（既定 origin） |
| --quiet / --verbose | 出力量の制御 |

## コマンド体系

```
githive init                     # fetch refspec 追加、meta/config 作成（無ければ）、初回 fetch
githive sync [--kind issue,...]  # fetch -> merge -> push（[03] のアルゴリズム）
githive status                   # 未 push の ref、未読 notify、自分の doing task の要約

githive issue   new|list|show|comment|status|edit|label|assign|link
githive task    new|list|show|status|note|reassign
githive notify  post|list|ack
githive chat    new|list|show|post|archive
githive users   add|list|key|group|policy
githive wiki    edit|save|show|log
githive whoami

githive verify [--ref <ref>] [--all]   # 署名とチェーン整合の検証（[11]）
githive fsck [--compact]               # スキーマ検証、チェックポイント作成
githive doctor                         # 環境診断：git バージョン、refspec 設定、時計のずれ
                                       # （最新リモートイベントとの比較）、identity 解決、署名設定
githive log [--since] [--actor]        # 全機能横断のイベントタイムライン

githive tui                            # TUI 起動（[15]）
githive mcp serve                      # MCP サーバー起動（[15]）
```

機能別サブコマンドの引数は各機能仕様に記載済み。

## JSON 出力の封筒

```json
{
  "ok": true,
  "data": { },
  "warnings": [ { "code": "sync_failed", "message": "..." } ]
}
```

失敗時：

```json
{
  "ok": false,
  "error": { "code": "conflict_retry_exhausted", "message": "...", "retryable": true }
}
```

- `data` の形は「コマンド名 + スキーマバージョン」で安定させ、破壊的変更をしない。
- 一覧系は `{ "items": [...], "total": n }` で統一する。
- ID は常に完全形（26 文字）で出力する。短縮は入力時のみ受け付ける。

## 終了コード

| コード | 意味 |
|--------|------|
| 0 | 成功（警告があっても成功は 0） |
| 1 | 一般エラー |
| 2 | 使い方の誤り（引数、未知フラグ） |
| 3 | 同期の競合再試行が尽きた（ローカルは保存済み、再実行可能） |
| 4 | 検証失敗（署名不正、スキーマ違反の検出） |
| 5 | 環境不備（git が古い、リポジトリでない、schema_version 非対応） |

## ID の入力解決

- 完全 ULID、先頭 8 文字以上の前方一致を受け付ける。
- 曖昧なら候補一覧を表示して終了コード 2。`--json` 時は candidates を error.data に入れる。
- `refs/projects/meta/counters`（forge の連番対応表、[12](12-forge-server.md)）が存在する場合は `#123` 形式も受け付け、一覧・詳細に番号を併記する。番号は別名であり、ID（ULID）が常に正である。

## 設定（git config）

| キー | 既定 | 意味 |
|------|------|------|
| githive.user | （なし） | 台帳上の自分のユーザー名。未設定時は鍵から逆引き |
| githive.sign | auto | always / auto（鍵設定済みなら署名） / never |
| githive.notify.auto | true | 操作に伴う自動 notify.post（[features/notify.md](features/notify.md)） |
| githive.sync.retries | 5 | push 競合の再試行回数 |

## 出力例

```
$ githive issue list --status open
ID        STATUS       TITLE                         ASSIGNEE   UPDATED
01j8xq4d  in_progress  sync が Windows でパスを壊す    yuumiya    2h ago
01j8xp1a  open         TUI の配色                     -          1d ago

$ githive issue show 01j8xq4d --json
{ "ok": true, "data": { "meta": { ... }, "body": "...", "comments": [ ... ] } }
```

## ライブラリとしての利用

`internal/app` の各サービスが公開 API の実体である。
将来、外部利用向けに `pkg/githive` として安定化する（v1.0 まで internal のまま。API 凍結を急がない）。
TUI と MCP サーバーは同一プロセス内で `app` を直接呼ぶため、CLI の JSON 層を経由しない。
