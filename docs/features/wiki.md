# 機能仕様：wiki 管理

## 目的

構造化された長寿命の文書（設計資料、手順書、用語集）を置く場所を提供する。
issue や chat が「出来事の記録」であるのに対し、wiki は「現在正しい知識」を置く。

## ref とツリー

```
refs/projects/wiki/main

tree:（自由な markdown ツリー。例）
  Home.md
  design/
    sync.md
  ops/
    release.md
  _assets/            # 小さな画像等。バイナリは 1 MiB 上限（超えるものは LFS 対応まで不可）
```

`_assets/` 配下のバイナリを `githive wiki show --json` で読むときは、`content` ではなく `content_base64` が返る。
canonical JSON は UTF-8 であり（[02](../02-data-model.md)「canonical JSON」）、不正なバイト列は U+FFFD に置換されてしまうため、有効な UTF-8 でない内容は base64 で返し `content` を省略する。
`content` が存在すれば必ず元のバイト列そのものであり、壊れた内容が `content` から読めることはない。
生バイトが欲しい場合は `--json` を付けずに `githive wiki show <path>` を使う（標準出力にそのまま書き出す）。

`Home.md` を入口ページの規約とする。

## 他機能との違い：イベントソーシングを使わない

wiki は通常の git ブランチと同じ運用とする。

- コミットは自由な編集（複数ファイル変更可）で、イベント封筒を持たない。
- 分岐の解決は通常の git マージで行う。テキストの意味的な衝突は人間が解決する。

イベント方式を使わない理由は、wiki の価値が「ファイルツリーの直接編集」にあり、編集操作をイベント語彙に翻訳すると表現力を失うためである。
event-union マージの自動解決も、散文の同時編集には適用できない（どちらの文も「勝ち」でよいかを機械が判定できない）。
代わりに git が本来持つ 3-way マージをそのまま使う。

## 編集フロー

CLI は一時 worktree を使った編集を補助する。

```
githive wiki edit                 # worktree を作成しエディタまたはパスを提示
githive wiki save -m "更新内容"    # commit + sync + worktree 掃除
githive wiki show <path>          # worktree なしで閲覧
githive wiki log [<path>]
```

初回 clone にローカル wiki ref が無い場合、`wiki edit` は `--remote`（既定 origin）の wiki ref を取得して編集を開始する。ソースコードのデフォルトブランチや remote HEAD の名前には依存しない。`--no-sync` または取得失敗時は、取得済み追跡 ref があればそれを使い、無ければ固定の空ルートを使う（[ADR-0022](../adr/0022-wiki-initial-edit-base.md)）。

`githive wiki edit --keep` で worktree を残し、通常の git 操作（`git -C <worktree> ...`）で作業してもよい。
競合したら `githive wiki save` が通常の git コンフリクトマーカーを提示し、解決後の再実行で完了する。

`githive wiki edit` は編集中の worktree を1つだけ記録する（`githive.wiki.worktree` という git config キー）。
このキーは githive が管理する値であり、通常は `wiki edit` が絶対パスを設定する。
手動設定した相対パスはリポジトリのディレクトリを基準に解決する（実行プロセスの作業ディレクトリには依存しない）。
自動削除するのは、githive が一時ディレクトリに作成し、所有マーカーが同じリポジトリを示す worktree だけである。
手動で作った worktree や所有マーカーのない旧版の worktree を設定した場合、save 成功後はこのキーを解除するが、
worktree とその親・兄弟のファイルは削除しない。必要なら利用者が `git worktree remove` で片付ける。
前回の `wiki edit` を `wiki save` せずに再度 `wiki edit` を実行すると、記録済みの worktree がまだディスク上に存在する限り
`ErrWorktreeExists`（`--json` では `error.code: "wiki_worktree_exists"`、終了コード 1）で拒否され、新しい worktree は作られない。
前回の worktree を放置したまま2つ目を作って古い方を参照不能にする（worktree 登録とディスクの両方がリークする）ことを避けるためである。
先に `githive wiki save` を実行するか、明示的に破棄する（`git -C <repo> worktree remove --force <worktree>` の後
`git -C <repo> config --unset githive.wiki.worktree`）こと。
なお、記録済みの worktree が（`wiki save` を経由せず）人手で既に削除されている場合は、この検出は行われず新規の
worktree 作成に進む（そうしないと二度と `wiki edit` できなくなるため）。

