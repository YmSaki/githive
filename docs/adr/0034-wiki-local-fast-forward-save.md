# ADR-0034: Wiki 保存は検査したローカル tip からだけ fast-forward する

- 状態: 採用
- 対象: wiki の通常保存・競合解消後保存・`--no-sync`

## 背景

Wiki は通常の Git 履歴であり、編集中も外部の Git writer がローカル ref を
更新できる。保存直前に現在 tip を読み直して CAS の expected に渡すだけでは、
編集 worktree に含まれない他 writer のコミットを置換できる。
既存の通信失敗時ローカル保存には祖先検査があるが、通常の保存にはなかった。

## 決定

保存の最終 ref 更新は、読み取った現在 tip が worktree HEAD の祖先であることを
確認してから、その同じ tip を expected とする CAS で行う。ref 未作成の場合は
zero OID を expected とする。検査後の競合更新は CAS で拒否する。

祖先でなければ `LocalAdvanceConflictError` を返し、ローカル ref、編集コミット、
worktree とその config 記録を保持する。push や cleanup はしない。CLI は
`wiki_local_conflict`（exit 3、retryable false）と両 OID・worktree path を返す。
利用者は worktree で `refs/projects/wiki/main` を通常の Git merge により取り込み、
必要なら競合を解消して `githive wiki save` を再実行する。

## 帰結

自動的に別履歴を置換したり強制 push したりしない。既存の offline 保存、保存済み
編集の再開、Git による分岐解消を維持する。イベント形式・fold 意味論・永続化された
JSON identity は変更しない。全 writer の停止や ref の排他的所有を要求しない。
