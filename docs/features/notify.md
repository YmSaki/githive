# 機能仕様：notify 管理

## 目的

対象（ユーザー、グループ、全員）を指定した通知イベントを蓄積し、各自が fetch 時に自分宛ての未読を検出できるようにする。
hosted モードでは「配信」は存在せず、pull 型（fetch して読む）で動く。
forge モードではサーバーが push 時に WebHook やメールへ配信する（[12](../12-forge-server.md)）。

## ref とツリー

```
refs/projects/notify/stream           # MVP は単一ストリーム

tree:
  meta.json                           # ストリーム情報（シャード世代など）
  events/
    2026-07.jsonl                     # 月ごとの通知一覧（1 行 1 通知、ULID 順）
  acks.json                           # 既読の集約 { "<notify-event-id>": ["user", ...] }
```

`events/*.jsonl` と `acks.json` は fold が生成する実体化ビューである。
正はコミットメッセージのイベントログにある。

## イベント定義

| kind | data | fold 規則 |
|------|------|-----------|
| notify.post | targets[], title, body?, source?, priority? | 当月の jsonl に追記 |
| notify.ack | ack_of（notify.post の event-id） | acks.json の該当エントリに actor を追加 |
| notify.checkpoint | state, count, hash | 読み出し起点 |

### notify.post の data

```json
{
  "targets": ["user:yuumiya@example.com", "group:core", "all"],
  "title": "task 01j8xt5e が done になりました",
  "body": "レビューをお願いします。",
  "source": { "kind": "task", "id": "01j8xt5e6f7g8h9j0k1a2b3c4d" },
  "priority": "normal"
}
```

- **targets**：`user:<email>`、`group:<name>`、`all` の配列。user の識別子は identity（[ADR-0009](../adr/0009-identity-user-email.md)）で、CLI 入力では username も受け付けて書き込み時に解決する。グループ展開は読み手が users 台帳を参照して行う。
- **source**：通知の発生元エンティティ。CLI はここからジャンプできる。
- **priority**：`low` / `normal` / `high`。表示順制御のみで意味論は持たない。

## 自動通知

issue や task の操作が通知を「自動生成」する仕組みは、コア層には置かない。
CLI の書き込みコマンドが、設定（`githive.notify.auto = true`、既定 true）に応じて notify.post を併発する。
自動生成する契機は次に限る：issue.assign（新規担当者宛て）、task.reassign（新 owner 宛て）、issue.comment の reply_to（返信先の author 宛て）、task.status の done 遷移（task の created_by 宛て）。
すべての変更を通知にすると流量が実用に耐えないため、明示的な宛先が存在する操作だけを対象とする。

## 未読の判定

自分宛て（直接指定、所属グループ、all）の notify.post のうち、自分の ack が無いものが未読である。
`githive notify list --unread` がこれを計算する。
ack は義務ではない。未 ack のまま放置しても他者には影響しない。

## シャーディング（将来）

push 競合が問題になる規模では、書き込み先を `refs/projects/notify/<yyyy-mm>` に切り替える。

- 読み手は `refs/projects/notify/` 配下の全 ref を対象とする（stream と月次シャードの混在を許す）。
- 書き手は meta/config の `notify_sharding: "monthly"` を見て書き込み先を選ぶ。
- イベント形式は変わらないため、移行はデータ変換なしで行える。

## CLI

```
githive notify post --to user:x --to group:y --title <t> [-m body] [--source kind:id]
githive notify list [--unread] [--all] [--json]
githive notify ack <event-id>...
githive notify ack --all
```

## 素の git での読み方

```
git show refs/projects/notify/stream:events/2026-07.jsonl   # 当月の通知一覧
git show refs/projects/notify/stream:acks.json              # 既読状況
```

jsonl は 1 行 1 通知なので grep で自分宛てを拾える（`grep 'user:yuumiya@example.com' ...`）。

## 設計判断

- 既読管理を各ユーザーのローカルに置く案は退けた。既読が共有されないと「誰も気付いていない通知」をチームで検出できないためである。
- acks.json が肥大する懸念には、チェックポイント時に「全員 ack 済みかつ 90 日経過」のエントリを間引く（イベントログには残る）ことで対処する。