### 同期に失敗したとき

`wiki save` はまず worktree でコミットし、その後リモートとの 3-way マージと push を行う。同期の失敗は次のように扱う。

- リモートに届かない（オフライン、fetch・ls-remote・push の失敗）：ローカルの `refs/projects/wiki/main` をそのコミットまで進め、worktree を片付けたうえで、警告（`--json` では `warnings[].code: "wiki_sync_failed"`）付きの成功（終了コード 0）を返す。コミットはローカル ref にあるので、後で `githive sync` が push する。ローカル ref が編集中に別の場所へ動いていて fast-forward できない場合だけは、ローカル ref を動かさずエラーを返し、worktree を残す。
- リモートが進み続け、競合による push 拒否が再試行の上限に達した：コミットはローカル ref に保存済みで、worktree は残る（`wiki_push_race`、終了コード 3）。
- pre-receive などの恒久的な push 拒否：初回で停止し、コミットと worktree を保持する（`push_rejected`、終了コード 1、再試行不可）。拒否の原因を解消してから `wiki save` を再実行する。
- 再実行：worktree に変更が無くても、worktree の HEAD がまだローカル ref に入っていない、またはリモート（最後に取得した `refs/githive-remote/wiki/main`）に入っていなければ、未完了の save とみなしてマージと push から再開する。「no changes to save」になるのは、公開済みの状態で何も編集していないときだけである。

worktree を破棄する前に `githive wiki save` を再実行すること。破棄手順を踏むと、ローカル ref に入っていない worktree 上のコミットは参照されなくなる（reflog の範囲でしか辿れない）。

## sync との関係

`githive sync` は wiki について **fetch と fast-forward だけ**を行う（[ADR-0017](../adr/0017-wiki-and-meta-sync.md)）。

- ローカルがリモートの祖先なら、ローカル ref をリモートへ前進させる。逆向きなら push する。
- 分岐している場合は自動マージしない。`wiki-diverged` として警告を返し、`githive wiki edit` → `githive wiki save` での解消を促す。sync 自体は失敗しない（終了コード 0 のまま、他の ref の同期も完了する）。
- `githive.wiki.worktree` が設定され、その worktree が実在する（= 編集中）場合は ref を動かさず `wiki-editing` として警告する。チェックアウト中の worktree の足元で ref が動くのを避けるためである。

3-way マージは `wiki save` が worktree 上で行う唯一の経路である。sync は作業ファイルに触らない（[03](../03-sync-and-concurrency.md)）。
`githive status` の未 push 一覧にも wiki が含まれる。

## 素の git での読み書き

```
git show refs/projects/wiki/main:Home.md
git worktree add ../wiki refs/projects/wiki/main    # 直接編集
（編集して commit した後）
git push origin HEAD:refs/projects/wiki/main
```

wiki は全機能の中で最も「素の git」に近く、専用ツールなしでの編集も現実的である。

## ページ間リンクと他機能への参照

- ページ間リンクは相対パスの markdown リンクとする。
- ファイル名は OS 間の可搬性を守る範囲に制限する。Windows の予約名（CON、PRN、AUX、NUL 等）、大文字小文字だけが異なる同名ファイル、末尾のドットや空白は `githive wiki save` と fsck が拒否する（case-insensitive なファイルシステムでの checkout 事故防止）。
- issue / task / chat への参照は `githive:issue/01j8xq4d` 形式のクライアント用リンクで書く。この本文リンクのジャンプ解決は CLI / TUI / VSCode 拡張とも未実装であり、現在は文字列として表示される。MCP の読み取り専用リソース URI `githive://issue/<id>`・`githive://wiki/<path>` とは別の記法である（[15](../15-clients.md)）。

## 設計判断

- ページごとの ref 分割はしない。wiki は横断的な一貫性（リンク、目次）が重要で、単一ツリーの原子的コミットがそれを守る。
- 履歴の粒度はコミットに任せ、ページ単位の履歴は `git log -- <path>` で得る。
- 承認フロー（レビュー付き編集）は v1 では持たない。forge モードで wiki への push を group 制限すれば、最低限の統制はできる（[users.md](users.md) の policy）。
