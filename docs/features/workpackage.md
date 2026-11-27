# 機能仕様：workpackage 管理（通称・エイリアス: `wp`）

> **ステータス**: Draft v18（厳格仕様・KISS 統合版）

## 1. 目的と他機能との棲み分け

プロジェクト管理における WBS（階層構造）・DAG（実行依存）・担当調整・提出 OID に対する受け入れ判断・バグ派生ループの事実記録を管理する。

githive 内の他エンティティとの責務分離は次のとおりである。

| エンティティ | 責務 | 主な用途 |
|---|---|---|
| `issue` | 議論と意思決定の記録 | 要望・設計相談・一般的なバグ報告・決定事項の記録 |
| `task` | 個人の軽量 ToDo・実行メモ | 単発の作業項目。関連付けは `workpackage` 側からの片方向 `link(rel=task)` で表現する |
| `workpackage`（エイリアス `wp`） | プロジェクト管理・受け入れ記録の単位 | WBS 分解、実行依存（DAG）、担当調整（Claim/Lease）、提出 OID への受け入れ判断、バグ枝による自動ブロックと解除 |

### スキーマ互換性（[ADR-0018](../adr/0018-schema-version-bump-rule.md)）

- `workpackage` は新しい ref 名前空間 `refs/projects/workpackage/<workpackage-id>` を追加する。
- 既存の `issue` / `task` の fold 意味論およびスキーマは一切変更しないため、封筒 `v`（`event.SchemaVersion`）およびリポジトリ `schema_version`（`initapp.SchemaVersion`）の昇格は不要である。
- フェーズ 2 で `workpackage` に新しい状態影響 kind（`gates` / `attest` 等）を追加する場合は、その時点で ADR-0018 に従い両バージョンの昇格を行う。

---

## 2. スコープ・非目標・上流変更前提

### 対象と非目標（フェーズ 1）

本仕様（フェーズ 1）が**行うこと**は以下に限定する：
- `refs/projects/workpackage/<id>` への追記専用イベント記録と決定的実体化
- `internal/core/wpgraph` による観測済み `meta.json` 集合からの状態導出（`Cancelled`, `Completed`, `Blocked`, `InReview`, `Active`, `Ready`, `Planned` の 7 状態）
- 提出コミット OID に対する受け入れ判断（`accept`）およびバグ枝（`finding_of`）による自動ブロック・`waive`・`cancel` による解除

本仕様（フェーズ 1）が**行わないこと（非目標）**：
- ホスティングサービスの PR UI や行単位 diff コメント・コードレビュー UI の置き換え（`pr_url` は任意の補助参照に留める）
- `wp start` 等での自動ブランチ checkout・ブランチ命名テンプレート管理・ブランチ最終更新時刻の `wpgraph` 入力化（`branch` は任意のヒント文字列として記録するのみ）
- サーバー強制ロックや暗号署名・push 権限の読み取り時強制（暗号署名・push 制御の強制は将来の forge / `policy.json` に委ねる。一方、台帳が存在する場合の `accept` / `waive` における業務上の承認資格判定は §7 に従い `wpgraph` で適用する）
- 複数レビュアー定足数 $N > 1$（フェーズ 1 は 1 件以上で成立）
- Git コード側コミット祖先関係（`merge-base --is-ancestor` 等）の自動検証、および「finding 完了後に親が再提出したこと」の時系列順序強制（§8 が保証するのは「finding が指す提出とは別の提出に対して承認が与えられたこと」および「その finding 自体も完了/免除/取消されていること」である）
- SQLite 派生キャッシュへの必須依存（`wpgraph` は純粋再計算で成立させる）
- 独立 `milestone` ref、汎用 `gates[]` / `attest`、`scope[]` 衝突回避、`Orphan` フラグ、進捗ロールアップ数値の保存

### 実装着手の前提（上流文書・コードの同時改訂対象）

本仕様を実装へ移す際は、コード変更に先立ちまたは同一 PR 群で以下を必ず更新する：
1. **[00-vision.md](../00-vision.md)**：非目標の「コードレビューは対象外」について、「PR UI や diff コメントの置き換えは対象外だが、提出 OID への受け入れ判断の事実記録は対象とする」旨を明確化する。
2. **[02-data-model.md](../02-data-model.md)**：ref 名前空間一覧に `refs/projects/workpackage/<id>` を追加する。
3. **[13-roadmap.md](../13-roadmap.md)**：`workpackage` のフェーズ位置づけ・依存・完了条件を追記する。
4. **[ADR-0013](../adr/0013-checkpoint-envelope-convention.md) / [ADR-0026](../adr/0026-reject-lossy-event-order.md)**：per-entity チェーンの対象列挙（`issue` / `task` / `chat`）に `workpackage` を追補する。
5. **`spec/` 義務（`CLAUDE.md` 準拠）**：`spec/schemas/workpackage.schema.json`、`spec/reference/fold_workpackage.py`、`spec/vectors/fold-workpackage/` の golden ベクタ、順序不変性テスト、checkpoint 透過性テストを追加する。
6. **[10-cli-spec.md](../10-cli-spec.md)**：書き込み後自動 sync の `sync_failed` 警告の `data` に含まれるキー名 `items`, `total`, `write_ref` を明記する（§9 の `sync_failed` の `data` 構造と一致させる）。
7. **アプリ・コア登録箇所**：`internal/core/event`（`data_schema.go` の `loadDataContracts` への `workpackage` 登録、[ADR-0030](../adr/0030-embedded-event-contract-validation.md)）、`internal/core/refspace`（`perEntityFeatures`）、`internal/app/syncapp`（`SupportedFeatures`）、`internal/app/verifyapp`、`internal/app/fsckapp`、`cmd/githive`（CLI）、MCP ツール群。

---

## 3. ref とツリー

```
refs/projects/workpackage/<workpackage-id>   # workpackage-id は ULID（小文字 26 文字）

tree:
  meta.json
  body.md
  notes/
    <event-id>.md
```

- `body.md`：生の `body` 文字列をそのまま保存する（`create` で `body` 未指定時は 0 byte の空ファイル。改行の自動付与・trim は行わない）。
- `notes/<event-id>.md`：**fold で no-op にならず状態に適用されたイベントのうち**、非空の `note` / `reason` / `body`（`workpackage.note`）を持つイベントごとに 1 ファイル生成する（例：同一 claim への並行 `release(note)` のうち、ID 順 2 件目は no-op のため `notes/<id2>.md` は生成しない）。同一イベントの再適用に対して冪等とし、YAML front matter のキー順は `event_id`, `kind`, `actor`, `ts` に固定、文字列値のエンコードは `internal/core/event` の **canonical JSON 文字列エンコーダ**（UTF-8・最小限エスケープ・`&` 等を `\u0026` にしない）を規範とし、LF 改行で出力する：
  ```markdown
  ---
  event_id: "01kwpfzxg00000000000000003"
  kind: "workpackage.note"
  actor: "dev1@example.com"
  ts: "2026-07-04T12:00:00.000Z"
  ---

  本文テキスト
  ```
  - 本文末尾に LF が無い場合のみ 1 つの LF を補う。空文字列の `note` / `reason` はファイルを作成しない（`workpackage.note` の `body` は空文字列不可）。
  - **shallow snapshot 閲覧時の扱い（[ADR-0028](../adr/0028-shallow-read-views.md)）**：`notes/*.md` から復元した本文には `body_source: "rendered_markdown"` を CLI / MCP 出力で付与する（`body.md` には付与しない）。

---

## 4. 決定的実体化の不変条件

[02. データモデル](../02-data-model.md) に従い、以下を厳格な不変条件とする。

1. **ref 単独閉包性**：各 ref の `fold` は、その ref 自身のイベント集合（`sort_by_event_id(events)`）だけを入力として完結する。他の ref（親 WP、依存先 WP、子バグ枝、`users` 台帳等）、ローカル/リモートブランチ、現在時刻 `now` を `fold` 内で一切参照してはならない。
2. **事実の非廃棄**：`cancel` の履歴を保持し、後続のイベントを `fold` 内で切り捨てない。
3. **派生状態・時刻依存状態の非保存**：ref をまたぐ派生状態および現在時刻に依存する状態は `meta.json` に絶対に保存しない（`ready`, `blocked_by`, `cycle`, `stale_approval`, `expired`, `completed` 等）。
4. **純粋関数導出と `meta.json` 一致**：`wpgraph` は観測済み `meta.json` 集合と `now`（および任意の台帳スナップショット）から決定的に状態を導出する。`depends_on` / `parent_id` / `cancellations` は `meta.json` に書かれた値をそのままグラフ構築に使い、素の git で読める状態とツール出力を一致させる。

---

## 5. `meta.json` のスキーマと正規表現

`meta.json` は canonical JSON（キーの Unicode コードポイント順、2 スペースインデント、末尾 LF）で出力する。
**すべてのフィールドを常に存在させ、空集合は `[]`、未設定のスカラー・オブジェクトは `null` とする（`null` と `[]`、キー省略の揺れを禁止する）。**

```json
{
  "acceptance_version": "01kwp946800000000000000001",
  "acceptances": [
    {
      "by": "reviewer@example.com",
      "event_id": "01kwpt9gc00000000000000005",
      "submit_id": "01kwppvmr00000000000000004",
      "ts": "2026-07-04T15:00:00.000Z"
    }
  ],
  "cancellations": [],
  "claims": [
    {
      "branch": "feature/wp-sync-fix",
      "claim_id": "01kwpcj1w00000000000000002",
      "holder": "dev1@example.com",
      "lease_until": "2026-07-05T10:00:00.000Z",
      "released_at": null,
      "ts": "2026-07-04T11:00:00.000Z"
    }
  ],
  "created_at": "2026-07-04T10:00:00.000Z",
  "created_by": "planner@example.com",
  "current_submission_id": "01kwppvmr00000000000000004",
  "depends_on": [
    "01j8xp00000000000000000000"
  ],
  "finding_of": null,
  "id": "01kwp946800000000000000001",
  "kind": "work",
  "links": [
    {
      "id": "01j8x000000000000000000001",
      "rel": "issue"
    }
  ],
  "parent_id": "01j8xp00000000000000000001",
  "priority": "high",
  "submissions": [
    {
      "acceptance_version": "01kwp946800000000000000001",
      "branch": "feature/wp-sync-fix",
      "by": "dev1@example.com",
      "commit": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
      "event_id": "01kwppvmr00000000000000004",
      "pr_url": "https://github.com/example/repo/pull/42",
      "reviewers": [
        "reviewer@example.com"
      ],
      "ts": "2026-07-04T14:00:00.000Z"
    }
  ],
  "title": "sync のパス正規化を実装する",
  "updated_at": "2026-07-04T15:00:00.000Z",
  "waivers": []
}
```

### フィールド型・ソート順の厳密定義

