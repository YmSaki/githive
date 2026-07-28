# ADR-0017: `githive sync` に wiki を fast-forward 限定で載せ、meta/config は通常経路に載せる

- 状態：採用
- 日付：2026-07-28
- 関連：[ADR-0003](0003-event-sourcing-commit-chain.md)（wiki はイベントソーシング非適用）、[ADR-0004](0004-merge-by-event-union.md)

## 文脈

`syncapp.SupportedFeatures` は issue / task / chat / notify / users の 5 つだけを対象にしており、wiki と meta/config が `githive sync` から漏れている。`githive status` の未 push 判定も同じ集合を使うため、両者を報告しない。

wiki の同期は現状 `githive wiki save` の中の独自 push 経路（`internal/app/wikiapp/write.go`。worktree で git 標準の 3-way マージを行い、push レースは 5 回まで再試行）だけである。この経路は「自分が編集して保存するとき」にしか走らないため、**他のクローンが push した wiki を `githive sync` で取り込む手段が無い**。編集の予定が無い読み手は、`git fetch` を自分で打つしか wiki を更新できない。

wiki を event-union 経路に載せることはできない。wiki は [ADR-0003](0003-event-sourcing-commit-chain.md) の決定どおりイベントソーシングの対象外で、通常の git ツリーと通常のマージを使う。イベント列が無いので和集合の取りようがない。

meta/config は逆に、ふつうのイベントチェーンである。載せられない理由が無いのに落ちている。

## 決定

### 1. wiki は fetch と fast-forward だけを sync が行う

`githive sync` は wiki について次の 3 つだけを行う。

1. **fetch**：既にワイルドカード fetch で `refs/githive-remote/wiki/main` は取得できているので、追加の通信は不要。
2. **fast-forward の自動適用**：ローカルがリモートの祖先なら、ローカル ref をリモート head へ CAS で前進させる。
3. **分岐時は自動マージせず警告**：ローカルとリモートが分岐している場合、Result に警告を載せて「`githive wiki edit` → `githive wiki save` で解消せよ」と案内する。エラーにはしない（終了コード 0 のまま）。

`githive.wiki.worktree` が設定されている（= 誰かが編集中）場合は、fast-forward もスキップして警告する。編集中の worktree の足元で ref が動くのを避けるためである。

push 方向は、ローカルがリモートより進んでいる（= リモートがローカルの祖先）場合の fast-forward push のみ行う。

### 2. meta/config は通常の event-union 経路に載せる

`SupportedFeatures` に `meta` を追加するだけでよい。meta/config はイベントチェーンであり、[03-sync-and-concurrency.md](../03-sync-and-concurrency.md)「共通祖先が無い場合」が同時 `githive init` による 2 ルートの合流を明示している以上、合流できる状態が仕様上の期待である。

ただし meta には reducer が無く、`config.json` は `githive init` が直接書く（[ADR-0016 の前提](../02-data-model.md)「meta には reducer が無い」）。したがってマージコミットのツリーをどう作るかを決める必要がある。**fold 結果からツリーを描画するのではなく、`config.json` を両側から読み、`schema_version` が大きい方を採用する**。同値なら local を採る。イベントは和集合として履歴に残る。

### 3. `githive status` の未 push 判定に wiki と meta/config を含める

sync 対象になった以上、未 push の報告からだけ外しておく理由が無くなる。

## 理由

- wiki の 3-way マージは worktree（実ファイル）を要する。`githive sync` は [03-sync-and-concurrency.md](../03-sync-and-concurrency.md) の全体設計として「ローカル ref の更新までで完結し、作業ファイルには触らない」原則で書かれており、sync がマージのために worktree を作り始めると、`wiki save` の既存のコンフリクトフロー（マーカーを残したまま人間が解消して再実行する）と二重になる。fast-forward だけなら ref 更新で完結し、この原則を壊さない。
- 分岐を**エラーにしない**のは、`githive sync` が複数 ref を一括処理するコマンドだからである。wiki が分岐しているだけで他の全 ref の同期が失敗するのは、[01-architecture.md](../01-architecture.md)「部分的成功は正常系として報告する」に反する。
- meta/config のマージで `schema_version` の大きい方を採るのは、[01-architecture.md](../01-architecture.md)「`schema_version` はリポジトリ全体の最低要求バージョンを表す」と「自分の対応より新しければ拒否」の組み合わせから、**下げる方向の合流が安全側にならない**ためである。上げる方向に倒せば、古いツールは拒否して止まる（安全）。下げると、新しい意味論で書かれた既存イベントを古い意味論で読む状態を招く。

## 帰結

- `wiki save` の既存挙動（push レース再試行、コンフリクト再実行）は一切変えない。sync 側は読み取り方向と fast-forward だけを担当する。
- 分岐した wiki は、誰かが `wiki save` を実行するまで分岐したままになる。これは現状（sync がまったく関与しない）より悪くはならず、少なくとも `githive sync` と `githive status` が分岐の存在を報告するようになる。
- 「wiki の自動 3-way マージ」を将来 sync に持たせたくなった場合は、本 ADR を改訂し、sync が一時 worktree を使う設計を明示的に選び直すことになる。
- meta/config が sync 対象になることで、同時 `githive init` した 2 クローンの `config.json` が合流する。`features` 配列など `schema_version` 以外のフィールドは、勝った側の `config.json` がそのまま採用される。
