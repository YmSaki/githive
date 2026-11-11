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
| [#135 入力検証](https://github.com/YmSaki/githive/issues/135) | issue/task/chat の title/status 等、notify 宛先/source、users の名前・kind/status 等を共有の書き込み経路で検査する。全 JSON Schema の汎用 validator ではない。due は `YYYY-MM-DD` の形を確認し、暦上の実在日までは検査しない。source.kind の列挙・グループの存在確認も追加していない。 |

## 引き続き追跡する互換性・日常利用の条件

- **[#87 shallow 閲覧](https://github.com/YmSaki/githive/issues/87):** issue/task/chat の一覧は head ツリーを読めるが、Show 系・notify/users の一覧等は履歴 fold を使い、完全なイベント履歴が必要。depth 1 の issue List が表示できても Show は親オブジェクト不足で失敗する。snapshot 閲覧、自動 deepen、専用の復旧案内は未実装の課題として追跡する（[同期仕様](03-sync-and-concurrency.md#shallow--partial-clone-との関係)）。この現状説明を shallow 対応の実装完了や課題の取消しとは扱わない。
- **[#88 イベント依存順序](https://github.com/YmSaki/githive/issues/88):** 通常追記は既知の最大非 checkpoint ID 以下の新イベントを `clock_skew` で拒否し、raw な issue/task/chat 履歴の create より前の変更・別 create による置換は `event_order_violation` で拒否する。reducer や既存イベントを書き換える対策ではない。raw users の key_add/key_revoke と user_set の依存順序・鍵ローテーション全体の検証は未実装であり、#88 は残課題を含む（[ADR-0026](adr/0026-reject-lossy-event-order.md)）。
- **[#129 副参照 ID](https://github.com/YmSaki/githive/issues/129):** link の参照先、reply_to、supersedes、既知 feature の notify source.id も app 層で前方一致と大文字入力を解決し、完全な小文字 ID を保存する。CLI/MCP で add/remove の同一性、返信通知、未知・曖昧・他エンティティの拒否と追記なしを検証する。完全 ID の弱いリンクや任意 source.kind の完全 ID は既存のオフライン・拡張用契約として保持する。未知 source.kind の短縮入力は完全 ID を要求する。詳細は [入力仕様](10-cli-spec.md) に従う。

このほか、部分的に破損した履歴の検査・一覧継続、fsck の全 data/feature/entity 検査、複数リモートの追跡意味等にも未検証・未完了の範囲がある。Issue が open であることと、特定の候補で緩和されたことを混同しない。

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