| フィールド | 型 | 初期値 / 不変条件 / 並び順 |
|---|---|---|
| `id` | string (ULID) | エンティティ ID（不変） |
| `title` | string | 作業タイトル |
| `created_by` | string (actor) | 先頭 `create` の `actor`（不変） |
| `created_at` | string (RFC 3339) | 先頭 `create` の `ts`（不変） |
| `updated_at` | string (RFC 3339) | 状態（`meta.json` / `body.md` / `notes/`）を実質的に変化させた非 `checkpoint` イベントの `ts`（no-op で無視されたイベントでは更新しない） |
| `priority` | `"low" \| "normal" \| "high"` | 省略時 `"normal"` |
| `kind` | `"work" \| "container"` | 省略時 `"work"`（`container` は WBS 集計用親ノード。提出・承認の対象外） |
| `parent_id` | string (ULID) \| `null` | WBS 上の親 `workpackage` ID（省略時 `null`） |
| `depends_on` | string[] (ULID) | 辞書順昇順、重複なし（初期値 `[]`） |
| `finding_of` | `{workpackage: ULID, submit_id: ULID}` \| `null` | バグ枝の発生源。`create` 時のみ設定可・以後不変（`commit` フィールドは持たない） |
| `acceptance_version` | string (ULID) | `body.md` を最後に確定したイベント ID（`create` 時は `body` 有無に関わらず `create.id`） |
| `cancellations` | array | `{event_id, by, ts}` の追記専用配列、`event_id` 昇順（初期値 `[]`） |
| `claims` | array | `{claim_id, holder, ts, lease_until, branch, released_at}` の配列、`claim_id` 昇順（`lease_until` は UTC 変換＋ミリ秒未満切り捨て後に `YYYY-MM-DDTHH:MM:SS.sssZ` 形式で保存） |
| `submissions` | array | `{event_id, by, ts, commit, acceptance_version, branch, pr_url, reviewers}` の配列、`event_id` 昇順。`commit` は 40/64 文字小文字 hex の完全 OID または `null`（成果物なし提出）。`reviewers` は**大文字小文字を保持した** email 配列（Unicode コードポイント昇順・重複なし）または `null`（未指定。空配列 `[]` は禁止） |
| `current_submission_id` | string (ULID) \| `null` | `submissions` 末尾要素の `event_id`（`submissions` が空なら `null`） |
| `acceptances` | array | `{event_id, by, ts, submit_id}` の配列、`event_id` 昇順 |
| `waivers` | array | `{event_id, by, ts, target_id, reason}` の配列、`event_id` 昇順 |
| `links` | array | `{rel: "issue" \| "task" \| "chat" \| "workpackage", id: ULID}` の配列、`(rel, id)` の辞書順昇順・重複なし |

### `workpackage.checkpoint` の `data.state` 厳密スキーマ（[ADR-0013](../adr/0013-checkpoint-envelope-convention.md) / [ADR-0016](../adr/0016-checkpoint-read-shortcut.md)）
`workpackage.checkpoint` の `data` は `{state, event_count, head, max_event_id}` とし、`entity` は対象 WP の ref ID とする。`data.state` は `internal/core/materialize` の `State`（`Collections` の各値は JSON オブジェクト / map）に準拠し、次の 3 部構成を必須とする：
- `state.meta`：上記 `meta.json` と同一構造・同一値の JSON オブジェクト。
- `state.collections`：`{"body": {"text": <生の body.md 文字列 string（空時 ""）>}, "notes": { "<event-id>": {"actor": <string>, "body": <元イベントの非空生本文 string（末尾 LF 補完前）>, "event_id": <string>, "kind": <string>, "ts": <string>} }}`（キーは Unicode コードポイント昇順、note が 0 件なら `"notes": {}`）。
- `state.scratch`：`{}`（フェーズ 1 は空オブジェクト）。

---

## 6. イベント定義と fold 規則（全 13 種）

すべてのイベントは [02. データモデル](../02-data-model.md) の共通封筒（`v`, `kind`, `id`, `ts`, `actor`, `entity`, `data`）に従う。
`resolve` / `reopen` は廃止する（バグ枝自体が `workpackage` であり、修正の完了はバグ枝自身の `submit` + `accept` で表現するため）。

### 共通規則（no-op・時刻正規化・取消非破棄）
1. **実質変化なしは no-op**：状態（`meta.json`・`body.md`・`notes/`）を実質的に変えない操作（空の `edit {}`、既存値と同値の上書き、既に存在する `dependency` / `link` の再追加、存在しない `dependency` / `link` の削除、対象外・期限切れの `renew`、対象外・既に `released_at != null` の `release`、2 件目以降の `create`）はすべて **no-op** とし、`updated_at` を更新しない（`notes/<id>.md` も生成しない）。
   - **期限切れ後の `release`**：未解除（`released_at == null`）かつ `holder == actor` の claim に対する `release` は、期限切れ後（`event.ts > lease_until`）であっても有効な解除イベントとして適用し、`released_at = event.ts`・`notes/<id>.md`（非空 `note` 時）・`updated_at = event.ts` を記録する。
2. **`lease_until` の決定論的正規化とクランプ順**：RFC 3339 入力（オフセット付き可）をまず UTC へ変換し、**ミリ秒未満を切り捨て（floor to millisecond）**て `t_norm`（`YYYY-MM-DDTHH:MM:SS.sssZ`）を得た後、`event.ts` と比較して `min(max(t_norm, event.ts), event.ts + 7日)` にクランプする。
3. **取消は後続イベントを消さない**：`cancellations` への追記は後続イベントを fold 内で破棄しない。

### イベント一覧

| kind | data | 書き込み時検査・fold 規則 |
|---|---|---|
| `workpackage.create` | `title, body?, priority?, kind?, parent_id?, depends_on?, finding_of?, links?` | **書き込み検査**: `title` 非空、`parent_id != entity`、`depends_on` に自己 ID や不正 ULID を含まない。`depends_on` の各要素には `workpackage.dependency` の追加操作（`remove != true`）と**完全に同一の検査**を適用する：共通 Writer（`RequireCompleteProjects`）によりローカルの `refs/projects/*` に shallow ref が 1 つでもあれば `shallow_history`（終了 5）、各要素はローカル既知の `workpackage` ref から 8 文字以上の前方一致で解決し（未取得・未解決は `not_found` 終了 1、曖昧は `ambiguous_id` 終了 2）、解決後の完全 ID を保存する。対象 ref が shallow なら `shallow_history`（終了 5）で拒否し、ローカル既知グラフで `depends_on` 循環を作る場合も拒否する。`finding_of` 指定時は `kind == "work"` かつ `finding_of.workpackage != entity`。<br>**fold**: チェーン先頭イベントに限り初期状態を生成（2 件目以降の `create` は no-op）。この先頭判定は **`.checkpoint` イベントを除外した非 checkpoint イベント列の先頭**で行う（`create.id` より小さい ID の checkpoint が存在しても先頭判定に影響しない）。`body` があれば `body.md` を書き、有無に関わらず `acceptance_version = event.id`。`depends_on` は自己 ID・非 ULID を除いて重複排除ソート。`links` には `workpackage.link` と同じ集合規則を適用する：`rel` 不正・非 ULID・自己参照（`rel == "workpackage"` かつ `id == entity`）の要素は除外し、同一 `{rel, id}` の重複入力は**書き込み時にエラーとせず受理**して fold で 1 件に統合し、`(rel, id)` 昇順で保存する。 |
| `workpackage.edit` | `title?, body?, priority?, parent_id?` | **書き込み検査**: `parent_id != entity`。<br>**fold**: 指定フィールドのみ評価。`title` / `priority` は既存値と異なる場合のみ上書き。`parent_id` はキーが存在する場合に `null` なら解除、有効 ULID（`!= entity`）なら上書き（既存と同値またはキー省略時は変化なし）。`body` キーが存在し既存 `body.md` とバイト列が異なる場合のみ `body.md` を上書きし `acceptance_version = event.id` に更新する。1 つ以上変化があれば `updated_at = event.ts`。 |
| `workpackage.dependency` | `target, remove?` | **書き込み検査（共通）**: `target` は有効 ULID かつ `target != entity`。共通 Writer（`RequireCompleteProjects`、[ADR-0028](../adr/0028-shallow-read-views.md)）に従い、ローカルの `refs/projects/*` に 1 つでも shallow ref が存在する間は**追加・削除を問わず**すべての書き込みを `shallow_history`（終了 5）で拒否する（`dep --remove` も例外としない）。<br>**追加操作（`remove != true`）のみ**：`target` はローカル既知 ref であること（8 文字以上の前方一致で解決）、およびローカル既知グラフで `depends_on` 循環を作らないこと。<br>**削除操作（`remove == true`）**：ローカルの全 `refs/projects/*` が完全履歴（非 shallow）である限り、`target` の存在照会・循環検査を行わない。`target` がローカル未取得（unfetched）の完全 26 文字 ULID であっても、また無関係な既存循環がグラフ上に残っていても、修復用削除として追記を許可する。<br>**fold**: `target == entity` または非 ULID なら no-op。`remove != true` で未登録なら追加、`remove == true` で登録済みなら削除して辞書順ソートに保つ（変化がなければ no-op）。 |
| `workpackage.claim` | `lease_until, branch?` | **書き込み検査**: `lease_until` は有効 RFC 3339 かつ UTC ミリ秒正規化後 `>= event.ts`、上限は `event.ts + 7日`。<br>**fold**: 上記正規化＋クランプを適用し、常に新しい不変レコード `{claim_id: event.id, holder: actor, ts: event.ts, lease_until: clamped, branch: branch\|null, released_at: null}` を `claims` に追加する（代理 `holder` 指定不可）。 |
| `workpackage.renew` | `claim_id, lease_until, branch?` | **書き込み検査**: §9 解決後、`holder == actor`（不一致は `unauthorized_claim_op` 終了 3）、`released_at == null`（解除済みは `claim_released` 終了 3）、`event.ts <= 既存.lease_until`（期限切れは `claim_expired` 終了 3）、`lease_until` 正規化後 `>= event.ts`。<br>**fold**: 上記条件をすべて満たすときのみ `lease_until` を正規化＋クランプ値へ更新し、`branch` 指定時は上書きする（不適格・変化なしは no-op）。 |
| `workpackage.release` | `claim_id, note?` | **書き込み検査**: §9 解決後、`holder == actor`（不一致は `unauthorized_claim_op` 終了 3）、`released_at == null`（解除済みは `claim_released` 終了 3。期限切れ後でも可）。<br>**fold**: `holder == actor` かつ `released_at == null` のときのみ `released_at = event.ts` を設定し、非空 `note` があれば `notes/<id>.md` を生成する（不適格は no-op）。 |
| `workpackage.submit` | `commit, acceptance_version, branch?, pr_url?, reviewers?, note?` | **書き込み検査**: `kind == "work"` であること、`acceptance_version` がローカル最新 fold の値と一致すること（不一致は `stale_acceptance_version` 終了 3 で拒否し CAS 再試行時も自動付け替えしない）、`commit` は完全 OID または `null`、`reviewers` **省略時は台帳不要（`null`）**。`reviewers` **明示時**は空配列 `[]` を拒否し、§7 の台帳区分に従って解決する。**台帳健全性を個々の reviewer 解決より先に判定する**：<br>- **台帳破損時（`ambiguous_email` を含む §7 の履歴破損すべて）**：reviewer 解決に進まず、すべて `verify_failed`（終了 4）で拒否する。直接操作時の `error.data` は `{reason, ref: "refs/projects/users/registry"}` とする。<br>- **台帳不完全（shallow / 未取得）時**：`shallow_history`（終了 5）で拒否（snapshot の `emails` を使わない）。<br>- **台帳あり（完全・健全）時**：username（`@` なし）は台帳ユーザーの `emails[0]`（大文字小文字保持。0 件なら `invalid_input` 終了 2、存在しない username は `not_found` 終了 1）へ解決。email 形式（`@` あり）は台帳の `emails[]` に完全一致で解決できること（0 人なら `invalid_input` 終了 2。健全台帳では email は一意に解決される）。<br>- **台帳なし時**：actor 形式 email（空白なし・`@` 含む・320 byte 以下）をそのまま検証（username 指定は `invalid_input` 終了 2）。<br>**fold**: `kind == "container"` なら no-op。`submissions` に追記し、`current_submission_id = event.id` とする。`reviewers` は `null` または**大文字小文字を保持したまま**重複排除・Unicode コードポイント昇順ソート済み配列として保存する。非空 `note` があれば `notes/<id>.md` を生成。 |
| `workpackage.accept` | `submit_id, note?` | **書き込み検査**: `kind == "work"` であること。§9 優先順位表に従い解決し、提出 0 件時・明示 ID 不在時・曖昧時・`!= current_submission_id` 時・`current_submission.acceptance_version != WP.acceptance_version` 時（CAS 再試行中の変化含む）はそれぞれ規定のエラーで拒否する（自動付け替え禁止）。<br>**fold**: `kind == "container"` なら no-op。`acceptances` に `{event_id, by: actor, ts, submit_id}` を追記する。非空 `note` があれば `notes/<id>.md` を生成。 |
| `workpackage.waive` | `target_id, reason` | **書き込み検査**: `reason` 非空、`target_id` は有効 ULID かつ `target_id != entity`。<br>**fold**: `target_id == entity` または非 ULID または `reason` 空なら no-op。`waivers` に `{event_id, by: actor, ts, target_id, reason}` を追記し、`notes/<id>.md` に `reason` を保存する。 |
| `workpackage.cancel` | `reason?` | **fold**: `cancellations` に `{event_id, by: actor, ts}` を追記する。非空 `reason` があれば `notes/<id>.md` を生成する。 |
| `workpackage.note` | `body` | **書き込み検査**: `body` 非空文字列。<br>**fold**: `body` が空なら no-op。`notes/<event-id>.md` を生成し `updated_at = event.ts` とする。 |
| `workpackage.link` | `rel, id, remove?` | **書き込み検査**: `rel` は `issue\|task\|chat\|workpackage`、`id` は有効 ULID（`rel == "workpackage"` かつ `id == entity` は拒否）。<br>**fold**: `rel` 不正・非 ULID・自己参照なら no-op。`(rel, id)` 集合へ追加・削除を適用し辞書順ソートに保つ（変化がなければ no-op）。 |
| `workpackage.checkpoint` | `state, event_count, head, max_event_id` | **fold**: 常に無視する。上記 `data.state` スキーマに従い、追記前後でツリー・`data.state` ともに canonical bytes が一致し、256 KiB 上限を超える場合は ref を動かさず compact 失敗とする。 |

