# CLI / MCP 境界テスト補償監査

対象 SHA: ae350d15145186ffbc9189ec500a6332f881d53b。読み取りのみの構造監査。対象42ファイルを列挙し、共通ハーネスと主要な補償・失敗注入箇所を精読した。全テストの実行・全分岐精読を主張しない。以前の64eでの選択実行証跡は `監査用ログ（非同梱）` と別管理する。

## Ledger

| 対象 | 補償 / seam | この証拠が示す範囲 | 限界 / 判定 |
| --- | --- | --- | --- |
| main_test.go TestMain / runCLI | global/system Git configをdevnull、ローカルCLIをgo build、Git identityをfixtureで設定 | 実CLIプロセスの引数・JSON・exit・Git永続状態 | 配布物そのもの/利用者の継承設定/ネットワーク認証の証拠ではない。設定中和は再現性のため妥当だが、通常環境との差を明記する必要がある |
| main_test.go runCLI | stdout/stderrを別pipe、stdoutの後stderrを同期読み出し、deadlineなし | 小さい通常出力の収集 | 大量stderr時の停止可能性という既知#154の構造的限界。ここでは新規停止再現を行っておらず、新しい重大確定所見には数えない |
| mcp_test.go newMcpTestSession / d7_mcpほか | SDK in-memory transport、実registerMcpTools/Resourcesを呼ぶ | SDK要求→実handler→実Gitの契約 | CLI起動/stdio framing/配布バイナリのMCP起動を通らない。コメントの「stdioと同じroundtrip」はhandler層に限定。別の固定配布物acceptance証拠が必要 |
| a6_cli_test.go retries | real bare origin + pre-push hookによる競合、同一イベントの有効コピーをcommit-treeで挿入 | 再試行回数・ローカル変更保持・Git push競合 | 衝突注入のため有効履歴を維持する補償は妥当。一般的な認証/ネットワーク断や異なるイベント衝突の網羅ではない |
| sync_reporting_test.go | real bare originのupdate hookで一refのみ拒否、wiki分岐も作成 | 明示syncの部分成功、warning/失敗JSON、実remote refへの到達 | implicit autosyncは別経路。これらの成功からautosync warning保持を推論できない（既に再現済の189関連修正待ち） |
| wiki_push_rejection_test.go / init_clone_test.go | real pre-receive hookで拒否 | push拒否時の分類とローカル成果保持 | Hostedサービス固有ACL/認証の再現ではない |
| ambiguous_id_test.go seedAmbiguousRefs | 固定ULID3件を通常empty commitへ向ける | ref名だけを読む曖昧候補分岐・JSON候補・exit2 | 有効イベント履歴のshow/read/署名検証の証明には使えない。狙いをコメントで明示しておりこの限定用途では妥当 |
| shallow_read_test.go | 実file:// clone --depth1、projectsもdepth1 fetch、読取前remote削除 | 真のshallow境界・ネットワークなしsnapshot read・strict操作拒否 | 手書きshallowファイルだけの疑似fixtureではない。Hosted shallow transport/利用者の全Git設定は別証拠 |
| sign_test.go / verify_negative_test.go | real ssh-keygenとGit署名、admin/key registry、規則一つずつ破損 | 実署名照合・診断コード・健全refがfalse-failしないこと | ssh-keygen欠如/旧版はskip。verifyCodesは結果とexitの整合性も検証し、一律拒否を通さない。これは妥当なpositive/negative対照 |
| clock_skew_classification_test.go / wiki_conflict_classification_test.go | typed errorを直接classifyErrorへ渡す | エラー→exit/code/recovery mapping | 実コマンドから当該errorへ到達する証拠ではない。単体mapping試験として妥当 |
| remote_injection_test.go | 危険remote入力とmarker不存在、CLI/MCP実handler | option remote拒否のexit/codeおよび拒否tool結果 | marker不存在単独では安全性証明にならないが、CLIはinvalid_remoteも検証。全remote形式の証明ではない |
| users_policy_test.go | POSIX test editor / TMPDIR /環境で内容と失敗を制御 | editor終了/不正JSON/ドラフト保持 | Windowsで2テストskip。Windows editor/draft契約はこのテストからPASSとできない |
| cliout_test.go captureStdout | package writerをbytes.Bufferへ一時置換しCleanup復帰 | envelopeのエンコード、fallback、部分JSON未出力 | 正当なwriter seam。[]map[string]anyが未対応であることに依存する負例は実装依存だが、呼出側がerrorを返すかはこの単体試験では証明されない。OS brokenpipe/write failure未検証 |

