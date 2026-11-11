# 現行機能の受け入れ・表明チェックリスト

2026-11-11 の提供範囲と、検証済み基点 `1a1dde2` の証拠を対応づける。登録・ソース実装・正常スモーク・異常系テスト・公開リリースは別の確認事項である。根拠は [配布物と CI の結果](acceptance/1a1dde2.json)、[準備状況](product-readiness.md)、`scripts/acceptance.py` と対象サービスのテスト。新たな変更の成功を旧基点から推定しない。

| 利用者へ提供する機能 | 現行の確認事項・制約 | 表明できる範囲 |
|---|---|---|
| インストール・更新・削除 | 小文字 module path のソース導入、固定 SHA の実導入、旧版からの更新と削除/再導入後の Issue/refs/config 保持を確認。 | Go ソース配布に対応。`@latest` が統合候補・安定版であるとは限らない。Homebrew・公開バイナリ・自動更新は未提供。 |
| Issue / Task / Chat | 通常 CLI と全登録 MCP ツールの作成・変更・再読込をスモーク。task 遷移、note、link 等はテスト対象。 | 登録済みの通常操作が固定版で成功。全オプション・全境界の保証ではない。chat 一覧は既定で archived も含む。 |
| 状態変更の監査 | 本変更は issue/task の status に、成功した追記の親状態から求めた `from` を付ける。並行書き込み・不正な既知遷移の監査も親状態と照合。 | 追加修正の回帰テストを根拠にする。1a1dde2 の配布物にはこの追加を含まない。reducer の意味論は変更しない。 |
| 通知 | 明示投稿・自動配送・一覧・ack を実行。notify ack は短縮 ID を解決。宛先の形式・空入力を追記前に検査。 | Git 同期時に配送する通知。push 時に常時配信するサーバーではない。副参照の共通 ID 解決は #129 の別範囲。 |
| users / Agent / policy | Agent profile と鍵・既存 identity 保護を検査。policy の形を保存前検査し、編集失敗の下書きを保持。 | CLI は `users policy edit` と明示 EDITOR、非対話保存は MCP `users_policy_set`。CLI `users policy set` は存在しない。Hosted policy は助言的。 |
| due 入力 | `YYYY-MM-DD` の正規表現を検査し、暦上の実在日までは確認しない（schema と docs/10 の現契約）。 | 日付の形を保持するフィールド。カレンダー計算・期限実行の機能や実在日の検証まで提供したとは言わない。 |
| Wiki | clone 後編集、save/show/log、push 拒否、未解消競合、worktree 後始末とデータ保持を検査。MCP は show/log と resource 読み出し。 | CLI の編集/保存と MCP の読み取り。MCP wiki edit/save は登録していない。 |
| sync / config | 収束・部分結果・通知・CAS と再試行を検査。同版の異なる config は両履歴を残す明示競合。 | 正常同期と既知の競合・失敗の処理。人が選ぶべき共有設定を自動選択しない。複数ホスティング実機の全面検証ではない。 |
| データ整合性 | `fsck` が event-sourced feature の head ツリーを再計算し、各 checkpoint state と照合。内容だけでなく Git tree の mode/構造も比較。 | #115 の現行再計算経路は fsck。途中の全コミット、meta/config、通常 Git ブランチの wiki を同じ reducer で全再計算する機能ではない。 |
| 暗号署名・台帳 | `verify` が署名・actor・台帳権限・信頼の根を検査。署名付き CLI/MCP の成功、unsigned の失敗を確認。 | 署名の成功と、fsck の実体化再計算の成功を分ける。`verify` 単独が正しいスナップショットを証明するとは言わない。 |
| MCP | 41ツールを登録・正常呼び出しし、4種 resource を読み出し。 | 正常経路の全登録ツールを実行済み。全パラメータ・負例の網羅ではない。 |

## 現在の通常利用に関係する追跡項目

- #87 の shallow snapshot 閲覧、#129 の副参照の短縮 ID、#88 の raw users のイベント依存順序は、担当する変更の統合・受け入れ証拠が揃うまで、それぞれの完成として表明しない。既に確認した完全履歴での通常経路とは区別する。
- #142 は status の from、users 一覧順、chat archived 既定の複合 Issue。今回の status 監査と chat の現挙動の明文化だけで users 一覧順も修正済みとはしない。
- policy 編集の入力喪失や、署名検証とツリー検証の混同は現機能に直結する。機能が存在しない CLI set や、verify が実行しない再計算を案内しない。

## 将来機能

Forge サーバー、pre-receive の権限強制・再計算、push 時のリアルタイム通知、Web UI、LFS、TUI/VS Code、通知 sharding、forge 連番は、対応する実装と検証を別途示すまで構想・将来範囲である。README のモード比較や設計文書の記載だけを現行機能の受け入れ証拠にしない。