---

## 7. 権限・検証モデル（検出注記 ＋ 業務承認資格）

[11. セキュリティ](../11-security.md) に従い、hosted モードでは**暗号署名・push 権限の強制は行わない**（事後の `githive verify` による検出・警告に留め、強制は将来の forge / `policy.json` に委ねる）。一方、業務ロジックとしての「有効な承認（`accept`）・免除（`waive`）の資格判定」は、`wpgraph` が以下 3 区分の台帳状態に応じて一貫して適用する。

### 台帳状態（3 区分）の判定
1. **台帳あり（完全・健全）**：`refs/projects/users/registry` がローカルに存在し、shallow 境界を含まず、かつ [ADR-0027](../adr/0027-reject-ignored-users-key-events.md)（鍵依存順序）・複数信頼根・pin 不一致・`ambiguous_email`・規則 1〜4 署名不整合等の履歴破損がない。
2. **台帳不完全 / 破損（shallow / 未取得 / 破損）**：
   - **不完全**：`users/registry` が shallow 境界を含む、追跡リモート（`refs/githive-remote/users/registry`）にあるがローカル未取得、または `githive.trust.root` 設定済みだがローカル不在 → 警告コード **`registry_incomplete`**（`registry_history_complete: false`）。
   - **破損**：上記台帳履歴破損がある → 警告コード **`registry_invalid`**（`registry_valid: false`）。
   - **扱い（[ADR-0028](../adr/0028-shallow-read-views.md) / [ADR-0027](../adr/0027-reject-ignored-users-key-events.md) 準拠）**：WP の事実は閲覧できるが、完全で検査済みの台帳を必要とする **`accept` / `waive` の資格は一切成立させず（有効承認 0 件・有効免除 0 件扱い）**、`verified: false` と上記警告コードを必ず付与する。
3. **台帳なし（未署名プロジェクトモード）**：上記 1・2 のいずれにも該当しない（`users/registry` が存在せず `trust.root` も未設定）。

### 台帳状態別の `registry_history_complete` / `registry_valid`（`--json` 出力）

`--json` 出力の `registry_history_complete` と `registry_valid` は、台帳状態ごとに次の値で固定する（`verified` は §7「`verified` フラグの定義」に従い、台帳あり（完全・健全）以外では常に `false`）。

| 台帳状態 | `registry_history_complete` | `registry_valid` | 付随する出力 |
|---|---|---|---|
| 台帳あり（完全・健全） | `true` | `true` | なし |
| 台帳なし（未署名プロジェクトモード） | `true` | `true` | `verified: false` |
| 台帳不完全（shallow / 未取得） | `false` | `false` | `warnings` に `registry_incomplete` |
| 台帳破損（完全走査で意味破損） | `true` | `false` | `warnings` に `registry_invalid` |

### 操作ごとの権限・資格表と警告コード一覧

| 操作 | 資格・判定規則 | 条件を満たさない場合（`wpgraph` の扱いと固定警告コード） |
|---|---|---|
| **制限・ブロック側** (`finding` 起票, `edit(body)`, `dependency(add)`, `submit`) | 権限制限なし（誰の記録でも表示・ブロック・旧承認失効に効く）。 | 親 `P` は観測済みだが `finding_of.submit_id` が `P.submissions` に存在しない場合、finding 自身に **`orphaned_finding`** を出力し非ブロックとする。 |
| **`accept`**（`submit` に対する承認） | 1. **台帳あり（完全・健全）時**：`submission.by` と `accept.by` の双方が台帳の `active` ユーザーへ `emails[]` **完全一致**で一意解決でき、かつ **`accept.by` と `submission.by` の解決済みユーザー ID が異なること**（自己承認禁止）。さらに `submission.reviewers != null` なら、そのいずれかの email の解決済みユーザー ID と `accept.by` の解決済みユーザー ID が一致すること。<br>2. **台帳不完全 / 破損時**：**承認資格を成立させない（0 件扱い）**。<br>3. **台帳なし時**：`reviewers != null` なら `actor` 完全一致を推奨、自己承認も許容する。 | 台帳あり時（無効扱い）：<br>- `submission.by` または `accept.by` が未登録・非 active・解決不能 → **`unauthorized_accept`**<br>- 同一ユーザー ID → **`self_accept`**<br>- `reviewers` 指定外 → **`unauthorized_accept`**<br>（※ 上記は該当 `accept` ごとに該当地点を判定し重複排除して `warnings[]` へ出力）<br>台帳なし時（許容＋注記）：<br>- 自己承認 → **`self_accept`**<br>- `reviewers` 指定外または未署名環境 → **`unverified_accept`** |
| **`waive(target_id)`**（finding `F` の免除） | `target_id` は認識済み finding `F` であり、`F.finding_of.workpackage == P.id` かつ `P.submissions` に `F.finding_of.submit_id` が実在すること（`S_orig = P.submissions[F.finding_of.submit_id]`）。免除者は `S_orig` に対する上記 `accept` と同じ基準で判定（台帳不完全 / 破損時は成立させず、台帳あり時は `S_orig.by` 本人不可）。**台帳なし時は、`accept` と完全に対称に、自己免除（`S_orig.by` 本人による免除）および `S_orig.reviewers` 指定外の免除も有効（許容）とする**。 | 次の 4 パターンのいずれか → **`invalid_waive_target`**（該当 `waive` を無効扱い）：<br>1. `target_id` が未観測（あわせて `incomplete_inputs` に `workpackage:<target_id>` を併記）<br>2. 観測済みだが `finding_of == null`（通常の `work` / `container`）<br>3. `finding_of.workpackage != P.id`（別の親 `Q` の finding）<br>4. `finding_of.submit_id` が `P.submissions` に存在しない（孤立）<br>台帳あり時の資格外（未登録・`S_orig.by` 本人・`S_orig.reviewers` 外）→ **`unauthorized_waive`**。<br>台帳なし時（許容＋注記。`accept` の `self_accept` / `unverified_accept` と対称）：<br>- 自己免除 → **`self_waive`**<br>- `S_orig.reviewers` 指定外または未署名環境 → **`unverified_waive`** |
| **`cancel`** / `dep --remove` / `edit(parent_id)` | `meta.json` の状態をそのままグラフに採用する。 | 事後検査（`githive verify`）で署名・台帳整合を検査する。 |

### `verified` フラグの定義（`--json` 出力）
- **検証・観測対象入力集合 $R_{\text{inspected}}(w)$**：完了計算に使う $G_{\text{exec}}$ の辺（§8）とは別に、結果の検証と `incomplete_inputs` の算出に使う入力 ref 集合を次の和集合で定義する。
  1. 対象 WP `w` 自身。
  2. `w` から以下の辺の和集合を**推移閉包で**たどって到達可能なすべての参照先 ref ID（**未観測・shallow の ref も含む**。未観測 ref は `meta.json` がないため、その先へはたどらない）：
     - ① 依存辺：$v \in u.\text{depends\_on}$
     - ② `container` 子辺：$u$ が `container` かつ $v.\text{parent\_id} == u$ を満たす**全観測済み直接子**（**`Cancelled` な子も含む**。$G_{\text{exec}}$ ② と異なり `not is_cancelled(v)` で絞らない）
     - ③ finding 辺：$u = P$ に対する**全認識済み finding** $F$（免除済み・取消済みも含む。$G_{\text{exec}}$ ③ と異なり未免除・未取消で絞らない）
     - ④ WBS 親辺：$u \to u.\text{parent\_id}$
     - ⑤ `finding_of` 親辺：$u \to u.\text{finding\_of.workpackage}$（$u.\text{finding\_of} \neq \text{null}$ のとき。親 WP が未観測の finding は、この辺によりその親 ref ID が `incomplete_inputs` に入る）
     - ⑥ 免除対象辺：$u \to t$（$t \in u.\text{waivers[].target\_id}$。有効・無効、認識済み finding か否かを問わず全 `waivers` 要素の `target_id` を対象とする。対象 `t` が未観測、または shallow で snapshot 上 `invalid_waive_target` 等の対象外と判定される場合も、この辺によりその ref ID が `incomplete_inputs` に入る）
  3. `users/registry`。
- `verified` は、「`users/registry` が健全（台帳状態 1）かつ完全履歴であり、かつ $R_{\text{inspected}}(w)$ 内のすべての `workpackage` ref が観測済み・非 shallow（`shallow_refs` に含まれない）・§8 入力 `ref_verified_map[r] == true`（全非マージイベントコミットが `githive verify` 相当の署名・台帳検査を通過済み）」のときのみ `true` とし、それ以外（未検証・未署名・台帳なし・台帳不完全 / 破損・$R_{\text{inspected}}(w)$ のいずれかが未観測または shallow）は `false` とする。
- `incomplete_inputs` には、$R_{\text{inspected}}(w)$ 内の未観測 ref ID と `shallow_refs` に含まれる ref ID を**すべて** `workpackage:<id>` 形式で列挙する。配列は**重複なし・Unicode コードポイント辞書順昇順**とする（台帳の不完全は `registry_history_complete: false` と `registry_incomplete` 警告で表す）。
- **例**：`A → B → C`（依存辺）で `C` のみ shallow の場合、`A` と `B` が完全・検証済みでも `A.verified == false` となり、`A.incomplete_inputs` に `workpackage:<C>` が含まれる。`derived_status` の導出（$G_{\text{exec}}$・$S^*$）と `verified` / `incomplete_inputs` の算出（$R_{\text{inspected}}$）は独立であり、前者が `Completed` でも後者は `false` / 非空となり得る（§11 受け入れ条件 8）。

