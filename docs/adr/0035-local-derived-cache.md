# ADR-0035: ローカル派生キャッシュ（`.git/githive-cache.db` / Pure-Go SQLite）の導入

- 状態：採用
- 日付：2026-11-24
- 関連：ADR-0001、ADR-0002、ADR-0020、issue #200

## 文脈

`refs/projects/*` を唯一の正（SSOT）とする設計は、外部 DB サービスを持たずに単一バイナリで完結する強みを持つ。一方で、エンティティ数・イベント数が数千 ref / 数万イベント規模に増えると、毎回 `ForEachRef` + `ReadFile` / `WalkChain` / 暗号署名検証を繰り返すコストが線形以上に効いてくる。

ADR-0020 でも「将来課題：commit OID と reducer の版に結びついた、独立検証済みのローカルキャッシュを導入する必要がある」と明記されている。

## 決定

- **位置づけ**：`.git/githive-cache.db` は `refs/projects/*` から派生する**使い捨て可能なローカルキャッシュ**であり、SSOT ではない。削除・破損しても自動再構築される。
- **採用技術**：Pure-Go SQLite（`modernc.org/sqlite`）を採用する。CGO 不要のため単一バイナリ配布とクロスコンパイルを維持できる。
- **透過フォールバックと自動回復**：ファイル欠損・破損・スキーマ版不一致・オープン失敗・ロック競合のいずれも呼び出し元エラーにせず、キャッシュ削除＆再構築またはキャッシュ無しパス（`refs/projects/*` 直接走査）へ透過フォールバックする。
- **Windows 安全性**：`-wal` / `-shm` ファイルの残留によるロック競合や `TempDir` 削除失敗を避けるため、`PRAGMA journal_mode = DELETE` と `PRAGMA busy_timeout = 5000` を設定し、操作ごとに確実に `Close()` する。
- **テーブル構成**：
  1. `cache_meta(key, value)`：`schema_version` 等のメタ情報
  2. `ref_meta_index(ref_name, head_oid, feature, entity_id, meta_json)`：`list` 用のスナップショット索引
  3. `verified_folds(commit_oid, reducer_ver, max_event_id, state_json)`：検証済み checkpoint / 増分 fold スナップショット
  4. `verified_sigs(commit_oid, sig_valid, pubkey_fp, key_type)`：台帳非依存の暗号署名検証結果キャッシュ（台帳変更時はメモリ上のポリシー照合のみ再実行）

## 帰結

- キャッシュ有無で CLI/MCP 出力および実体化ツリーが byte 単位で完全一致する不変条件を維持する。
- `internal/core/localcache` は `database/sql` のみを用い、`encoding/json` を直接 import しない（canonical JSON 一元化規則の遵守）。
