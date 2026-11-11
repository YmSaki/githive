# 統合候補のコミット来歴（実装固定スナップショット）

Source: `9ea5809a02ce49c1e6c0bb56ac67947ef0cc3188`
Branch: `codex/product-quality-candidate`
Base: `ff1ab6707ff9fea8fda3aea200f726f89012c798`
Worktree: セッションの一時作業領域（公開配布物ではない）
Status: 追加の設定例 quoting 修正後の実装スナップショット。文書収録コミット自体は上記 Source に含まれない。最終配布 SHA と受け入れ検証は別途再固定する。

## Existing PR heads included

| PR | Included head | Remote ref |
|---|---|---|
| #163 | `2f165fc2f6142f1b8097e8096ed1a22ccc112878` | `origin/claude/project-thread-kao04e` |
| #164 | `f7df0d951bbf3d09c386e6b156d722d30ebc2283` | `origin/claude/project-thread-kao04e-78` |
| #165 | `14d663df94bad57598a0cb276eae55d1453bbc75` | `origin/claude/project-thread-kao04e-95` |
| #166 | `6cb59547c01b76d69cc8551af4881a3a2c000cce` | `origin/claude/project-thread-ndpz3t` |
| #167 | `62acf6888ae25543f7babb0c231ac47d146ae7f7` | `origin/claude/project-thread-kao04e-101` |
| #180 | `c6c3d4919a9a92835ad26a01a58bc43c5f5c619a` | `origin/claude/project-thread-wywys8` |
| #181 | `44a62f1ce0dc76ffc9e4edbc45a90c203b7dff96` | `origin/codex/fix-idgen-clock-rollback` |
| #182 | `6bbd714ffa28f54e6a71f67d34e9c9a52789d74f` | `origin/fix/pr180-missing-data-negative` |
| #183 | `84d49487e7c1ca223e84986c030ee67078d95090` | `origin/swarm/fix-pr166-tree-modes` |
| #185 | `ea91c04c1065c8be08f2b78b51d31f3eecc128a2` | `origin/codex/fix-init-stale-tracking` |
| #186 | `c5a20b7bffe514f7a3b8f5e09744766588e0bd64` | `origin/codex/fix-sync-config-retries` |

## Additional fixes included

