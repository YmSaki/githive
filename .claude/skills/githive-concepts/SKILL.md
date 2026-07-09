---
name: githive-concepts
description: githive のイベントソーシング設計の背景知識（イベント封筒、fold、event-union マージ、refspace、entitychain）と、対応する docs・パッケージの対応表。internal/core や internal/app、event/fold/merge/ref 名まわりのコードを読んだり書いたりするときに参照する。
user-invocable: false
---

# githive の設計概念

githive は「エンティティ = 追記専用コミットチェーン」（イベントソーシング）で全データを管理する。この設計の核となる概念と、対応する設計書・実装パッケージの対応を以下にまとめる。作業対象のコードがこの表のどれに当たるかを先に特定してから読み書きすると、関係する不変条件（[[determinism]]）を見落としにくい。

## 概念 ↔ docs ↔ パッケージ対応表

| 概念 | 一言 | 設計書 | 実装 |
|------|------|--------|------|
| イベント封筒 | `{v, kind, id, ts, actor, entity, data}` の共通形式。すべての変更はイベントとして記録される | [docs/02-data-model.md](../../../docs/02-data-model.md)「イベント封筒」 | `internal/core/event`（`envelope.go`：型と検証、`canonical.go`：canonical JSON codec） |
| canonical JSON | 実体化ツリーの決定性を保証する JSON の正規形（キーソート、2 スペースインデント、LF） | [docs/02-data-model.md](../../../docs/02-data-model.md)「canonical JSON」 | `internal/core/event`（`Encode`/`EncodeString`/`EncodeCompactLine`/`DecodeGeneric`）唯一の実装。他は再利用のみ |
| fold | `state = fold(sort_by_event_id(events))`。イベント集合から現在状態を導く純粋関数 | [docs/02-data-model.md](../../../docs/02-data-model.md)「イベントの全順序と実体化」、[docs/14-testing.md](../../../docs/14-testing.md) | `internal/core/materialize`（`Registry`/`Reducer`、feature ごとの reducer ファイル） |
| checkpoint | 「読み出しの近道」。fold は常に無視する | [docs/03-sync-and-concurrency.md](../../../docs/03-sync-and-concurrency.md)「チェックポイント」 | `materialize.Registry.Fold` が `.checkpoint` サフィックスを汎用スキップ |
| event-union マージ | 分岐したチェーンをイベントの和集合 + 再 fold で自動収束させる。手動解決なし | [ADR-0004](../../../docs/adr/0004-merge-by-event-union.md)、[docs/03-sync-and-concurrency.md](../../../docs/03-sync-and-concurrency.md) | `internal/core/merge` |
| refspace | `refs/projects/<feature>/<id>` の ref 名前空間。ローカル作業名前空間とリモート追跡（`refs/githive-remote/*`）を分離する | [docs/02-data-model.md](../../../docs/02-data-model.md)「ref 名前空間」、[ADR-0006](../../../docs/adr/0006-ref-namespace.md)、[ADR-0008](../../../docs/adr/0008-remote-tracking-separation.md) | `internal/core/refspace` |
| entitychain | 「fold → ツリー化 → commit → CAS で ref 前進、競合時は再試行」の共通ループ。3 機能目（issue/task/chat/notify のうち 3 番目）が必要とした時点で抽出（[[go-layering]]） | [docs/03-sync-and-concurrency.md](../../../docs/03-sync-and-concurrency.md)「クラッシュ安全性とローカル競合」 | `internal/app/entitychain` |
| ULID | エンティティ ID・イベント ID。時刻順ソート可能、辞書順が全順序になる | [ADR-0005](../../../docs/adr/0005-ulid-entity-ids.md) | `internal/core/idgen` |
| identity（actor） | イベントの `actor` は git の `user.email` をそのまま使う。username への解決は表示時のみ（fold は不透明な文字列として扱う） | [ADR-0009](../../../docs/adr/0009-identity-user-email.md) | `internal/core/identity` |
| sync | fetch → merge（必要なら）→ push の再試行ループ。書き込みはローカル ref 更新までで完結し、ネットワーク失敗と分離される | [docs/03-sync-and-concurrency.md](../../../docs/03-sync-and-concurrency.md)、[docs/01-architecture.md](../../../docs/01-architecture.md)「データフロー」 | `internal/app/syncapp` |
| gitx | system git 呼び出し（fetch/push/credential/CAS ref 更新）。go-git はローカルのオブジェクト・ref 操作専用 | [ADR-0002](../../../docs/adr/0002-go-and-hybrid-git-access.md) | `internal/core/gitx` |
| sign | SSH 署名の付与・検証。actor（committer email）との一致を検証する | [ADR-0007](../../../docs/adr/0007-ssh-signing.md)、[docs/11-security.md](../../../docs/11-security.md) | `internal/core/sign`、`internal/app/verifyapp` |

## 層構造の要点（詳細は [[go-layering]]）

```
クライアント層（cmd/githive, TUI, MCP, VSCode）
  -> アプリケーション層 internal/app（issueapp/taskapp/chatapp/notifyapp/usersapp/wikiapp, syncapp）
    -> コア層 internal/core（event/chain/materialize/merge/refspace/sign/registry/gitx/identity/idgen）
      -> git 層（go-git ローカル、system git 通信）
```

`core` は `app` を import しない。`materialize`/`merge` は純粋関数、I/O は `chain` に隔離。

## 決定性の不変条件（詳細は [[determinism]]）

「同じイベント集合 → byte 単位で同一の実体化ツリー」。canonical JSON の一元化、fold の順序不変性（ID 昇順ソート後に畳み込み）、checkpoint 透過性の 3 つが柱。fold 意味論を変える変更は `event.SchemaVersion` の昇格が必須。

## spec/ との関係（詳細は [[spec-sync]]）

`spec/` は言語中立の実行可能スペック（Python リファレンス実装 + JSON ベクタ）。`internal/core/materialize` の Go reducer を追加・変更したら、同じコミット群で `spec/reference/fold_<kind>.py`・`spec/vectors/fold-<kind>/`・`spec/validate.py` の配線・Go golden テストを揃える（`/add-fold-vector` 参照）。
