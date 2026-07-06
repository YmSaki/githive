# ADR-0011: MCP サーバーの書き込みツールは自動 sync しない

- 状態：採用
- 日付：2026-07-06

## 文脈

CLI の書き込みコマンドは既定で書き込み後に `githive sync` 相当のフェッチ/マージ/プッシュを1回だけ実行し、`--no-sync` で無効化できる（[10](../10-cli-spec.md)）。これは1プロセス1回きりの起動を前提にした設計であり、ネットワーク往復のコストは1回で済む。

MCP サーバー（[15](../15-clients.md)）は長時間稼働する1プロセスであり、Agent が1セッション中に issue/task/chat/notify への書き込みツールを連続で何十回と呼び出しうる。CLI と同じく「書き込みのたびに自動 sync」を踏襲すると、次の問題が生じる。

1. 呼び出しごとに fetch/push のネットワーク往復が挟まり、Agent のツール呼び出しループ全体が push の遅延・リトライに引きずられる。
2. sync 失敗時の扱いが不明瞭になる。CLI では失敗は warning として出力されユーザーが目視できるが、MCP のツール呼び出しは Agent が逐次消費するため、書き込みの成否とネットワークの成否が1回のツール結果に混在すると、Agent が「データが壊れた」のか「ただの push 失敗」なのか区別しづらい。
3. docs/15 の tool 一覧には `sync` 自体が独立したツールとして明記されており、書き込みツールに sync を畳み込む必然性はない。

## 決定

- `cmd/githive/mcp.go` の書き込みツール（issue_new/comment/status/... 等すべて）は sync を一切行わない。CLI の `--no-sync` を常に指定した状態と同じ既定動作にする。
- 明示的に sync したい Agent は `sync` ツールを呼ぶ。`status` ツールで未 push ref を確認できる。
- 自動 notify（issue.comment の reply-to、issue.assign の新規担当者、task.status(done)、task.reassign）は CLI と同じ規則（自分自身・既に対象だった相手への再通知を抑止）でそのまま発行する。CLI 側もこれらの notify イベント自体を自動 sync の対象に含めていない（`syncIfEnabled` は当該エンティティの ref のみを対象にし、`notifyapp.StreamRef` を含めない）ため、MCP のこの決定は既存の CLI の実際の挙動（notify イベントは常にローカルのみで生成され、次の明示的な sync まで push されない）を変えるものではない。

## 帰結

- MCP 経由の書き込みは常にローカル完結で高速・決定的になる。ネットワーク状態に書き込みの成否が左右されない。
- 「clone 直後に Agent が文脈を持てる」体験（[13](../13-roadmap.md) の MVP 定義）は変わらず成立する。sync は Agent が明示的に判断して呼ぶ操作になる。
- docs/15「入出力スキーマは CLI の `--json` の data と同一とする」は書き込みツールの返り値の形状（成功データ・warnings）には引き続き適用されるが、sync 実行の有無という副作用の既定値は CLI と異なる。この非対称性を認識せず「MCP は CLI と全く同じに動く」と誤解しないよう、本 ADR と `cmd/githive/mcp.go` 冒頭のコメントで明記する。
