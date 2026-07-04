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

`githive wiki edit --keep` で worktree を残し、通常の git 操作（`git -C <worktree> ...`）で作業してもよい。
競合したら `githive wiki save` が通常の git コンフリクトマーカーを提示し、解決後の再実行で完了する。

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
- issue / task / chat への参照は `githive:issue/01j8xq4d` 形式の URI で書く。CLI / TUI / VSCode 拡張がこれを解決して表示する。素の閲覧では文字列のまま見えるが、ID から辿れる。

## 設計判断

- ページごとの ref 分割はしない。wiki は横断的な一貫性（リンク、目次）が重要で、単一ツリーの原子的コミットがそれを守る。
- 履歴の粒度はコミットに任せ、ページ単位の履歴は `git log -- <path>` で得る。
- 承認フロー（レビュー付き編集）は v1 では持たない。forge モードで wiki への push を group 制限すれば、最低限の統制はできる（[users.md](users.md) の policy）。
