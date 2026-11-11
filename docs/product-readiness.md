# 製品準備状況

**未リリース・完成判定前。** 2026-11-11 時点の統合候補 `c3e3db24cb4de7ff0142b887141a43da20ea1f00` に対する作業記録である。候補で改善したこと、公開 main に取り込まれたこと、配布・最終受け入れの完了は別の状態として扱う。全機能の完成や、本番運用の包括的な安全性を宣言しない。

## 配布物と候補の区別

インストール検証で `go install github.com/ymsaki/githive/cmd/githive@latest` が取得したのは `v0.0.0-20260728142753-ff1ab6707ff9`。確認した公開 main も `ff1ab6707ff9fea8fda3aea200f726f89012c798` で、候補の修正は含まれない。`@latest` は安定版やこの統合候補を意味しない。導入後は `go version -m` で実際の版を確認する（[導入手順](installation.md)）。

現在提供している方法は Go によるソースからのインストール。公開リリースのバイナリ・Homebrew・自動更新は未提供であり、CLI は `githive`、MCP 起動は `githive mcp serve` である。`git hive` 用の shim は同梱しない。README の Forge 列は将来のサーバー設計であり、サーバー配布・pre-receive の権限強制・push 時の通知配信を実装済みとは扱わない。

## 来歴と確認済み範囲

採用した PR head、元の追加修正 commit と候補 commit の対応は [固定した来歴一覧](product-candidate-provenance.md) に記録する。一時作業領域にあった manifest と QA ログを整理した記録であり、一時パスや過去のバイナリを現候補の配布物として参照しない。

候補には init/sync/wiki の通常フロー、checkpoint の偽 state 対策、JSON の厳格化・容量/深さ制限、同一 ID 異内容の拒否、worktree 削除の保護、署名設定の真偽値解釈、部分結果の報告、通知配送、Agent identity/鍵の保護などの修正を取り込んでいる。ただし、各修正のテスト通過はその修正範囲の証拠であり、全広告機能の検証完了ではない。

| 対象 | 現候補での改善と残る制約 |
|---|---|
| [#102 config の合流](https://github.com/YmSaki/githive/issues/102) | 同版の異なる config は非 retryable な `meta_config_conflict` として両履歴を残して停止する。同じ設定は canonical 化して合流する。設定を自動で選ぶ機能ではなく、競合の解決は両親を残す明示操作を要する（[ADR-0025](adr/0025-meta-config-conflict.md)）。 |
| [#91 users の名前と外部イベント](https://github.com/YmSaki/githive/issues/91) | 正規書き込みの名前・enum 検証、user/group 衝突防止、危険なツリーパスの保存拒否を追加した。既存の raw イベントを fold から消す変更や、自動の台帳修復を実装したわけではない。外部・既存履歴に対する受理範囲と回復性まで解決済みとはしない。 |
| [#136 policy 編集](https://github.com/YmSaki/githive/issues/136) | 保存前の policy 検証、非対話 set、編集失敗時の下書き保持を追加した。Hosted の policy は助言的であり、Forge のサーバー側権限強制を提供するものではない。 |
| [#135 入力検証](https://github.com/YmSaki/githive/issues/135) | issue/task/chat の title/status 等、notify 宛先/source、users の名前・kind/status 等を共有の書き込み経路で検査する。全 JSON Schema の汎用 validator ではない。due は `YYYY-MM-DD` の形を確認し、暦上の実在日までは検査しない。source.kind の列挙・グループの存在確認も追加していない。 |

## 引き続き追跡する互換性・日常利用の条件

- **[#87 shallow 閲覧](https://github.com/YmSaki/githive/issues/87):** issue/task/chat の一覧は head ツリーを読めるが、Show 系・notify/users の一覧等は履歴 fold を使い、完全なイベント履歴が必要。depth 1 の issue List が表示できても Show は親オブジェクト不足で失敗する。snapshot 閲覧、自動 deepen、専用の復旧案内は未実装の課題として追跡する（[同期仕様](03-sync-and-concurrency.md#shallow--partial-clone-との関係)）。この現状説明を shallow 対応の実装完了や課題の取消しとは扱わない。
- **[#129 副参照 ID](https://github.com/YmSaki/githive/issues/129):** 主 ID の解決と notify ack は短縮 ID を受け付ける。link の参照先、reply_to、supersedes、notify source.id は現在、完全な小文字 ULID を要求する。これらの短縮 ID 解決は未実装であり、不正値の拒否は解決処理の実装と同義ではない。CLI/MCP の実際の制約は [入力仕様](10-cli-spec.md) に従う。「すべての ID が同じ短縮入力に対応する」とは保証しない。

このほか、部分的に破損した履歴の検査・一覧継続、fsck の全 data/feature/entity 検査、複数リモートの追跡意味等にも未検証・未完了の範囲がある。Issue が open であることと、特定の候補で緩和されたことを混同しない。

## テスト証拠の範囲と残るゲート

従前の製品スモーク QA はソース `bc9dd06898de19736707d78dbe6f5fbe255cc1ad`、バイナリ SHA256 `69593d5f53628410e52d06f401cccf21e6cc19c41fa7b546f40766a3a7e98b8a` を対象にした。CLI/Git の67コマンドと署名・policy 等の30コマンド、MCP の initialize/initialized/tools/list と22回の tool call（計25メッセージ）を記録している。**41ツールの登録を確認したことは、全41ツールの呼び出し・全オプションの検証ではない。** 25メッセージを25種類のツールの完全なカバレッジとも数えない。

その後に候補へ追加した修正を、この古いバイナリの QA 結果で合格にしない。最終ソースとバイナリの対応を固定した可搬的な CLI/MCP QA、必要な通常操作・競合/失敗時の検証、最終コミットに対する CI の確認とレビューが残っている。既存の Go/spec テストや途中コミットでの PASS は証拠として保持するが、未実行・対象外・想定違いのケースを PASS に換算しない。

## マージと完了の扱い

作業ルートと当該リポジトリを確認した範囲に、適用される `AGENTS.md` は見つからなかった。開発規約は [CLAUDE.md](../CLAUDE.md) と関連 rules を読んでいる。CLAUDE.md の開発ワークフローには、次の明文がある。

> **マージの実行は常に人間の承認を要求する設計**

この規約の下で、候補の作成・修正・レビュー・検証・PR 準備と、公開 main のマージ承認を分ける。最終証拠をそろえてレビュー可能にしたうえで承認を得る。公開タグ・リリース公開・インストール先への反映も別途確認する。現在の状態は、その承認・公開・最終受け入れを済ませたという意味ではない。
