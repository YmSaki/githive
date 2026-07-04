# ADR-0009: identity は git の user.email を使う

- 状態：採用（初期案を差し替え）
- 日付：2026-07-04

## 文脈

初期案は、actor を users 台帳のユーザー名とし、`githive.user` 設定で指定させる方式だった。
利用者から「git の設定（user.name）をそのまま使い、githive 独自の設定を増やしたくない」という要求が出た。
ただし user.name は表示名であり、空白・日本語を含み、一意でなく、macOS の Unicode 正規化（NFC/NFD）差でファイル名の byte 表現が割れて決定的実体化を壊しうる。

## 決定

- actor（イベントの行為者 identity）は git の `user.email` の値をそのまま記録する。
- users 台帳の `emails[]` が「email → username・表示名」のマップを担い、解決は表示時にのみ行う。fold は actor を不透明な文字列として扱う。
- owner / assignees / notify の user ターゲット等、人物を指すイベント内の値もすべて email で持つ。CLI 入力では username を受け付け、書き込み時に email へ解決する。
- `githive.user` は廃止する。署名の可否も githive 独自キーではなく git 標準（`commit.gpgsign` / `gpg.format` / `user.signingkey`）に従い、`githive.sign` も廃止する。

## 理由

- email は git が署名検証（allowed_signers の principal）や mailmap で identity に使う値であり、「git の機能をそのまま使う」方針に最も沿う。
- 空白を含まず、事実上一意で、既に全コミットの author/committer に含まれるため新たな露出がない。
- 台帳登録前（P3 以前）でも day 0 から identity が機能する。

## 帰結

- 必須の githive.* 設定はゼロになる（githive.notify.auto / githive.sync.retries / githive.trust.root は任意）。
- イベントの actor はコミッタ email と同値であることを検証規則に加え、素の `git log` でも行為者が読める。
- 実体化ツリー（meta.json 等）には email がそのまま現れる。username・表示名での表示はビュー（CLI/TUI/拡張）の責務とする。
- 同一人物が複数 email を使う場合は台帳の emails[] に列挙して同一ユーザーへ束ねる。