---

## 8. グラフエンジン（`internal/core/wpgraph`）の導出規則

`wpgraph` は `meta.json` 集合のみを入力とする純粋関数として実装し、素の git で読める値と完全に一致する状態を導出する。

### 入力
- 観測済み `workpackage` ref の `fold` 済み `meta.json` 集合（`observed_wps`）
- `shallow_refs`（`refs/projects/workpackage/*` または `users/registry` のうち shallow 境界を含む ref 集合）
- `users/registry` スナップショットおよび台帳状態（3 区分）
- `ref_verified_map`（観測済み各 ref → bool の署名・台帳検証結果マップ）：通常の `wp show` / `wp list` は全 ref `false` を渡す。`githive verify` 連携時・検証済みスナップショットを入力とする場合のみ、各 ref の検証結果の真偽値が入る。§7「`verified` フラグの定義」の $R_{\text{inspected}}(w)$ に対する集約に用いる。
- 現在時刻 `now`

### 実行前提グラフ $G_{\text{exec}}$ と完了集合 $S^*$（最小不動点）

1. **有効関係（`meta.json` そのまま）**：
   - `is_cancelled(w)` ⇔ `len(w.cancellations) > 0`
   - `depends_on(w)` / `parent_id(w)` は `meta.json` の値をそのまま使う。
2. **認識済み finding**：`F.finding_of != null` かつ `F.finding_of.workpackage`（親 `P`）が `observed_wps` に存在かつ `F.finding_of.submit_id` が `P.submissions` に実在するもの（親未観測は `incomplete_inputs`、存在しない `submit_id` は `F` に `orphaned_finding` 警告を出力し非ブロック）。
3. **実行前提グラフ $G_{\text{exec}}$ と WBS 循環**：
   - 実行前提の有向辺 $u \to v$（「$u$ の完了に $v$ の完了が必要」）：
     - ① 依存辺：$v \in u.\text{depends\_on}$
     - ② `container` 子集計辺：$u$ が `container` かつ $v$ が $v.\text{parent\_id} == u$ を満たす観測済み直接子 かつ `not is_cancelled(v)`
     - ③ 未免除・未取消の認識済み finding 辺：$u = P$ に対する認識済み finding $v = F$ で、`P` に `F.id` への有効な `waiver` がなく、かつ `not is_cancelled(F)` であるもの
   - WBS 親子辺：$u \to u.\text{parent\_id}$
4. **循環検出（`in_cycle(w)`）**：
   - ①〜③ からなる実行前提グラフの SCC（頂点数 $\ge 2$ または自己辺 $u \to u$）に属するノードを `cycle` と判定する。
   - WBS 親子辺の SCC（頂点数 $\ge 2$ または自己辺 $u \to u$）に属するノードを `wbs_cycle` と判定する。
   - `in_cycle(w)` ⇔ `cycle` または `wbs_cycle` に該当すること。
5. **完了集合 $S^*$（最小不動点）**：
   - $S_0 = \emptyset$ とし、非取消（`not is_cancelled(w)`）かつ非循環（`not in_cycle(w)`）のノード $w$ について、各段 $S_{i+1}$ を以下で反復し不動点 $S^*$ を得る：
     - **`kind == "work"`**：`current_submission_id != null` かつ `current_submission.acceptance_version == acceptance_version` かつ `current_submission` への有効 `accept` が 1 件以上 かつ $\forall v \in w.\text{depends\_on}: v \in S_i$ かつ 未解消 finding が 0 件（認識済み finding $F$ が未解消とは、有効 `waive` なし かつ `not is_cancelled(F)` かつ (`F.finding_of.submit_id == P.current_submission_id` または $F \notin S_i$)）。
     - **`kind == "container"`**：$v.\text{parent\_id} == w$ な観測済み直接子が 1 件以上存在し、その全子が `is_cancelled(v)` または $v \in S_i$（うち 1 件以上が $v \in S_i$）かつ $\forall v \in w.\text{depends\_on}: v \in S_i$ かつ 未解消 finding が 0 件。

### 評価優先順位と単一 `derived_status`

各 WP の `derived_status` は以下の**優先順位（上から最初に一致したもの）**で単一値を決定する：

1. **`Cancelled`**：`is_cancelled(w)`
2. **`Completed`**：$w \in S^*$
3. **`Blocked`**：上記 1・2 に該当せず、かつ以下の `blocked_reasons[]` が **1 件以上存在する**場合
4. **`InReview`**：`kind == "work"` かつ `current_submission_id != null`（未承認、または `stale_approval`）
5. **`Active`**：`kind == "work"` かつ `active_claim != null`（`released_at == null` かつ `now <= lease_until` のうち最小 `claim_id`）、または `kind == "container"` で非取消（`not is_cancelled(v)`）の直接子が 1 件以上存在
6. **`Ready`**：`kind == "work"` かつ上記 1〜5 に該当しない
7. **`Planned`**：`kind == "container"` かつ上記 1〜5 に該当しない（直接子が存在しない、または直接子がすべて `Cancelled` の場合）

### `blocked_reasons[]` の厳密述語と JSON 表現（重複なし・`(type, target_id)` 昇順）
- `derived_status` が `Cancelled` または `Completed` のとき、`blocked_reasons` は常に `[]`（空配列）とする（以下の述語を評価しない）。
- 以下の述語はすべて収束後の不動点 $S^*$ に対して評価し、反復途中の $S_i$ は最終出力に用いない。
- **`cancelled_dependency`**：$v \in w.\text{depends\_on}$ かつ $v \in \text{observed\_wps}$ かつ `is_cancelled(v)`（`target_id: v`。**`unresolved_dependency` と重複生成しない**）。
- **`missing_dependency`**：$v \in w.\text{depends\_on}$ かつ **$v \notin \text{observed\_wps}$**（未観測。`target_id: v`）。
- **`unresolved_dependency`**：$v \in w.\text{depends\_on}$ かつ $v \in \text{observed\_wps}$ かつ `not is_cancelled(v)` かつ $v \notin S^*$（`target_id: v`）。
- **`cycle`**：`w` が実行前提 SCC に属するとき 1 要素のみ生成（`target_id: w.id`）。
- **`wbs_cycle`**：`w` が WBS SCC に属するとき 1 要素のみ生成（`target_id: w.id`）。
- **`open_finding`**：`w` に対する未解消の認識済み finding `F` ごとに生成（`target_id: F.id`）。未解消の判定は §8 の定義の $S_i$ を**収束後の不動点 $S^*$** に置き換えて行う（有効 `waive` なし かつ `not is_cancelled(F)` かつ (`F.finding_of.submit_id == w.current_submission_id` または $F \notin S^*$)）。`accepted` は親 `w` の現提出に対する有効承認かつ版一致の有無を表す必須フィールドとし、これも $S^*$ 収束後の最終状態で判定する：
  ```json
  { "accepted": true, "target_id": "01j8x000000000000000000001", "type": "open_finding" }
  ```

### 派生フィールドと観測範囲（`--json` 出力）
- **`stale_approval`（bool）**：`current_submission_id != null` かつ、以下のいずれかを満たすとき `true`、それ以外は `false`：
  1. `current_submission` に対する有効承認が 0 件であり、かつ過去のいずれかの提出に有効承認が存在する。
  2. `current_submission.acceptance_version != acceptance_version` であり、かつ `current_submission` に対する有効承認が 1 件以上存在する（本文更新による現提出の承認失効）。
- **`warnings[]`**：文字列配列（重複なし・辞書順昇順）。
- **観測範囲と shallow 閲覧（[ADR-0028](../adr/0028-shallow-read-views.md)）**：
  - すべての導出結果はローカルに観測された ref 集合に対する結果であり、`--json` に `observation_scope: "local_refs_only"`, `verified`, `read_source`, `history_complete`, `registry_history_complete`, `registry_valid`（値は §7「台帳状態別の `registry_history_complete` / `registry_valid`」の表に従う）, `incomplete_inputs`, `warnings` を必ず出力する（`incomplete_inputs` は §7「`verified` フラグの定義」の $R_{\text{inspected}}(w)$ 内の未観測 ref と shallow ref をすべて含み、**重複なし・Unicode コードポイント辞書順昇順**で出力する。$G_{\text{exec}}$ の到達範囲には限定しない）。
  - **個別行と関連 ref の扱い**：完全履歴の `A` が shallow な `B` に依存する場合、`A` の閲覧では `B` の snapshot `meta.json` の状態をそのまま使い、`A` の出力は `read_source: "chain"`, `history_complete: true`, `verified: false`, `incomplete_inputs: ["workpackage:<B>"]` とする。`wp list` で完全行と shallow 行が混在する場合は、各行（item）が自身の `read_source` / `history_complete` を持ち、リスト全体としては 1 行でも shallow があれば `history_complete: false` とする。参照解決・書き込み（循環検査を含む）には snapshot を使用しない。共通 Writer（`RequireCompleteProjects`、[ADR-0028](../adr/0028-shallow-read-views.md)）に従い、ローカルの `refs/projects/*` に 1 つでも shallow ref が存在する間は、**`dep --remove` を含むすべての書き込み**を `shallow_history`（終了 5）で拒否する（例外は設けない）。ローカルの全 `refs/projects/*` が完全履歴である限り、`dep --remove` は追記対象自身の `depends_on` から `target` を除くだけで `target` 側の ref を参照しないため、§6 `workpackage.dependency` の削除操作の規則に従い `target` がローカル未取得（未観測の完全 26 文字 ULID）でも拒否しない。

---

## 9. `wp start`・CLI・MCP・エラーコード

