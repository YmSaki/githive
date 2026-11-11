# ADR-0033: 台帳置換前の永続退避と書き込み・配送結果の分離

## 状態

採用。Refs #189、#107。

## 不変条件

ローカルへの書き込み成功と、その書き込みの現在 ref における保持、remote への配送は異なる事実である。同期が pin に従って別ルートの台帳を採用するとき、失われるローカル tip は reflog 設定・有効期限・GC に依存しない形で保持しなければならない。

## 決定

remote registry を採用する CAS の前に、旧 tip を `refs/githive-recovery/users/<old_oid>` に保存する。ref が無い場合は expected-zero の CAS で作成し、同じ OID を保存済みなら再利用する。違う OID、保存失敗、CAS 失敗なら採用しない。退避 ref は refs/projects の外に置き、自動 sync で送信しない。台帳の採用 CAS が競合した場合も並行 head を上書きせず、既に作った退避は残す。

SyncResult は local_oid/remote_oid/backup_ref/recovery を持つ。CLI/MCP の共通 result adapter は成功時の warning、部分配送、および published/local_write_preserved を保持する。MCP の書き込みは ADR-0011 どおり自動 sync しない。

CLI 自動 sync で直前の書き込みが current ref から外れた場合、local_write_replaced（終了1、retryable:false）とする。local_written:true と永続退避先を返し、無条件の再実行を促さない。通常の通信失敗でローカルの書き込みが保持される場合は、従来どおり書き込み成功と警告を返す。

## 範囲

退避は復旧用のローカル参照であり、第二の正本でも自動マージ入力でもない。無関係な信頼の根を自動で合流させず、reducer・イベント封筒・schema_version は変更しない。既存 reflog も変更・削除しない。