## Skipと証拠解釈

- Git欠如はCLI binary自体を作らず、多数のケースがskip。署名はssh-keygen欠如と-Y未対応もskipする。skipを成功した機能証拠として数えない。
- Windows明示skipはusers_policy_test.goのPOSIX editor2件。main_test.goの.exe分岐はskipではない。最終プラットフォーム判断は各OS CIの実行記録と突き合わせる。
- 語句grepの原始結果は `監査用ログ（非同梱）`。`SkippedWithoutMetaConfig`等のテスト名はt.Skipではないため件数に混ぜない。
- 外部API mockでSync成功を捏造するパターンは精読した主要fixtureでは見つからなかった。全42ファイルのあらゆるmock不存在を保証する結論ではない。

## 確定重大所見

この構造監査から新たに確定した重大バグはない。既報の「implicit autosyncが成功Syncのwarningsを捨て、trust-root採用時に直前local-only userが見えなくなる」は64eの実CLI再現証拠と別レポート `監査用ログ（非同梱）` に保持している。明示sync試験・in-memory MCP試験・reflogありfixtureの成功でこの問題が補償されると解釈してはならない。担当が189/recovery修正中であり、このledgerはその修正を検証済とはしない。

## 対象inventory

- `cmd/githive/a6_cli_test.go`
- `cmd/githive/agent_profile_test.go`
- `cmd/githive/ambiguous_id_test.go`
- `cmd/githive/clock_skew_classification_test.go`
- `cmd/githive/d7_cli_test.go`
- `cmd/githive/d7_mcp_test.go`
- `cmd/githive/display_ids_test.go`
- `cmd/githive/doctor_test.go`
- `cmd/githive/email_ownership_test.go`
- `cmd/githive/event_size_test.go`
- `cmd/githive/init_clone_test.go`
- `cmd/githive/log_test.go`
- `cmd/githive/main_test.go`
- `cmd/githive/mcp_test.go`
- `cmd/githive/mcp_wiki_resource_test.go`
- `cmd/githive/meta_conflict_classification_test.go`
- `cmd/githive/notify_ack_id_test.go`
- `cmd/githive/notify_delivery_test.go`
- `cmd/githive/notify_group_test.go`
- `cmd/githive/notify_hooks_test.go`
- `cmd/githive/notify_parity_test.go`
- `cmd/githive/p4_cli_test.go`
- `cmd/githive/registry_notify_input_test.go`
- `cmd/githive/related_ids_test.go`
- `cmd/githive/remote_injection_test.go`
- `cmd/githive/schema_version_test.go`
- `cmd/githive/shallow_read_test.go`
- `cmd/githive/sign_test.go`
- `cmd/githive/summary_newline_test.go`
- `cmd/githive/sync_discovery_test.go`
- `cmd/githive/sync_reporting_test.go`
- `cmd/githive/users_policy_test.go`
- `cmd/githive/users_validation_test.go`
- `cmd/githive/verify_corruption_test.go`
- `cmd/githive/verify_negative_test.go`
- `cmd/githive/verify_registry_test.go`
- `cmd/githive/whoami_test.go`
- `cmd/githive/wiki_conflict_classification_test.go`
- `cmd/githive/wiki_push_rejection_test.go`
- `cmd/githive/wiki_test.go`
- `cmd/githive/write_input_test.go`
- `internal/cliout/cliout_test.go`