### `githive wp start <id>` の動作（シンプル助言モデル）
1. **事前 `fetch` と通信失敗時の扱い**：オンライン CLI（`--no-sync` なし）は最初に `fetch`（`refs/githive-remote/*` のみ更新。既存 `syncapp.fetchNamespace` と同じ refspec・`FetchPruned`（pruned fetch）を用いる）を試みるが、通信不能で `fetch` が失敗してもローカル保存を止めず、警告を出してローカル最新状態での事前検査・`claim` 追記へ進む（[10. CLI 仕様](../10-cli-spec.md) 準拠）。**MCP 経由は [ADR-0011](../adr/0011-mcp-no-auto-sync.md) に従い事前 `fetch` も `sync` も行わない**。
   - **通信前の remote 名入力検査（`ValidateRemote`）**：オンライン CLI は事前 `fetch` の通信を始める前に、選択した remote 名を `ValidateRemote` で検査する。remote が未設定、または remote 名が不正な場合は、入力不備として **`invalid_remote`（終了 2）で即拒否し、`claim` を保存しない**（ローカル続行しない。ローカル WP ref の CAS 更新に一度も成功しない）。`ValidateRemote` を通過した有効な remote への通信が失敗した場合に限り、下記 `prefetch_failed` 警告を出してローカル続行する。両者は対の規則であり、`invalid_remote` を `prefetch_failed` に読み替えてローカル続行してはならない。
   - **`prefetch_failed` 警告**：事前 `fetch` 失敗時は機械可読な警告 `{code: "prefetch_failed", message, data: {remote}}`（`data.remote` は `"origin"` 固定ではなく**実際に選択した remote 名**、`message` は `fetch` のエラー文字列）を発行する。後続が成功（終了 0）ならトップレベル封筒の `warnings[]` に格納する。後続がどのエラー（終了 1・2・3・4・5 のいずれか）で終了する場合も、そのエラーの `code`・終了コード・固有の `data` を変えずに維持したまま、`error.data.warnings[]` に `prefetch_failed` を追記する（`sync_failed` がある場合は同じ配列に `[{code, message, data}]` 形式で併記する）。たとえば `prefetch_failed` 時にローカル `meta/config` が不正なら、`schema_unsupported` / `invalid_config`（終了 5）の `code`・`data` を維持したまま `error.data.warnings[]` に `prefetch_failed` を併記する。
   - **追跡 OID スナップショット（`FetchPruned` 結果からの一組取得）**：事前 `fetch` 成功時は、次の 4 項目を**すべて当該 `FetchPruned` の同じ結果から一組として**取得する：① `<id>` の前方一致解決に用いる追跡側の候補 WP ID 集合、② 選択した対象 WP の追跡 tip OID（`refs/githive-remote/workpackage/<id>`）、③ 追跡 `meta/config` の OID（`refs/githive-remote/meta/config`）、④ 上記各 ref の不存在。fetch 完了後に `refs/githive-remote/*` を改めて読み直して組み立ててはならない（別プロセスが直後に別 remote を `refs/githive-remote/*` へ fetch しても、候補集合・tip OID・config OID・不存在判定が異なる remote の状態と混線しないようにする）。この一組は**不変のスナップショット**として**ローカル追記 CAS 完了まで**（手順 2 の事前検査・各 CAS 再試行・初回採用の親指定を経て `claim` のローカル CAS が成功するまで）保持し、その間は追跡 ref 名を再読しない（別プロセスの `fetch` による追跡 ref の巻き戻り・prune で、既に観測した `release` / `cancel` 等が検査入力から消えないようにする）。ローカル追記 CAS 完了後の通常 `sync` 段階（手順 3）にはこのスナップショットを持ち込まず、通常の `syncapp.Sync` に従って再 `fetch`・再計画を行う。
2. **事前検査・CAS 再試行ループ・追記の順序（先行取り込みなし）**：
   - **ローカル ref への先行取り込みは行わない**：初回採用・fast-forward によるローカル WP ref への事前取り込みは廃止する。事前検査は**ローカル ref と（事前 `fetch` 成功時は）追跡 ref（`refs/githive-remote/workpackage/<id>`）のインメモリ仮 union のみ**で評価し、ローカル WP ref の更新は後述の大手順 3（追記と同期）の `claim` 追記時およびその後の通常 `sync` でのみ行う。したがって以下 0〜3 のいずれかで拒否した場合（`shallow_history`・`schema_unsupported`・`invalid_config`・`meta_config_conflict`・`conflicting_event_id`・`event_ref_mismatch`・`invalid_event_data`・`event_order_violation`・`clock_skew`・`already_cancelled`・`claim_held` 等）、**拒否した `start` 自身はローカル WP ref の CAS 更新に一度も成功していない**（ローカル未存在の場合は `start` 自身が ref を作成しない）。これは並行する他プロセスがローカル WP ref へ保存したコミットが保持されることを妨げない。
   - **追跡側にだけある WP への初回 `start`（初回採用）**：CAS 再試行で再読した時点でローカルに対象 WP ref が存在せず、追跡側（`refs/githive-remote/workpackage/<id>`）にのみ完全な WP 履歴が存在する場合、その初回採用に限って、**検査に使った追跡 tip（上記スナップショットで固定した追跡 OID）を `claim` コミットの親（parent OID）および fold・`clock_skew` 最大 ID 検査の基点とし、ローカル ref 作成時の CAS 期待値は「不存在（zero OID）」とする**。これにより中間 ref（`claim` を含まない追跡 tip のみのローカル ref）を公開せず、`create → … → claim` の履歴を 1 度の CAS でローカル ref として保存する。追跡側の `create.id` 等より小さい ID の `claim` は、この基点に対する最大 ID 検査により `clock_skew`（終了 5）で確実に拒否され、ref は作成されない。CAS が競合した場合（他プロセスが先にローカル ref を作成した場合）は再読から再試行し、ローカル ref が存在すれば通常の手順（ローカル tip を親・基点とする）に従う。
   - 事前検査・`claim` 追記の前に、必ず以下 0〜3 を**この順序で**検査する。0 は全経路（オンライン・`--no-sync`・MCP・`prefetch_failed` 時）で仮 fold より先に必ず実行し、1 は事前 `fetch` 成功時のみ 0 に続けて実行する。
   0. **共通 Writer 検査（既存 `entitychain.Writer.AppendOnto` / [ADR-0032](../adr/0032-fail-closed-project-config.md) と同順）**：
      - **① ローカル `meta/config` 検査**：ローカルの `meta/config` が対応版を超える `schema_version` なら `schema_unsupported`（終了 5、[ADR-0018](../adr/0018-schema-version-bump-rule.md)）、存在する config の欠損・破損・版情報不正は `invalid_config`（終了 5、ADR-0032）。
      - **② `RequireCompleteProjects`（[ADR-0028](../adr/0028-shallow-read-views.md)）**：ローカルの `refs/projects/*` に shallow ref が 1 つでもあれば `shallow_history`（終了 5）で即拒否する。対象 WP 以外の無関係な ref（例：shallow な `task/T`）のみが shallow で、追跡側にだけ完全な対象 WP がある場合も同様に拒否し、対象 WP ref は一切動かさない（作成もしない）。
   1. **③ 追跡側 `meta/config` 検査と分岐同版競合判定（事前 `fetch` 成功時のみ）**：0 を通過した後、仮 union の評価より前に、通常の `syncapp.Sync` と同じく追跡側 `meta/config`（スナップショットで固定した OID）を検査し、ローカル側と比較する。
      - 追跡側が対応版を超える `schema_version` なら `schema_unsupported`（終了 5、[ADR-0018](../adr/0018-schema-version-bump-rule.md)）。
      - 追跡側に存在する config の欠損・破損・版情報不正は `invalid_config`（終了 5、[ADR-0032](../adr/0032-fail-closed-project-config.md)）。
      - **祖先関係がない分岐した同版 config で canonical 内容が異なる場合のみ** `meta_config_conflict`（終了 4、`retryable: false`、`error.data` に両側の OID `{local_oid, remote_oid}` を返す、[ADR-0025](../adr/0025-meta-config-conflict.md)）。片側の `meta/config` tip が他方の祖先である場合（ADR-0025 の手順で作成した解決済みマージコミットを含む）は正常に通過する。
   2. **検査入力の組み立て**：ローカル追記における毎回の CAS 再試行（`entitychain.Writer`）では、初回・再試行を問わず**常に「再読したローカル ref tip ＋（事前 `fetch` 成功時は）スナップショットで固定した追跡 WP tip OID」のイベント集合を `event.IDSet`（[ADR-0026](../adr/0026-reject-lossy-event-order.md)）で和集合として組み立て**、新規 `claim` 封筒を生成する（この時点では保存しない）。ローカル追記 CAS 完了までの再試行ごとに再読するのはローカル tip のみで、追跡 ref 名は再読しない。これにより、別プロセスがローカルへ `note` 等を追記して CAS 競合した場合でも、また同時に別プロセスの `fetch` が追跡 ref を巻き戻し・prune した場合でも、既に追跡側で観測した `release` / `cancel` 等が事前検査の入力から消えることはない（追跡 ref は検査の入力にのみ使い、`claim` 保存前にローカル ref を動かさない。追跡 tip を `claim` コミットの親とするのは上記の初回採用に限る）。
   3. **ローカル保存前の検査順序**：上記 0・1 を通過した後、各 CAS 再試行（初回を含む）において、ローカル ref へ保存する前に以下を**この順序で**検査し、いずれかで拒否した場合はローカル ref を更新しない。**破損履歴に対して状態判定を行わないため、① 履歴健全性検査をすべて通過した後にのみ ② 状態事前検査を行う**：
      - **① 履歴健全性検査**（スナップショット時点で追跡 WP ref が存在した場合は、固定した追跡 tip OID から到達可能な完全履歴を通常 `sync` の履歴受け入れ検査（[ADR-0023](../adr/0023-conflicting-event-identities.md) / [ADR-0026](../adr/0026-reject-lossy-event-order.md)）と同じ基準で検査に含める。以下 1〜5 はすべて**仮 fold より前**に必須で実行し、いずれかで拒否した場合 `start` 自身のローカル CAS 成功は 0 回とする）：
        1. **ID 衝突検査**：再読したローカル ref tip ＋（事前 `fetch` 成功時は）固定した追跡 tip OID ＋ **今回の新規 `claim` 封筒自身**を含めたイベント集合に対し `event.IDSet` の衝突検査を行う。同一 ID で canonical bytes が不一致なら `conflicting_event_id`（終了 4）で即拒否する（追跡側にのみ存在する ID との衝突も対象とする）。
        2. **ref と封筒の照合**：再読したローカル tip ＋（事前 `fetch` 成功時は）固定した追跡 tip OID から到達可能な全イベント封筒について、封筒の feature・entity ID が対象 WP ref（`refs/projects/workpackage/<id>`）と一致することを照合し、不一致なら `event_ref_mismatch`（終了 4）で即拒否する（[ADR-0030](../adr/0030-embedded-event-contract-validation.md)、通常 `sync` の履歴受け入れ検査と同一基準）。
        3. **既知 kind の data 契約検査**：同じイベント集合のうち `loadDataContracts` に登録された `workpackage.*` の既知 kind について data 契約を検査し、不正 data（例：`lease_until` を欠く `workpackage.claim`）があれば `invalid_event_data`（終了 4）で即拒否する（[ADR-0030](../adr/0030-embedded-event-contract-validation.md)。未知 kind は前方互換として拒否しない）。追跡 tip を親とする初回採用でも同様に検査する。
        4. **順序検査（`create` 件数を各 tip と union の両方で検査）**：`ValidateEntityOrder` 相当の検査を、**存在する各 WP tip の完全履歴**（ローカル tip が存在すればその完全履歴、スナップショット時点で追跡 tip が存在すればその完全履歴のそれぞれ）と、**両履歴を同一 ID で重複排除した仮 union** の**両方**に対して行う。いずれか 1 つでも、同一 ID 重複排除後の非 checkpoint の `workpackage.create` が**ちょうど 1 件でない**場合（0 件＝`note` のみや `checkpoint` のみの履歴、または異なる複数の `create`）、もしくは `create` より前の**非 checkpoint** イベントがあれば `event_order_violation`（終了 4）で即拒否する（§11 の `create` 1 件契約と同一基準）。`workpackage.checkpoint` は先頭判定の対象外であるため、`create.id` より小さい ID を持つ checkpoint を含む正当な履歴は、この順序検査を含む `wp start` の保存前検査を通過する（§11 受け入れ条件 7 の非 checkpoint 先頭判定と同じ扱い）。したがって、ローカル `note` のみ＋追跡 `create` のみ（またはその左右逆）のように union では `create` がちょうど 1 件になる場合でも、`create` を欠く側の tip により拒否される。**ref 自体の不存在**（ローカル ref 未作成での初回採用、事前 `fetch` 失敗・`--no-sync`・MCP・追跡 ref 不存在で追跡側を入力にしない場合）は検査対象の tip がないだけであり、`create` 0 件の履歴とは区別して違反としない。
        5. **`clock_skew` 検査**：最大 ID 検査（[ADR-0026](../adr/0026-reject-lossy-event-order.md)）は上記の分岐和集合とは分離し、**追記先のローカル ref（再読 tip）の非 checkpoint 履歴**に対してのみ行う（追跡側にのみ存在するイベントの ID は最大 ID 検査に含めない）。ただし上記の初回採用では、`claim` コミットの親となる**追跡 tip の非 checkpoint 履歴**を基点とする。違反は `clock_skew`（終了 5）。
      - **② 状態事前検査**（①を通過した仮 union の fold 状態で判定。新規 `claim` 封筒は含めない）：
        1. `Cancelled` なら追記せず `already_cancelled`（終了 3）。
        2. **自分を含む**有効 `active_claim` が存在すれば追記せず `claim_held`（終了 3、自分が保持中なら `renew` を案内）。
