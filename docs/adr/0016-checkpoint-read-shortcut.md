# ADR-0016: チェックポイントを読み出しの起点に使う条件と、state への scratch/max_event_id の追加

- 状態：採用
- 日付：2026-07-28
- 関連：[ADR-0013](0013-checkpoint-envelope-convention.md)（チェックポイント封筒の規約）

## 文脈

[03-sync-and-concurrency.md](../03-sync-and-concurrency.md)「チェックポイント」は、チェックポイントを「チェックポイント以降のイベントだけ fold すれば現在状態を再構成できる」読み出しの近道と定め、「線形履歴の読み出し高速化（自分より前のイベントがすべてチェーン上の祖先である場合の起点）にのみ使う」と適用条件を限定している。

書き込み側は [ADR-0013](0013-checkpoint-envelope-convention.md) と `internal/app/fsckapp/compact.go` で実装済みだが、**読み出し側が 1 行も実装されていなかった**。すべての読み出しがルートまで全走査するため、`githive fsck --compact` は読み出しを一切速くせず、チェーンにコミットを増やすだけの操作になっていた。

読み出し側を実装するにあたり、2 つの障害が調査で判明した。

### 障害 1：checkpoint の state が fold 状態の一部しか持っていない

`fsckapp` の `stateToMap` は `State.Meta` と `State.Collections` しか書き出さない。`materialize.State` には第 3 の場所 `Scratch` があり、reducer が fold 中に必要とする作業データがここに入る。

| feature | Scratch のキー | Go の型 |
|---------|----------------|---------|
| issue | `labels` / `assignees` | `map[string]bool` |
| issue / task | `links` | `[]map[string]any` |
| task | `status_history` | `[]map[string]any` |
| chat | `participants` | `map[string]bool` |
| notify | `acks` | `map[string][]string` |
| users | `policy` | `map[string]any` |

notify の `acks` は `materialize.NotifyAcks` 経由で `acks.json` の内容そのものになり、users の `policy` は `policy.json` になる。**Scratch を復元せずにチェックポイントの state から fold を再開すると、これらの実体化結果が壊れる。**

### 障害 2：ULID の全順序と、チェーン上の祖先関係は一致しない

fold はイベントを **ULID の辞書順**で適用する（[02-data-model.md](../02-data-model.md)「イベントの全順序と実体化」）。近道が正しいのは「チェックポイントが包含する全イベントの ID が、チェックポイント以降の全イベントの ID より小さい」ときだけである。

チェックポイントはローカルの `fsck` が作るので、その後にローカルで追記されたイベントの ULID はチェックポイントより大きい。しかし**包含側**には、時計が進んだ他端末が作ったイベントが event-union マージ経由で入りうる。その ID がチェックポイント以降のローカルイベントより大きいと、全走査と近道で適用順が変わり、実体化結果が分岐する。[ADR-0012](0012-walkchainsince-clock-skew-best-effort.md) が認めているとおり時計異常は防げないため、この可能性は設計上排除できない。

## 決定

### 1. checkpoint の `data.state` を `{meta, collections, scratch}` の 3 部構成にする

`scratch` キーを追加する。値は各 feature の Scratch を JSON 表現に正規化したものとする（`map[string]bool` → オブジェクト、`[]map[string]any` → 配列、`map[string][]string` → 値が配列のオブジェクト）。

`meta` は `State.Meta` が nil のとき **JSON の `null`** を書く。`{}` と `null` を潰すと、users のように Meta を使わない feature で「まだ作成されていない」状態と「空の状態」が区別できなくなる。

往復変換は `internal/core/materialize` に集約する。`StateToMap` が書き出し、`Registry.StateFromMap` が読み戻す。型付き Scratch への復元は feature ごとの知識が要るので、各 Registry が自分の scratch デコーダを登録する。`fsckapp` はこのヘルパを呼ぶだけにする。

### 2. checkpoint の `data` に `max_event_id` を加える

チェックポイントが包含する全イベント（過去のチェックポイント自身を含む）の ULID の最大値を記録する。近道の成立条件を、これで**厳密に**判定できるようにするためである。

### 3. 近道が成立する条件

次をすべて満たすときだけ、チェックポイントの state を fold の初期状態として使う。ひとつでも欠ければ従来どおりルートまで全走査する（結果は同じで、遅いだけ）。

1. head から親を辿る経路が**線形**である（マージコミットに出会う前にチェックポイントに到達する）。[03](../03-sync-and-concurrency.md) の「線形履歴の読み出し高速化にのみ使う」の直訳である。
2. チェックポイント封筒の `data.state` が復元できる（`scratch` を含む新形式であり、decode に成功する）。
3. `data.max_event_id` が有効な ULID であり、**チェックポイント以降の全イベントの ID がそれより大きい**。

条件 3 が障害 2 への答えである。条件 2 により、`scratch` を持たない旧形式のチェックポイントは自動的に「近道に使えない」ものとして扱われる。githive はプレリリースであり運用中データが存在しないため、旧形式の migration は行わない。

### 4. 近道は状態再構成にのみ使う

`logapp`（タイムライン表示）は実イベントを必要とするので変更しない。`fsckapp` の compact 自体も `event_count` と `max_event_id` の算出に全イベントが要るため全走査を維持する。近道が入るのは `internal/app/entitychain` の読み出し経路（`Append` の fold と `Fold`）だけである。

## 理由

- Scratch を state に含める以外の選択肢（reducer 側で Scratch を廃し Meta/Collections だけで完結させる）は、5 つの reducer すべてを書き換えることになり、決定性の最終防衛線を触るリスクが近道の実装そのものより大きい。
- `max_event_id` を「チェックポイントの ID で代用する」ことも考えたが、それは「チェックポイントの ID が包含イベントの ID より必ず大きい」という、時計異常下で成り立たない仮定に依存する。1 フィールド増やす方が安い。
- 近道が成立しないときに**黙って全走査に落とす**のは、[01-architecture.md](../01-architecture.md)「エラー処理方針」の部分的成功の扱いに合う。近道はあくまで最適化であり、失敗しても利用者に見える挙動は変わらない。

## 帰結

- チェックポイントは fold で常に無視される（[determinism ルール](../../.claude/rules/determinism.md)）ままである。近道は fold の**入力の作り方**を変えるだけで、fold の意味論を変えない。したがって `event.SchemaVersion` の昇格は不要である。
- `data.state` の形式変更と `max_event_id` の追加は、チェックポイントを持つクローンと持たないクローンで実体化ツリーを分岐させない（チェックポイントコミットのツリーは追記前とバイト単位で同一のまま、ADR-0013）。
- 「近道で fold した状態」と「全イベントを素朴に fold した状態」が canonical JSON で byte 一致することを、全 feature × ランダムイベント列のプロパティテストで機械的に守る。
- 新しい Scratch キーを追加する reducer は、対応する scratch デコーダも同時に登録しなければならない。未知のキーはデコード時にエラーになり、近道が無効化される（壊れた復元より安全側）ので、忘れても実体化結果は壊れない。
