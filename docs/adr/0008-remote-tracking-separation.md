# ADR-0008: fetch はリモート追跡名前空間に分離する

- 状態：採用（初期案を差し替え）
- 日付：2026-07-04

## 文脈

初期案は `+refs/projects/*:refs/projects/*` のミラー refspec を `githive init` で追加し、ローカルとリモートを同名にする構成だった。
設計レビューで、この構成が通常のユースケースでデータ損失を起こすことがわかった。

1. refspec の `+`（force）により、`git fetch` がローカルの未 push チェーンをリモート状態で強制上書きする。fetch は利用者の明示操作とは限らず、IDE（VS Code 等）が自動実行する。
2. `fetch.prune = true` をグローバル設定している利用者では、リモートに存在しない未 push の新規 ref（オフラインで作った issue 等）が fetch のたびに削除される。

## 決定

- fetch refspec は `+refs/projects/*:refs/githive-remote/*` とする。
- `refs/projects/*` はローカル作業名前空間とし、更新するのは githive の書き込みと sync のマージだけにする。
- `githive init` は `core.logAllRefUpdates = always` も設定し、万一の事故に reflog で備える。

## 帰結

- IDE の自動 fetch、`git fetch --all`、prune 設定のいずれもローカルデータを壊さなくなる。prune は追跡側にだけ働き、それは望ましい動作である。
- 「clone + 設定一行で専用ツールなしで読める」は保たれる。読み取り専用の利用者は従来どおり `git fetch origin '+refs/projects/*:refs/projects/*'` を直接使ってよい（ローカル書き込みが無ければ force 上書きは無害）。
- ツール利用者のローカル `refs/projects/*` が「最後に sync した状態 + 自分の未 push 分」を表すという意味論が明確になる。