3. **追記と同期・排他的な追記後判定表**：`claim` イベントをローカル ref へ追記する。オンライン CLI（`--no-sync` なし）は既存 CLI の書き込み後自動 sync（`cmd/githive/root.go` の `syncIfEnabled`、[10. CLI 仕様](../10-cli-spec.md) 準拠）と同じく、対象 WP ref と `refs/projects/notify/stream` を `sync` する。この `sync` は手順 1 の追跡 OID スナップショットを用いず、通常の `syncapp.Sync` として改めて `fetch` し、その結果から計画を立て直す。
   - **ラウンド別一組スナップショットの使用**：追記後の通常 `sync` は、単一ラウンドの場合も複数ラウンド（`push` 拒否後の再 `fetch`・再計画等）の場合も、**各ラウンドにおいて、そのラウンドで実行した `FetchPruned` の同じ結果から、選択した remote の `meta/config` OID と、そのラウンドで計画する全 ref（対象 WP・`refs/projects/notify/stream`・その他の対象 ref）の OID または不存在を一組として取得し、`checkSchemaBothSides`（両側スキーマ検査）とすべての `planRef` がその一組を使う**。この一組はラウンドごとに新たに取得するもので、手順 1 の追跡 OID スナップショットとは別物である（手順 1 のスナップショットは持ち込まない）。`fetch` 後に共有の追跡 ref 名（`refs/githive-remote/*`）を改めて読み直して検査・計画してはならない。これにより、別プロセスが他 remote を `refs/githive-remote/*` へ fetch しても計画が混線せず、**選択 remote で観測した状態（`UpToDate` / `FastForwardIn`）または選択 remote への `push` 成功（`FastForwardOut` / `Merged`）だけ**が下記 `published: true` の根拠となる。
   - **`published`**：`sync` 結果のうち**対象 WP ref 単体の action** が `UpToDate` / `FastForwardIn` / `FastForwardOut` / `Merged` のいずれかであれば `true`、それ以外（対象 WP の配送失敗・結果なし）は `false`。他 ref（例：`notify`）の結果は `published` に影響しない。
   - **`sync_failed` の発行条件**：オンライン CLI で `sync` を実行し、その同期エラー `syncErr != nil` のとき（対象 WP ref の失敗、**または** 他 ref（例：`notify`）の同期エラー）に発行する。したがって：
     - **WP 公開成功・`notify` 失敗**：`published: true` かつ `warnings` に `sync_failed`（`data.published: true`, `delivery_complete: false`）。
     - **WP 配送失敗**：`published: false` かつ `warnings` に `sync_failed`（`data.published: false`, `delivery_complete: false`）。
     - **`--no-sync` 指定時および MCP 経由**：同期を省略するため `published: false` とし、`sync_failed` は**発行しない**。
   - **`sync_failed` の `data` 構造**（`syncIfEnabled` と一致）：`items`（同期に成功した処理済み各 ref の `{ref, action, local_oid?, remote_oid?, backup_ref?, recovery?, warning?}`。失敗した ref は含まれない）、`total`（`items` の件数）、`warnings?`（ref 単位の `sync_warning` がある場合のみ）、`local_written: true`、`local_write_preserved: true`、`write_ref`（書込対象の対象 WP ref 名）、`published`（上記と同値）、`delivery_complete: false`。`message` は `syncErr` のエラー文字列。
   - **警告の JSON 表現**：グラフ導出結果（§8）の `data.warnings` は `string[]`。CLI トップレベル JSON 封筒の `warnings` は `[{code, message, data}]`（[10. CLI 仕様](../10-cli-spec.md) 準拠）。終了 3 エラー時に `sync_failed` を併記する場合は `error.data.warnings` に `[{code, message, data}]` で格納する。以下は「WP 公開成功・`notify` 失敗」で勝者になった例と、「WP 配送失敗」で `claim_lost` になった例：
     ```json
     { "ok": true,
       "data": { "claim_id": "01j8x000000000000000000002", "acquired": true, "published": true, "active_claim": { } },
       "warnings": [ { "code": "sync_failed", "message": "...",
         "data": { "items": [ { "ref": "refs/projects/workpackage/01j8x000000000000000000001", "action": "merged" } ], "total": 1,
           "local_written": true, "local_write_preserved": true,
           "write_ref": "refs/projects/workpackage/01j8x000000000000000000001", "published": true, "delivery_complete": false } } ] }
     ```
     ```json
     { "ok": false,
       "error": { "code": "claim_lost", "message": "...", "retryable": false,
         "data": { "claim_id": "01j8x000000000000000000002", "published": false, "active_claim": { },
           "warnings": [ { "code": "sync_failed", "message": "...",
             "data": { "items": [ { "ref": "refs/projects/notify/stream", "action": "up-to-date" } ], "total": 1,
               "local_written": true, "local_write_preserved": true,
               "write_ref": "refs/projects/workpackage/01j8x000000000000000000001", "published": false, "delivery_complete": false } } ] } } }
     ```
   **再 fold 対象**は、**同期処理（`sync`）が戻った時点で、実際のローカル WP ref を再読して fold した状態**に一本化する（`sync` の成否や失敗箇所で分岐しない。`--no-sync` / MCP は `sync` を行わないため、追記後に実際のローカル WP ref を再読して fold する）。既存 `sync` は分岐を先にローカルでマージして ref を進めた後に `push` するため、マージ成功・`push` 失敗時も実際のローカル tip にはマージ結果が反映されている。事前検査で用いた仮 union は判定に再利用しない。したがって、分岐中のローカルに有効な旧 claim `C` があり、追跡側だけに `release(C)` がある状態で、仮 union の事前検査を通過して新規 claim `N` をローカル追記した後：
   - `sync` がローカルマージに成功し対象 WP の `push` が拒否された場合は、再読したローカル tip に `release(C)` が合流済みのため、`N` が有効期限内なら **終了 0・`acquired: true`・`published: false`・`warnings` に `sync_failed`** を返す。
   - `sync` が最初の `fetch` で失敗してローカルマージも起きなかった場合は、再読したローカル tip に `release(C)` がないため `C` が `active_claim` 勝者となり、**終了 3・`code: "claim_lost"`・`published: false`・`error.data.warnings` に `sync_failed`** を返す。
   この再 fold した状態と `now` に対し、以下の**排他的決定表（上から最初に一致したもの）**で結果を返す（補償 `release` は自動生成しない）：
   1. `is_cancelled(w)` → 終了 3、`code: "already_cancelled"`、`error.data: {claim_id, published}`
   2. 今回の `claim_id` の `released_at != null` → 終了 3、`code: "claim_released"`、`error.data: {claim_id, published, active_claim}`
   3. 今回の `claim_id` が期限切れ（`now > lease_until`）→ 終了 3、`code: "claim_expired"`、`error.data: {claim_id, published, active_claim}`
   4. `active_claim.claim_id == 今回のclaim_id` → **終了 0**、`{claim_id, acquired: true, published, active_claim}`（`sync_failed` 発行条件 `syncErr != nil` を満たす場合は封筒 `warnings` に `sync_failed` を併記）
   5. それ以外（別 claim が `active_claim` 勝者）→ 終了 3、`code: "claim_lost"`、`error.data: {claim_id, published, active_claim}`（`sync_failed` 発行条件 `syncErr != nil` を満たす場合は `error.data.warnings` に `sync_failed` を併記。終了 3 の 1〜3 も同様）
4. `--branch <name>` は単なるヒント文字列として記録し、自動 checkout は行わない。

### CLI & MCP コマンド一覧

```
githive wp new --title <t> [--body <b>] [--kind work|container] [--priority p] [--parent id] [--depends-on id,...]
githive wp edit <id> [--title <t>] [--body <b>] [--priority p] [--parent id | --clear-parent]
githive wp finding <parent-id> --title <t> [--body <b>] [--submit-id id] [--parent id]
githive wp list [--ready] [--mine] [--blocked] [--in-review] [--parent id] [--json]
githive wp show <id> [--json]
githive wp start <id> [--branch <name>] [--lease <duration>] [--no-sync]
githive wp renew <id> [--claim-id <cid>] [--lease <duration>] [--branch <name>]
githive wp release <id> [--claim-id <cid>] [-m note]
githive wp submit <id> [--commit <sha> | --no-commit] [--branch <name>] [--pr-url <url>] [--reviewers u1,u2] [-m note]
githive wp accept <id> [--submit-id <eid>] [-m note]
githive wp waive <id> --target <finding-id> -m <reason>
githive wp cancel <id> [-m reason]
githive wp dep <id> <target-id> [--remove]
githive wp note <id> -m <note>
githive wp link <id> <linked-id> --rel <issue|task|chat|workpackage> [--remove]
```

- **`--submit-id` / `--claim-id` のコマンド別解決・エラー優先順位表**：
  1. 対象 WP 自体の ID が存在しない → `not_found`（終了 1）
  2. **フラグ明示時**：対象 WP の全配列（`submissions[]` または `claims[]`）内で前方一致解決し、0 件なら `not_found`（終了 1）、2 件以上なら `ambiguous_id`（終了 2）。1 件特定後の検査：
     - `accept`：`!= current_submission_id` → `stale_submission`（終了 3）、`acceptance_version` 不一致 → `stale_acceptance_version`（終了 3）
     - `finding`：実在する任意旧提出の指定を許容
     - `renew`：`holder != actor` → `unauthorized_claim_op`（終了 3）、`released_at != null` → `claim_released`（終了 3）、`event.ts > lease_until` → `claim_expired`（終了 3）
     - `release`：`holder != actor` → `unauthorized_claim_op`（終了 3）、`released_at != null` → `claim_released`（終了 3）
  3. **フラグ省略時**：以下のコマンド別候補集合から選択（0 件なら `invalid_input` 終了 2、2 件以上なら `ambiguous_id` 終了 2）：
     - `accept` / `finding`：`current_submission_id`（提出配列 0 件なら `invalid_input` 終了 2）
     - `renew`：`claims[]` のうち「`holder == actor` かつ `released_at == null` かつ `event.ts <= lease_until`」の集合
     - `release`：`claims[]` のうち「`holder == actor` かつ `released_at == null`」（**期限切れを含む**）の集合
