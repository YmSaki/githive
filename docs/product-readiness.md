# 製品準備状況

**未リリース・完成判定前。** 2026-11-11 時点の検証済み配布基点 `1a1dde2de203d60a4dff0682aaae7a70dac73c5f` と、それ以後の追加修正を分けて記録する。候補で改善したこと、公開 main に取り込まれたこと、配布・最終受け入れの完了は別の状態として扱う。全機能の完成や、本番運用の包括的な安全性を宣言しない。

## 配布物と候補の区別

インストール検証で `go install github.com/ymsaki/githive/cmd/githive@latest` が取得したのは `v0.0.0-20260728142753-ff1ab6707ff9`。確認した公開 main も `ff1ab6707ff9fea8fda3aea200f726f89012c798` で、候補の修正は含まれない。`@latest` は安定版やこの統合候補を意味しない。導入後は `go version -m` で実際の版を確認する（[導入手順](installation.md)）。

現在提供している方法は Go によるソースからのインストール。公開リリースのバイナリ・Homebrew・自動更新は未提供であり、CLI は `githive`、MCP 起動は `githive mcp serve` である。`git hive` 用の shim は同梱しない。README の Forge 列は将来のサーバー設計であり、サーバー配布・pre-receive の権限強制・push 時の通知配信を実装済みとは扱わない。

## 来歴と確認済み範囲

採用した PR head、元の追加修正 commit と候補 commit の対応は [固定した来歴一覧](product-candidate-provenance.md) に記録する。文書更新後の最終配布対象コミットとバイナリは PR・QA の来歴で別途固定する。一時作業領域にあった manifest と QA ログを整理した記録であり、一時パスや過去のバイナリを現候補の配布物として参照しない。

候補には init/sync/wiki の通常フロー、checkpoint の偽 state 対策、JSON の厳格化・容量/深さ制限、同一 ID 異内容の拒否、worktree 削除の保護、署名設定の真偽値解釈、部分結果の報告、通知配送、Agent identity/鍵の保護、時計ずれで実体化から消える追記と create 置換の拒否などの修正を取り込んでいる。ただし、各修正のテスト通過はその修正範囲の証拠であり、全広告機能の検証完了ではない。