| Scope | Original commit | Candidate commit |
|---|---|---|
| init: 新規の共有設定を自動同期して第二ルートを防ぐ (#175) | `ac55dbcdcd75bfd5bc77aadeca306c11fe148eb2` | `3ddc3a1e69b30a987c44542bf9c877a35ebc92f8` |
| entitychain: checkpoint の状態を親履歴から検証して偽状態の再署名を防ぐ | `c92d493551040f26d496390a7e8f7a5002815a62` | `954f5b474354b315fd3af21f05441c41db266cad` |
| wikiapp: 恒久的なpush拒否を競合として再試行しない | `fbfdbac2754071ead3c86728d625d9a9a998631d` | `39b8df91a1207f03da69f01d9ee3fbc0b5f5e5f2` |
| cli/mcp: sync の部分結果と警告を失敗時にも返す (#108) | `fcd8302b01a62fda89198720b43c317fa3cea90b` | `7edbdfb5d4917ed15e0f7d10966f0dfe4ba81332` |
| docs: 実際のインストールとデータを保持する更新・削除手順を追加する | `675f2c0e3675675365e9c5a5d01e0c3f31b040ba` | `4d70e25bb14ed5293496e5daee660642a4c1388a` |
| MCP: wiki の読み取り専用リソースを公開する | `323a59a0a277c85bb4d27dcd4883e0ea3e333a11` | `5b6f2cce53ea376536f9e8926ec1a0af4ff14931` |
| wiki: 所有していない worktree と親ディレクトリの削除を防ぐ (#123) | `97c76fd921f1312f4a3f6b479cfc08b9f5288ef3` | `e36d6637c16bd032779bd0092967f0fc26c8ff06` |
| plugin: clone後の取得とidentity確認を案内する (#161) | `0c8a2d75883bbc430d25af21fd77a2b37c3be6b2` | `af6bd65eb17e6758a289ed7ac0377d049453a23e` |
| config: Gitの真偽値解釈を使い静かな無署名化を防止 (#138) | `220a54c5a8dc17fd1e70cd0e3bd40e69c7078e09` | `adb3c266d2c0616e9b5a4e9c7f9595696c06c6e1` |
| mcp: フィルタの説明をusername解決に合わせる (#161) | `f8c75e25f68ccb8ab70981e022264e9edd997318` | `cbdc10c018810bd35d109c0995826fa85770e32b` |
| chain: 不正パスと衝突を拒否し Git のツリー順で保存する | `ed9d436fbfdbfd234411095a6cc8b7d39035849d` | `3bfb7c0a4e5bd93ebaa14aef6743ef70e038ca86` |
| wikiapp: clone直後の編集をリモートwikiの履歴から開始 | `d3863afb895d2ebf6bd23ddf01df894d93797f0a` | `b885fb074828f54432db501a6d3d5dd89ee92e74` |
| notify: ack の短縮 ID を投稿に解決して不明な対象を拒否する | `0bcfedc8924f123cdb33f2dafc2a18e61ed4a971` | `4b4c5eb7c476a6f5eb448329af70da5e8d5c08fc` |
| wikiapp: 未解消の競合を再保存してもstagingせず拒否 | `4d34fdd6e90ce91d0ee3cf1c1740ce5691b98c74` | `4b727089cdb847bd26e008eb673426b4c773be58` |
| event: 厳格な JSON 検証と容量・深さ制限で不正イベントの保存を防ぐ | `31eeb8a6a53b342775ade7e1599c455c753f5c54` | `974d664dfaa9c92aa6a6cabf20555f7c7a816bc3` |
| chain: イベント検証をツリー保存より先に行い封筒欠落を拒否する | `70fcbdd59fa4186c33b45c338d9c32e6f6c68d5e` | `71b1bbe8bdf5b8c90441f3388df7de87c4c0066e` |
| usersapp: Agent 作成時の既存 identity と鍵を保護し名前衝突を拒否する | `7e487ad149eb1a9adb30d783e8d3e0f3863347ea` | `ed4f859dbfc03082a4b7ab3e17b1129ce050d7d4` |
| core: 同一イベント ID の内容衝突を破損として拒否する (#81) | `93fb692fdc07c75a9fe511b1fefe8c40dfbfe074` | `1aa12ba0accfaed900457132f583e6045bdad121` |
| app: issue task chat の不正入力を追記前に拒否する | `9fc1fa26eb6d8a7eaba625384967d49b843cecc2` | `aa4566b42db2c21ad892a44c2091f5c90054e173` |
| notify: 書き込みに伴う通知も既定の同期で届ける (#144) | `3b097b9bdf5dcb99079883201538c302d2043753` | `cac7eed9f09bc22d69f11d05d6e88add69129d23` |
| syncapp: ローカルCAS競合を再計画し書き込みロックを共有 | `686431da9fbe69f051004233a3f62240bf88c92d` | `8faf3c53ea56acd59f42c8ead4c3e75beca8528f` |
| users: policy の保存前検証と編集失敗時の下書き保持を追加する | `e1f622ba98cff50eb54681736a78578831a9eaf8` | `58e920ac9e5acb470dd84ae1ebddb3c44e87d198` |
| syncapp: 同版の共有設定の衝突で両履歴を保持して停止する | `b1d8fd73d64d7f92957802394cc4fd02813e3e0c` | `d8c799d82b34d0931004f9c47359801fc811fa6a` |
| cli/mcp: 共有設定の衝突を両履歴と復旧手順付きで報告する | `4c8825c311466e531623b3ca39b697124af0bed3` | `4c8825c311466e531623b3ca39b697124af0bed3` |
| app: notify users の不正入力と危険な台帳名を拒否する | `87d0657dab9719471ce028a446dae741f67a4bdd` | `5e0cfd11f0ab71ed28b0ca59c6b05d21b25985e8` |
| users: Agent作成で指定したdisplayとroleを保持する (#139) | `d21ef1dbbd3ec5db6f49b49b364f1228797aeceb` | `4ba9d4fa277d162e1a23d24887199eaec625e1fd` |
| mcp: Agentプロフィールと設定情報をCLIと揃える (#139) | `8293e5a6c3d8d0db7de36892d4efcd2f221644c0` | `c3e3db24cb4de7ff0142b887141a43da20ea1f00` |
| docs: 統合候補の来歴と未完了の受け入れ条件を明記する | `c64fd325ff5698eb42b5f764a4cd6e0613a5b841` | `86714fde94276381957db3219441656213d15aff` |
| qa: 固定バイナリを対象に CLI と全 MCP ツールを試す受け入れスクリプトを追加する | `8185d8d6b75db8501794814bc23db0fdde8d9139` | `9c651ff910bf961378c24bd3b186106c3278031f` |
| entitychain: 時計ずれで消える変更とcreate置換を境界で拒否 | `311c85683330ef356878259a546e4b98a15dace6` | `3d42987c7b937bed8502be71bc3cf41d36cd029b` |
| docs: 最終実装の来歴とイベント依存順序の残課題を更新する | `fe77a322834b8aaf82172bbfd9ceb67c2733b5c6` | `fe77a322834b8aaf82172bbfd9ceb67c2733b5c6` |
| users: Agent設定例をシェル展開から保護する (#139) | `9edfc2b1c174991d6a566507fbf40c70147f8ffc` | `9ea5809a02ce49c1e6c0bb56ac67947ef0cc3188` |

## Integration resolutions

- init CLI preserves remote status/adopted refs/warnings and JSON encoding error propagation.
- Shared private fetch preserves remote validation and places --prune before --end-of-options.
- Registry verification test helper type renamed to avoid duplicate declaration across PRs.
- Strict JSON and conflicting event-ID checks, CLI classifications, documentation, and executable spec validations all retained together.
- Write-data validation runs before folding; the pending envelope remains supplied to conflicting-ID validation.
- CLI sync failures retain partial results, including when classifying invalid input or other nested errors.
- Excluded #168 (future Forge ADR) and #179/#184 (relay feature) from ordinary client candidate.

## Validation evidence before final freeze

- Baseline bc9dd06: full Go suite/vet/build, spec179, format/import lint PASS.
- Follow-up af6bd65: full Go suite/vet/build, spec179, format/import lint PASS.
- Follow-up b885fb0: gitx/wiki/entitychain/chain interaction tests PASS.
- Follow-up 1aa12ba: all-package compilation/API sweep PASS; combined strict JSON + event identity spec197/197 PASS.
- Follow-up cac7eed: input/empty-edit/conflict/notification interaction tests PASS.
- Follow-up 4c8825c: full Go suite/vet, spec197/197, format/import lint/diff checks PASS.
- Build/test uses GOFLAGS=-buildvcs=false because outer /workspace/.git breaks automatic Go VCS stamping.
- 旧候補 fe77a32 はレビュー用 [PR #187](https://github.com/YmSaki/githive/pull/187) に公開済み。追加修正後は同じ PR に追記し、固定版を再検証する。公開 main のマージ・タグ/リリース公開・人間の承認が済んだという意味ではない。

## 固定版検証後の追加修正

旧 fe77a32 の配布物検証で見つかった Agent 設定例の shell 展開を追加修正した。旧版の QA を新候補の承認に流用せず、最終コミットで全体ゲートと固定版受け入れを再実行する。