- **その他の引数規則**：
  - `wp start <id>` の短縮 ID 解決：オンライン CLI で事前 `fetch` が成功したときは「ローカル WP ref ＋ 追跡 WP ref（`refs/githive-remote/workpackage/*`）の完全 ID を重複排除した集合」、事前 `fetch` 失敗時・`--no-sync` 指定時・MCP 経由は「ローカル WP ref の集合」から、**8 文字以上の前方一致**で解決する。
  - `wp finding <parent-id>`：`finding_of: {workpackage: <parent-id>, submit_id}` のみを設定し、**`parent_id` はデフォルトでは設定しない（`null`、`--parent` 明示時のみ設定）**。
  - `wp new --depends-on id,...`：各 ID に `dep` 追加時（`--remove` なし）と同一の規則を適用する。すなわちローカル既知の `workpackage` ref から**8 文字以上の前方一致**で解決し（未取得・未解決は `not_found` 終了 1）、循環・対象 shallow は拒否し、ローカルに shallow な `refs/projects/*` が存在する間は `shallow_history`（終了 5）で拒否する（§6 `workpackage.create`）。
  - `dep <id> <target-id>`：追加時（`--remove` なし）は `<target-id>` をローカル既知の `workpackage` ref から**8 文字以上の前方一致**で解決する（未解決は `not_found` 終了 1）。`--remove` 時は、`<target-id>` が完全 26 文字 ULID であれば未観測（ローカル未取得）でも存在照会なしで許可する（完全 ID でない場合はローカル既知 ref から 8 文字以上の前方一致で解決する）。いずれもローカルに shallow な `refs/projects/*` が存在する間は §6 の共通 Writer 規則により `shallow_history`（終了 5）で拒否する。
  - `--submit-id` / `--claim-id` の前方一致解決は、共通 CLI（[02. データモデル](../02-data-model.md)）と同じく**最低 8 文字**を要求し、8 文字未満は `invalid_input`（終了 2）とする。
  - `--lease`：省略時は `24h`。
  - `--commit` / `--no-commit`（`submit`）：どちらも省略時は現在の `HEAD` コミット OID（完全 hex）を使用（`--no-commit` 指定時は `commit: null`）。
  - `--rel`（`link`）：必須フラグ（省略不可）。`<linked-id>` は指定された `--rel` の feature の全ローカル ref から前方一致解決し、完全 26 文字 ULID の疎な参照は相手未取得時も許可する（追加・削除で同一正規化）。
  - `list --mine`：`active_claim.holder == self`（勝者 claim のみ）で絞り込む。
  - `new` / `edit` / `finding` の `--parent <id>`：ローカル既知の `workpackage` ref から**8 文字以上の前方一致**で解決する（未取得・未解決は `not_found` 終了 1、曖昧は `ambiguous_id` 終了 2）。
  - `list --parent <id>`：`parent_id` で絞り込む。`<id>` はローカル既知の `workpackage` ref から**8 文字以上の前方一致**で解決する。ただし完全 26 文字 ULID の場合は、未観測（ローカル未取得）の親 ID による絞り込みも許可する。
  - `waive --target <finding-id>`：ローカル既知の `workpackage` ref から**8 文字以上の前方一致**で解決する。ただし完全 26 文字 ULID の疎な参照は、対象が未取得でも許可する（§6・§7 の未観測免除対象規則と整合させる）。
- **一覧・表示と MCP**：
  - `wp list` の人間向け ID 列は、フィルター前の全ローカル `workpackage` ref に対して一意となる最短プレフィックス（最低 8 文字、必要なら 26 文字）を表示し、`--json` は完全 26 文字 ULID を返す（[ADR-0031](../adr/0031-unambiguous-entity-list-ids.md)）。
  - `actor` は共通封筒契約どおり `git user.email` をそのまま記録し、台帳 username → email 解決は `--reviewers` 入力にのみ適用する。
  - 上記すべての操作に対応する MCP ツール（`wp_*`）を同一 `app` 層経由で提供する（ADR-0011 に従い MCP は自動 sync しない）。
- **終了コード / `error.code`（既存 CLI `cmd/githive/root.go` 準拠）**：
  - 終了コード **1**：`not_found`（明示した ID が見つからない場合）
  - 終了コード **2**：`invalid_input`（入力不備・省略時候補 0 件・提出 0 件での `accept`/`finding`）、`ambiguous_id`（曖昧な短縮 ID）、`invalid_remote`（`wp start` の事前 `fetch` 前の `ValidateRemote` で remote 未設定・remote 名不正。`claim` を保存しない）
  - 終了コード **3**：`stale_acceptance_version`, `stale_submission`, `claim_held`, `claim_lost`, `claim_expired`, `claim_released`, `unauthorized_claim_op`, `already_cancelled`（競合・前提不一致）
  - 終了コード **4**：`conflicting_event_id`, `event_order_violation`, `invalid_event_data`, `event_ref_mismatch`（直接操作の破損・順序違反・data 契約違反・ref と封筒の不一致。`wp start` の保存前検査を含む）、`meta_config_conflict`（`retryable: false`、`error.data: {local_oid, remote_oid}` に両側 OID を返す。`wp start` の事前検査を含む）、直接操作で台帳破損を検出した `verify_failed`（`error.data: {reason, ref: "refs/projects/users/registry"}`。`submit --reviewers` 等）、および集約コマンドの `fsck_failed`（`error.data.findings[].code` に個別原因） / `verify_failed`（`error.data.reports[].issues[].code` に個別原因）
  - 終了コード **5**：`clock_skew`, `shallow_history`, `schema_unsupported`, `invalid_config`（環境・履歴不足・設定非互換）

---

## 10. 素の git での読み方

```
git for-each-ref refs/projects/workpackage
git show refs/projects/workpackage/<id>:meta.json
git show refs/projects/workpackage/<id>:body.md
git log --format='%s' refs/projects/workpackage/<id>
```

---

## 11. 履歴受け入れ検査・旧クライアント互換性・受け入れ条件

- **履歴受け入れ検査（[ADR-0023](../adr/0023-conflicting-event-identities.md) / [ADR-0026](../adr/0026-reject-lossy-event-order.md)）**：
  - 同一 ref・同一 ID の封筒は未知フィールド込み canonical JSON bytes 一致時のみ重複排除し、不一致は `conflicting_event_id` を `clock_skew` より先に返す。
  - 初回採用・fast-forward・分岐マージ・checkpoint 経由のすべてで `event.IDSet` と `create` 順序検査を適用し、不正時は ref を進めず `fsck` / `verify` に同原因を報告する（`fsck` / `verify` コマンド自体は終了 4・`fsck_failed` / `verify_failed` 封筒内の `error.data.findings[].code` / `error.data.reports[].issues[].code` に個別原因を返す）。
  - **`create` 1 件契約**：WP 履歴を採用・実体化するには、同一 ID 重複排除後に非 checkpoint の `workpackage.create` が**ちょうど 1 件**必要とする。`create` が 0 件の履歴（`workpackage.note` のみ、`workpackage.checkpoint` のみ等）は `event_order_violation`（終了 4）とし、`init` / `sync` / `start` では ref を更新せず拒否し、`fsck` / `verify` でも `event_order_violation` として報告する（2 件以上の異なる `create` も同コード）。
    - **各 tip と union の両方で検査**：`sync` / `start` は、**存在する各 WP tip（ローカル tip・追跡 tip のそれぞれが存在する場合）の完全履歴**で同一 ID 重複排除後の非 checkpoint `workpackage.create` が**ちょうど 1 件**であり、**かつ両履歴を同一 ID で重複排除した union でもちょうど 1 件**であることを要求する。片側の tip が `create` を欠く場合は、union で 1 件になっても `event_order_violation`（終了 4）とする。**ref 自体の不存在**（ローカル ref 未作成で追跡側を初回採用する場合、追跡 ref が存在しない場合）は初回採用・片側のみの正常条件であり、`create` 0 件の履歴とは区別して違反としない。
  - **data contract 検査（[ADR-0030](../adr/0030-embedded-event-contract-validation.md)）**：`internal/core/event` の `loadDataContracts` に登録した `workpackage.*` の既知 kind は、初回採用・fast-forward・分岐マージおよび `wp start` の保存前検査（§9 手順 2 ①）で data 契約を検査し、不正 data は ref を進めず `invalid_event_data`（終了 4）で拒否する。封筒の feature・entity ID が ref と一致しない場合は `event_ref_mismatch`（終了 4）で同様に拒否する。未知 kind は従来の前方互換に従い reducer で無視し、検査コマンドは警告に留める。
  - **追加必須テスト**：
    - **`workpackage.note` のみの履歴**：`create` を含まず `note` だけからなる WP ref を `init` / `sync` / `start` で取り込むと `event_order_violation`（終了 4）で拒否され ref が更新されないこと、`fsck` / `verify` が同コードを報告すること。
    - **`workpackage.checkpoint` のみの履歴**：`create` を含まず `checkpoint` だけからなる WP ref について同上。
    - **左右を入れ替えた `create` 欠落の拒否**：(i) ローカル tip が `note` のみ（`create` なし）＋追跡 tip が `create` のみ、(ii) ローカル tip が `create` のみ＋追跡 tip が `note` のみ（`create` なし）の 2 構成のそれぞれで、`wp start` と `sync` がともに `event_order_violation`（終了 4）で拒否し、`start` 自身のローカル CAS 成功が 0 回であり、ローカル WP ref が更新されないこと（union では `create` がちょうど 1 件でも拒否されること）。
    - **既知 kind の不正 data 拒否**：既知 kind の不正 data（例：`lease_until` を欠く `workpackage.claim`）を含む追跡側履歴を、`sync` の初回採用と fast-forward、および `wp start`（追跡 tip を親とする初回採用の場合を含む）のそれぞれで取り込むと `invalid_event_data`（終了 4）で拒否され、ref が更新されないこと（`start` はローカル CAS 成功 0 回）。
    - **未知 kind の前方互換**：将来の未知 kind（例：`workpackage.future_kind`）を含む履歴は拒否されず同期・保持され、fold では無視され（実体化ツリーに影響しない）、検査コマンドは警告のみを出すこと。
  - **ローカル追記拒否と同期許容の分離**：`stale_acceptance_version` や `clock_skew` はローカル追記・CAS 再試行時の書き込み拒否であり、既に別クローンで生成された正当な並行履歴（例：切断中の `submit(v1)` と `edit(v2)` の合流）は event-union マージで正常に同期・保持し、`wpgraph` 上で版不一致により `Completed` 不成立とする。
- **旧クライアント互換性**：
  - 現行旧クライアントは `refspace.Parse` が未知 feature を拒否し、`syncapp` は無視、`verifyapp` は `unrecognized_ref` 警告、`fsckapp` は検査対象から外しつつ `unrecognized_ref` の `warning` finding を追加する（既存データを破損させないが、旧クライアント経由では `workpackage` ref は伝播しない）。この非破壊・非伝播＋警告観測の挙動を結合テストで固定する。