| 対象 | 現候補での改善と残る制約 |
|---|---|
| [#102 config の合流](https://github.com/YmSaki/githive/issues/102) | 同版の異なる config は非 retryable な `meta_config_conflict` として両履歴を残して停止する。同じ設定は canonical 化して合流する。設定を自動で選ぶ機能ではなく、競合の解決は両親を残す明示操作を要する（[ADR-0025](adr/0025-meta-config-conflict.md)）。 |
| [#91 users の名前と外部イベント](https://github.com/YmSaki/githive/issues/91) | 正規書き込みの名前・enum 検証、user/group 衝突防止、危険なツリーパスの保存拒否を追加した。既存の raw イベントを fold から消す変更や、自動の台帳修復を実装したわけではない。外部・既存履歴に対する受理範囲と回復性まで解決済みとはしない。 |
| [#136 policy 編集](https://github.com/YmSaki/githive/issues/136) | 保存前の policy 検証と編集失敗時の下書き保持を追加した。CLI は `users policy edit`（EDITOR の明示設定が必要）、非対話の保存は MCP の `users_policy_set` を使う。`githive users policy set` という CLI は現在存在しない。Hosted の policy は助言的であり、Forge のサーバー側権限強制を提供するものではない。 |
| [#135 入力検証](https://github.com/YmSaki/githive/issues/135) | issue/task/chat の title/status 等、notify 宛先/source、users の名前・kind/status 等を共有の書き込み経路で検査する。既知 kind の data は同梱した権威スキーマでも検査し、外部履歴の feature/entity 文脈を確認する。未知 kind は将来拡張として警告付きで保持する。due は `YYYY-MM-DD` の形を確認し、暦上の実在日までは検査しない。source.kind の列挙・グループの存在確認も追加していない。 |

## 引き続き追跡する互換性・日常利用の条件

- **[#87 shallow 閲覧](https://github.com/YmSaki/githive/issues/87):** issue/task/chat の詳細と一覧、notify/users/status/whoami に閲覧専用 snapshot view を追加した。不完全な履歴では `read_source: snapshot`、`history_complete: false`、`verified: false` を返す。保存済み Markdown の LF を保持し、元イベント本文の完全復元とは扱わない。書き込み・権限判定・log/verify/fsck/compact は完全履歴を要求し、`shallow_history` と復旧案内を返す。自動 fetch/deepen は行わない（[ADR-0028](adr/0028-shallow-read-views.md)）。
- **[#88 イベント依存順序](https://github.com/YmSaki/githive/issues/88):** 通常追記は既知の最大非 checkpoint ID 以下の新イベントを `clock_skew` で拒否する。raw issue/task/chat の create 前変更・別 create による置換に加え、users の user_set 前の鍵操作、対応する key_add より前の revoke、同じ鍵素材の文字列表現差による失効空振りを受理境界で拒否する。既存の不正履歴は隔離し、削除・書換え・自動修復しない。reducer と schema version は変更していない（[ADR-0026](adr/0026-reject-lossy-event-order.md)、[ADR-0027](adr/0027-reject-ignored-users-key-events.md)）。
- **[#129 副参照 ID](https://github.com/YmSaki/githive/issues/129):** link の参照先、reply_to、supersedes、既知 feature の notify source.id も app 層で前方一致と大文字入力を解決し、完全な小文字 ID を保存する。CLI/MCP で add/remove の同一性、返信通知、未知・曖昧・他エンティティの拒否と追記なしを検証する。完全 ID の弱いリンクや任意 source.kind の完全 ID は既存のオフライン・拡張用契約として保持する。未知 source.kind の短縮入力は完全 ID を要求する。詳細は [入力仕様](10-cli-spec.md) に従う。

- **[#130 一覧の表示 ID](https://github.com/YmSaki/githive/issues/130):** 人間向け issue/task/chat list の ID 列は、同 feature の全ローカル ref を対象に一意な最短の前方一致を表示する。非表示・archived の競合も含め、コピーした ID で show が同じエンティティを開くことを検証する。show のコメント・note・message ヘッダも一意なイベント ID を表示し、chat --tail の表示外競合も含む。初回本文付き chat.create の ID も返信・置換に使えることを CLI/MCP で検証する。JSON は完全 ID のまま。通知タイトルの装飾的短縮や notify/status/log の他の表示列まで対応済みとは扱わない（[ADR-0031](adr/0031-unambiguous-entity-list-ids.md)）。

部分破損でも verify は他 ref の検査結果を保持し、fsck/verify と同期・初期化は既知 data と feature/entity を検査する。破損した一覧の継続、verify の全警告分類、複数リモートの追跡意味等には未完了の範囲がある。Issue が open であることと、特定の候補で緩和されたことを混同しない。

## 検証済み配布基点と後続変更

固定 SHA `1a1dde2de203d60a4dff0682aaae7a70dac73c5f` を公開 Go module から実際にインストールし、`v0.0.0-20261111130129-1a1dde2de203`、Linux amd64 / Go 1.25.4、バイナリ SHA256 `633a1f88a1a5731a51180e1544de371e07f366bce7f08c338e6ac0ef886bbc71` を確認した。受け入れ試験前後のバイナリハッシュは一致する。

この配布物に対する `scripts/acceptance.py` の実行は **101コマンド、56 JSON-RPC、登録された全41ツールの正常呼び出し、Issue/Task/Chat/Wiki の4種 resource 読み出しが成功、失敗0**。署名付き CLI/MCP の書き込みと verify、旧公開版からの更新・実行ファイル削除・再インストール後の Issue/refs/config の保持、Agent 設定例の shell 展開防止も追加確認した。公開可能な要約は [固定した結果](acceptance/1a1dde2.json) に保存する。試験用秘密鍵を含む raw fixture は公開しない。

同じ SHA の [CI 実行](https://github.com/YmSaki/githive/actions/runs/43025638502) は Ubuntu・Windows・spec job が成功した。197件の spec、Go test/vet/build と主要コアの race 検査も、この基点の証拠として記録されている。旧候補の Windows 補助実行・設定例の shell quoting の失敗は修正後に再検証済みであり、現在の同 SHA の失敗として記述しない。

以前の bc9dd06 に対する「41ツール登録、22 tool calls / 計25メッセージ」は過去の限定的なスモーク結果である。現在の全41ツールの正常呼び出しの証拠と区別し、件数を足したり古い不足を現在の不足として扱ったりしない。ただし、全ツールを一度正常に呼んだことは、全フラグ・境界値・権限攻撃・競合・OS・ホスティング実機を網羅した意味ではない。macOS と実際の GitHub/GitLab/Gitea への project refs 配送は、この実行の確認範囲外である。

この基点以後に追加する status の監査用 from 等の変更には、対応する新しいソースでローカル回帰テストを実行する。**1a1dde2 の配布物・CI 成功を後続コミットの成功証拠にはしない。** 最終統合後の配布物と CI の再確認は、対象 SHA とともに追記する。現行機能と将来機能の具体的な受け入れ範囲は [機能別チェックリスト](product-acceptance.md) を参照。

## マージと完了の扱い

作業ルートと当該リポジトリを確認した範囲に、適用される `AGENTS.md` は見つからなかった。開発規約は [CLAUDE.md](../CLAUDE.md) と関連 rules を読んでいる。CLAUDE.md の開発ワークフローには、次の明文がある。

> **マージの実行は常に人間の承認を要求する設計**

この規約の下で、候補の作成・修正・レビュー・検証・PR 準備と、公開 main のマージ承認を分ける。最終証拠をそろえてレビュー可能にしたうえで承認を得る。公開タグ・リリース公開・インストール先への反映も別途確認する。現在の状態は、その承認・公開・最終受け入れを済ませたという意味ではない。

## 第二波の独立検証と監査

固定配布物 `1db77fc` の新しい導入・更新/削除/再導入・全41ツールの成功記録は [受け入れ要約](acceptance/1db77fc.json)。[実GitHubの配送](acceptance/c5da0be-hosted.md) と [最低版Git2.34](acceptance/c5da0be-git-2.34.md) は、それぞれ旧基点 c5da0be に固定した別の証拠である。最終差分の成功へ流用しない。

候補は追加で、台帳の置換前 recovery ref、成功時の同期警告・部分配送結果、壊れた設定版の拒否、Wiki の別writer履歴を失わない祖先確認、本文ファイル末尾改行の保持を含む。最終 head の CI/配布物は PR #187 へ対象 SHA とともに記録する。

[補償コード監査と再設計案](audits/2026-11-11/README.md) は現行の不具合修正と分離した未採用の提案である。実装しないままアーキテクチャ変更済みと数えない。
