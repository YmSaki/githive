# 13. ロードマップと実装順序

## 順序決定の方針

手戻りを最小にするため、次の 3 点を守る。

1. **全機能の共通機構（イベント、チェーン、実体化、マージ）を最初に完成させる**。issue も task も chat も同じ機構のインスタンスであり、機構の変更は全機能に波及する。逆に機構が固まれば、機能追加は fold 規則とペイロード定義の追加で済む。
2. **イベント封筒に actor と署名の置き場を初日から含める**。署名機能自体は後のフェーズだが、封筒に後からフィールドを足すとデータ移行が要る。
3. **決定性のテスト（[14](14-testing.md)）をフェーズ 0 から回す**。決定性の破れを後から直すのは全データ形式の変更になる。

## フェーズ一覧

| フェーズ | 内容 | 完了条件（exit criteria） |
|---------|------|--------------------------|
| P0 | コア機構 | イベント封筒、canonical JSON、chain 読み書き、materialize/merge の枠組み、決定性プロパティテストが green |
| P1 | issue 縦切り + sync | GitHub 上のリポジトリで issue の new/list/show/comment/status が動き、2 クライアント同時更新が自動収束する |
| P2 | task / chat / notify | 3 機能が issue と同じ機構で動く。自動 notify、未読検出が動く |
| P3 | users / 署名 / verify | 台帳、SSH 署名付与、`githive verify`、`whoami` が動く |
| P4 | wiki / 運用コマンド | wiki edit/save、fsck、チェックポイント、log（横断タイムライン） |
| P5 | MCP サーバー | `githive mcp serve` で Agent が全機能を操作できる。Claude/Codex plugin パッケージ生成 |
| P6 | TUI | ダッシュボード、issue/task/chat 操作 |
| P7 | VSCode 拡張 | サイドバー、閲覧、投稿 |
| P8 | forge 最小構成 | SSH 受け、pre-receive（権限/署名/スキーマ強制）、smart HTTP、mirror 互換 |
| P9 | forge 付加機能 | 通知配信、WebHook、連番払い出し、Web UI（読み取り） |
| P10 | LFS | batch API、FS ストア、wiki assets との接続 |
| P11 | CI | ワークフロー定義、scheduler、コンテナ runner |

**MVP は P0〜P2**（既存ホスティング上で issue/task/chat/notify が動く CLI）。
P3 まで到達すると他者と安心して共有できる状態になり、これを最初の公開リリース（v0.1）とする。

## フェーズ間の依存関係

```
P0 --> P1 --> P2 --> P3 --> P4
                      |       
                      +--> P5 --> P6, P7   （クライアント群。P6/P7 は並行可）
                      |
                      +--> P8 --> P9 --> P10, P11
```

- P5（MCP）は P3 後すぐ着手できる。P4 と並行可能。
- P8（forge）が P3 に依存するのは、pre-receive の権限・署名検証が users 台帳と検証ロジックの再利用だからである。ここを別実装にすると仕様の二重管理になる。
- P10/P11 は P9 と独立に進められる。

## 各フェーズの作業分解（実装 Agent への指示粒度）

### P0：コア機構

1. リポジトリ雛形（Go module、lint、CI 3 OS、テスト骨格）。
2. `core/event`：封筒型、canonical JSON encoder/decoder、スキーマ検証。golden テスト。
3. `core/refspace`：ref 名の構築と検証、列挙。
4. `core/gitx`：system git 呼び出し（version 検査、fetch/push、for-each-ref）。
5. `core/chain`：go-git によるコミット作成（message+tree）、チェーン走査、イベント抽出。
6. `core/materialize`：fold の枠組み（kind -> reducer の登録制）。ダミー kind で決定性プロパティテスト。
7. `core/merge`：event-union（収集、和集合、fold、マージコミット作成）。収束プロパティテスト。

### P1：issue 縦切り + sync

1. issue の reducer と ツリー writer（meta.json、body.md、comments/）。
2. `app/issueapp` と CLI サブコマンド（new/list/show/comment/status/edit/label/assign/link）。
3. `app/syncapp`：[03](03-sync-and-concurrency.md) のアルゴリズム、再試行、`githive sync`。
4. `githive init`（refspec、meta/config）。
5. 結合テスト：ローカル 2 clone + bare origin で同時更新 -> 収束。GitHub 実機での手動確認手順書。

### P2：task / chat / notify

1. task reducer + CLI（issue の複製から開始。共通化しすぎない。2 例目で共通パターンを抽出する）。
2. chat reducer + CLI。
3. notify reducer + CLI + 自動 notify 発行 + 未読計算。
4. `githive status`（横断要約）。

### P3：users / 署名 / verify

1. registry reducer（users/groups/policy）+ CLI。
2. `core/sign`：署名付与（git config 連携）、allowed_signers 生成、検証。
3. `githive verify`、信頼の根の照合、`whoami`。
4. actor 解決の差し替え（仮置き user.name -> 台帳連動）。

### P4〜P11

各フェーズ開始時に、このドキュメントと対象設計書（12、15）から同粒度の作業分解を起こす。
P8 以降は forge の詳細設計（12a: CI 仕様、12b: Web UI 仕様）を先行して書く。

## 手戻りリスクと先回りの手当て

| リスク | 手当て |
|--------|--------|
| 決定性の破れ（実装差でツリーが分岐） | P0 からプロパティテスト。canonical JSON を 1 実装に集約し、encoding/json の直接使用を lint で禁止 |
| 封筒スキーマの変更 | v フィールドと未知フィールド保存を最初から実装。スキーマは golden ファイルで凍結 |
| GitHub 側の refs 制約の見落とし | P1 の完了条件に GitHub 実機確認を含める。GitLab / Gitea も P2 終了時に確認 |
| Windows のパス・改行問題 | CI を 3 OS で P0 から回す。LF 固定、autocrlf 無効化を gitx が強制 |
| 機能間の共通化の失敗（早すぎる抽象化） | P2 の方針どおり「2 例目まで複製、3 例目で抽出」 |
| forge とクライアントの検証ロジック乖離 | 検証は core に置き、pre-receive はそれを呼ぶだけにする（P8 の構造要件） |
| IDE の自動 fetch によるローカル ref 破壊 | 追跡名前空間の分離で設計段階で解消済み（[ADR-0008](adr/0008-remote-tracking-separation.md)）。シナリオ 7 で回帰防止 |
| チェックポイントがマージでイベントを消す | 「fold ではチェックポイントを常に無視」を規則化（[03](03-sync-and-concurrency.md)）。シナリオ 8 で回帰防止 |
| ツールのバージョン混在による実体化分岐 | fold 意味論の変更に schema_version 昇格を必須化（[01](01-architecture.md)） |