- **実装受け入れ条件（必須テストケースと規範例）**：
  以下の各ケースについて、**①左右逆マージ後のツリー byte 一致、②同一観測入力に対する期待 `derived_status` / `warnings` / `stale_approval` / `blocked_reasons` 一致、③不正履歴での ref 非更新と規定エラーコード** を独立に検証する：
  1. **並行操作**：`edit/submit`、`claim/renew/release`、`accept/finding/waive/cancel`、**「`cancel` 後に後続 `submit` / `accept` が残ること」「並行 `release(note)` の 2 件目が no-op になり `notes/<id2>.md` が生成されないこと」「`container` と子の相互循環および `work` 同士の WBS 循環が未完了へ収束し依存元も未完了になること」**。
  2. **受け入れ基準変更後の失効**：`submit(S1, v1) → accept(S1) → edit(body, v2)`（および `accept` と `edit` の並行マージ）で、`derived_status == "InReview"`, `stale_approval == true` となり、同提出の再承認は `stale_acceptance_version`（終了 3）で拒否され再提出が必要になること。
  3. **大小文字・alias・台帳あり/不完全/破損/なし**：`Dev@example.org` と `dev@example.org` が別人の場合（指定側のみ有効）、1 人が複数 alias `["a@x", "b@x"]` を持つ場合の同一ユーザー ID 照合、shallow / 未取得台帳時の資格不成立（`registry_incomplete`）、破損台帳時の資格不成立（`registry_invalid`）、台帳なし時の `self_accept` 許容＋警告注記。さらに、台帳なし時の自己免除について次の規範例を固定する：
     - **台帳なし時の `self_waive` 許容**：台帳なし環境で、`work` の `P` に `submit(S1)`（`S1.by = "dev@example.org"`、`S1.reviewers = ["rev@example.org"]`）があり、`S1` に対する finding `F`（`F.finding_of = {workpackage: P, submit_id: S1}`、未完了・未取消）を提出者本人 `dev@example.org` が `waive(target_id=F)` した場合、免除が成立して `F` は `open_finding` を生成せず、`P` の `derived_status != "Blocked"`（`accept` がないため `"InReview"`）となり、`warnings` に `self_waive` が含まれること（本例は `S1.reviewers` 指定外かつ未署名環境の免除であり、§7 の規則により `unverified_waive` も含む）。`P` の期待出力（抜粋）：
       ```json
       { "derived_status": "InReview", "blocked_reasons": [],
         "warnings": ["self_waive", "unverified_waive"],
         "verified": false, "registry_history_complete": true, "registry_valid": true }
       ```
  4. **期限後の `release`・特殊文字 `actor`**：期限切れ claim に対する holder の `release(note="終了")` と `@name` 等を含む `actor` の `notes/<id>.md` が canonical JSON 文字列エンコーダでバイト一致すること。
  5. **二重 `start`・部分 sync 成功・配送失敗**：同一ローカル ref への二重 `start`（`claim_held` 終了 3）、事前 `fetch` 失敗時のローカル続行（`prefetch_failed` 警告の `data.remote` が実際に選択した remote 名であり、終了 0 ではトップレベル `warnings[]`、終了 1〜5 のいずれのエラーでも元の `code`・終了コード・固有 `data` を維持したまま `error.data.warnings[]` に入ること）、および `sync_failed` の次の 3 ケース：
     - (a)「WP ref 公開成功（`ActionMerged`）・`notify` 同期失敗」時に `published: true` かつ封筒 `warnings` に `sync_failed`（`data` に `local_written: true`, `local_write_preserved: true`, `write_ref`, `published: true`, `delivery_complete: false`）を返すこと。
     - (b)「勝者だが対象 WP の配送失敗」時に終了 0・`published: false`・封筒 `warnings` に `sync_failed`（`data.published: false`, `delivery_complete: false`）を返すこと。
     - (c) `--no-sync` / MCP 時は `published: false` かつ `sync_failed` を返さないこと。

     さらに、CAS 競合（別プロセスのローカル `note` 追記）後の再試行でも追跡側で観測済みの `release` / `cancel` が事前検査に反映されること、および追跡側に ID `X` の `note` があり、ローカル最大 ID が `X` 未満の状態で新規 `claim` の ID が `X` と衝突する場合に、ローカル保存前に `conflicting_event_id`（終了 4）で拒否され、拒否した `start` 自身がローカル WP ref の CAS 更新に一度も成功していないこと。加えて、分岐中のローカルに有効な旧 claim `C`、追跡側だけに `release(C)` がある状態で、仮 union の事前検査を通過して新規 claim `N` をローカル追記した後、`sync` が戻った時点の実際のローカル WP ref を再読して fold した状態で判定すること。すなわち、`sync` がローカルマージに成功し対象 WP の `push` が拒否された場合は、`N` が有効期限内なら **終了 0・`acquired: true`・`published: false`・`warnings` に `sync_failed`** を返し、`sync` が最初の `fetch` で失敗してローカルマージも起きなかった場合は `C` が勝者となり、**終了 3・`code: "claim_lost"`・`published: false`・`error.data.warnings` に `sync_failed`** を返すこと。

     さらに、追跡側だけに完全な WP が存在する場合の初回採用について次の 2 例を検証する：
     - **成功例**：ローカルに `refs/projects/workpackage/<WP>` がなく、追跡側にのみ完全な `WP` 履歴がある状態で `wp start WP` が成功し、`claim` コミットの親が検査に使った追跡 tip であり、ローカル ref が zero OID を CAS 期待値とする 1 度の CAS で（`claim` を含まない中間 ref を公開せずに）作成されること。
     - **`clock_skew` 拒否例**：同じ状態で、時計が追跡側の履歴より遅く新規 `claim` の ID が追跡側の `create.id` 等（追跡 tip の非 checkpoint 最大 ID）より小さい場合に、`clock_skew`（終了 5）で拒否され、ローカル ref が作成されないこと。

     加えて、`wp start` の事前検査について次の 4 ケースを検証する：
     - (d) **`meta/config` の祖先関係**：ローカルと追跡側の `meta/config` が同版で内容が異なっても、片側の tip が他方の祖先である場合（ADR-0025 の解決済みマージコミットを含む）は事前検査を正常通過すること。一方、祖先関係のない未解決の分岐で canonical 内容が異なる場合は `meta_config_conflict`（終了 4・`retryable: false`・`error.data` に `local_oid` / `remote_oid`）で拒否され、拒否した `start` 自身がローカル WP ref の CAS 更新に一度も成功していないこと。
     - (e) **無関係な shallow ref**：ローカルに無関係な shallow `task/T` があり、追跡側にだけ完全な `WP` がある場合、`wp start WP` が `shallow_history`（終了 5）で拒否され、ローカルに `refs/projects/workpackage/<WP>` が作成されないこと。
     - (f) **拒否時の ref 不変**：追跡側に有効な `claim` があり `claim_held`（終了 3）で拒否される場合、拒否した `start` 自身がローカル WP ref の CAS 更新に一度も成功していないこと（ローカル未存在なら `start` 自身が ref を作成しないこと。並行する他プロセスのコミットが保持されることは妨げない）。
     - (g) **追跡 OID の固定**：ローカル CAS 競合（別プロセスのローカル追記）と、別プロセスの `fetch` による追跡 ref の移動（巻き戻り・prune を含む）が同時に起きても、各再試行が初回に取得した追跡 tip OID・追跡 `meta/config` OID を維持して検査・初回採用の親指定を行い、既に観測した `release` / `cancel` を含めて正しく判定されること。

     加えて、追記後 `sync` のラウンド別一組スナップショット（§9 手順 3）について、`claim` 追記成功後の通常 `sync` が選択 remote A を再 `fetch` してから計画するまでの間に、別プロセスが別 remote B を `refs/githive-remote/*` へ fetch した場合の次の 2 ケースを検証する：
     - (h) **schema 検査すり抜け防止**：remote A の `meta/config` が対応版を超える将来版へ移行済みで、remote B の `meta/config` は対応版である場合、そのラウンドの `checkSchemaBothSides` が A の `FetchPruned` 結果から得た A の `meta/config` OID で検査して `schema_unsupported` で拒否し、B の状態を根拠に検査を通過しないこと。そのラウンドでは A 由来のいずれの ref も `planRef`・マージ・`push` されず、ローカル追記済みの `claim` は保持されること。この同期エラーは §9 手順 3 の規則どおり `sync_failed`（`message` に `schema_unsupported` を含み、`data.published: false`、`delivery_complete: false`）として報告され、`published: false` となり、終了コードは同手順の排他的決定表（再 fold 判定）に従うこと。
     - (i) **`notify/stream` 配送漏れ防止**：`claim` 追記に伴う通知 `N` が remote B には存在し remote A には未配信である場合、そのラウンドの `notify/stream` の `planRef` が A の `FetchPruned` 結果から得た A の OID（または不存在）で計画し、B の追跡状態を根拠に `up-to-date` と誤判定しないこと。合格条件は次のいずれかとする：`N` が A へ実際に `push` されて A の `notify/stream` に `N` が含まれる、または A への配送が完了せず `sync_failed`（`data.delivery_complete: false`）が返る。A に `N` がないまま警告なしで終了してはならない。
  6. **finding 並行と再提出・混在 shallow**：`S1` に finding `F` がある間に `S2` を承認し、その後 `F` が `Completed` になった場合は追加提出なしで `Completed` となる一方、`S1` のまま `F` が `Completed` になっても再提出なしでは `Completed` にならないこと。また完全行と shallow 行が混在する `wp list` で各行の `read_source` とリスト全体の `history_complete: false` が正しいこと。
  7. **checkpoint 復元・近道検証**：checkpoint 追記前後のツリー byte 一致および `data.state`（`collections.body.text`・`x` vs `x\n` の `collections.notes` を含む）canonical bytes 一致に加え、生成した checkpoint が `StateFromMap` で復元に成功して ADR-0016 の近道が実際に使われること、および checkpoint 後に `edit(body)`・`note`・`renew` を適用した結果が近道経由とルート全走査で byte 一致すること。さらに、`create.id` より小さい ID を持つ checkpoint が存在する場合でも、`create` の非 checkpoint 先頭判定が成立し、全走査と近道でツリーが byte 一致すること。
  8. **検証・観測対象入力集合 $R_{\text{inspected}}$**：
     - 完全・検証済みの container `P` に完了子 `A` と shallow で `Cancelled` な子 `B` がある場合、`P.derived_status == "Completed"` だが `P.verified == false` かつ `P.incomplete_inputs` に `workpackage:<B>` が含まれること。
     - `W.parent_id = P` で `P` が未観測の場合、`W.verified == false` かつ `W.incomplete_inputs` に `workpackage:<P>` が含まれること。
     - 完全・検証済みの `P` に `waive(target_id=X)` があり、`X` が未観測の場合、および `X` が shallow で snapshot 上対象外と判定した場合のいずれも、`P.verified == false` かつ `P.incomplete_inputs` に `workpackage:<X>` が含まれること。
     - **`invalid_waive_target` の全 4 パターン**：`P` に `waive(target_id=X)` がある場合、次の各パターンで `P` の `warnings` に `invalid_waive_target` が含まれ、その `waive` が有効免除として数えられないこと。`incomplete_inputs` への `workpackage:<X>` 併記は (i) 未観測時のみとし、(ii)〜(iv) では `invalid_waive_target` を理由とする併記を行わないこと（`X` が shallow 等で別理由により観測不完全な場合を除く）。
       - (i) `X` が未観測 → `warnings` に `invalid_waive_target`、`incomplete_inputs` に `workpackage:<X>`。
       - (ii) `X` は観測済みだが `finding_of == null`（通常の `work` / `container`）→ `warnings` に `invalid_waive_target`。
       - (iii) `X.finding_of.workpackage == Q`（`Q != P.id`、別の親の finding）→ `warnings` に `invalid_waive_target`。
       - (iv) `X.finding_of.submit_id` が `P.submissions` に存在しない（孤立）→ `warnings` に `invalid_waive_target`。
  9. **`dep --remove` による既存循環の修復**：`A.depends_on ∋ B` の状態で、ローカルの全 `refs/projects/*` が完全履歴（非 shallow）かつ `B` が未観測（ローカル未取得）の完全 26 文字 ULID である場合に、`dep A <Bの完全ID> --remove` が存在照会・循環検査なしで受理され（無関係な既存循環がグラフ上に残っていても拒否されない）、`A.depends_on` から `B` が除かれること。一方、`B` が shallow としてローカルに存在する場合は、共通 Writer（`RequireCompleteProjects`）により `dep A B --remove` が `shallow_history`（終了 5）で拒否され、ref が更新されないこと。
  10. **`create.links` の重複統合（golden vector 対象）**：同一 `{rel, id}` を重複して含む `links` を持つ `create` が書き込み時にエラーとならず受理され、fold 後の `meta.json` の `links` が 1 件に統合され `(rel, id)` 昇順で保存されること。これを `spec/vectors/` の golden vector として固定する。
