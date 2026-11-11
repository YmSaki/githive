# ADR-0027: users の鍵操作が fold で無視される履歴を受け入れない

## 状態

採用（Issue #88 の users 鍵依存順序。ADR-0026 の残課題を閉じる）

## 背景

users の reducer は ID 順で user_set より前の key_add/key_revoke、登録されていない鍵の revoke を無視する。また pub の照合は完全一致なので、コメントだけが違う revoke は失効を記録せず、同じ鍵材のコメント違い add は別の鍵エントリを作る。署名が正しくても、この履歴を黙って採用すると、利用者が失効したつもりの鍵を有効なまま扱う危険がある。

## 決定

保存・読み取り・取り込みの境界で、ID 順の完全な users イベント集合に次を要求する。reducer 自体、イベントの ID、pub 文字列や既存コミットは変更しない。

1. key_add/key_revoke の username は、それより前の ID の user_set で登録されていなければならない。
2. key_revoke は、同じ username のそれより前の key_add を参照し、その pub 文字列と完全一致しなければならない。未知の鍵・登録前の revoke・コメントや空白だけが異なる revoke は拒否する。通常の KeyRevoke API は従来どおり鍵材で入力を解決し、登録済みの文字列をイベントに書く。
3. 同じ username で同じ鍵材を持つ異なる pub 文字列の key_add は拒否する。鍵材は既存の users API と同じ「先頭2フィールド（型と base64 token）、コメント除外」で比較する。単一 token の旧形式はその token 全体を identity とする。この規則は暗号鍵の構文検証を新設するものではない。
4. 同じ ID の同一 canonical payload のコピー、同じ pub 文字列の繰り返し add、既に失効した同じ pub の繰り返し revoke は受け入れる。reducer の既存の冪等性を維持する。失効した pub の再 add は有効化しない。
5. user_set による表示名・ロール等の更新は鍵の登録状態をリセットしない。同じ鍵材を別 username が使うことの可否は既存のポリシーを維持し、この変更で全ユーザー横断の一意制約は追加しない。
6. checkpoint は依存関係の証拠にせず、覆われた履歴を含むイベント全体を検査する。同一 ID の異なる payload は、従来どおり `conflicting_event_id` を先に返す。

違反は `event_order_violation` として、read/sync/init の台帳採用前に拒否し、fsck/verify でも明示する。完全な履歴のない断片を推測で補わず、イベントを自動的に正規化・並べ替え・書き換えない。取得した Git オブジェクトは残るが、検証に失敗した台帳を正常なローカル ref や署名信頼状態へ採用しない。

## 互換性と検証

同じ有効なイベント集合から生成されるツリーは変更しない。Go/Python の fold と既存の fold ベクタは維持し、schema_version は昇格しない。無視される revoke や同一鍵材の別 pub を含む旧履歴は、新しい受け入れ規則では拒否される。元の履歴を診断対象として残し、単にテストを通すために削除・正規化しない。

Go/Python で同じ domain-validation ベクタを検査し、raw Git の履歴から read/sync/verify/fsck/init が一貫して失敗すること、正当なローテーションと冪等な失効が受け入れられることを確認する。
