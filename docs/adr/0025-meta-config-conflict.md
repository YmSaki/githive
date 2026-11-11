# ADR-0025: 同版の異なる meta/config を自動選択しない

- 状態：採用
- 日付：2026-11-11
- 関連：issue #102、ADR-0017 §2 を部分的に置換

## 文脈

ADR-0017 の「同じ schema_version なら local」は、同じ2つの head を逆方向にマージしたとき別の project/features を採用する。init の初回 fetch は二重ルートの発生を減らすが、既存・オフラインの別ルートは残る。どの project を優先すべきかを自動で決める根拠はない。

## 決定

- 異なる schema_version は従来どおり大きい側を採る。sync 前の対応版検査も維持する。
- 同じ schema_version なら両方の config.json を canonical 化して比較する。同じ値なら同じ canonical bytes を使う。空白やキー順だけの違いは競合にしない。
- 同版で値が違えば `MetaConfigConflictError`（`meta_config_conflict`、非 retryable）で停止する。対象 meta ref とリモート ref は進めない。fetch 済みの追跡 ref と両方の履歴を残す。他の ref の部分的成功は既存どおり報告する。
- 解決する人が両方の config を比較して設定を選び、両 head を親に持つ merge commit を作る。同じ sync の再試行で勝者を決めたり、force push や reset で片方の履歴を消したりしない。
- 正当な reducer の fold 規則は変わらず、config の自動選択を明示的な衝突に置き換えるため、スキーマ版は昇格しない。

## 復旧手順

エラーの local_oid と remote_oid を、それぞれ以下の変数に設定する。両方を `git show` できることを確認し、追跡 ref が動いても同じ OID を比較する。

```sh
local=<local_oid>
remote=<remote_oid>
git show "$local:config.json"
git show "$remote:config.json"
mkdir -p work/meta-resolution
git show "$local:config.json" > work/meta-resolution/config.json
```

`work/meta-resolution/config.json` を編集して project/features 等の採用値を決める。schema_version は両側の最大値を維持する。ツールが対応しない版なら先にツールを更新する。JSON は通常の config と同じ canonical 形式（キー順・2スペース・末尾LF）に整え、他の人が確認できる形で内容をレビューする。

確認後に次を実行する。署名が必要な運用では設定済みの署名方法に従って `git commit-tree` に `-S` を付ける。

```sh
blob=$(git hash-object -w work/meta-resolution/config.json)
tree=$(printf '100644 blob %s\tconfig.json\n' "$blob" | git mktree)
merge=$(git commit-tree "$tree" -p "$local" -p "$remote" -m 'merge: resolve meta/config conflict')
git update-ref refs/projects/meta/config "$merge" "$local"
githive sync
```

`update-ref` は旧 OID を条件にする。並行変更で失敗したら新 head を調べ直し、既存の解決を上書きしない。この merge は両履歴を祖先に保ち、通常の fast-forward push で届けられる。自動の設定編集・解決コマンドは現時点では設けない。
