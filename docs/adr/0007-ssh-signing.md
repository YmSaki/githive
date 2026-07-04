# ADR-0007: なりすまし対策は SSH コミット署名で行う

- 状態：採用
- 日付：2026-07-04

## 文脈

git の author / committer は自己申告であり、hosted モード（サーバー検証なし）ではなりすましを防げない。
users 台帳（[features/users.md](../features/users.md)）が公開鍵を持つ前提で、イベントの真正性をどう担保するかを決める。

## 決定

git 標準の SSH コミット署名（gpg.format = ssh）を使う。

- hosted モード：署名は推奨（git 標準の `commit.gpgsign` に従う。[ADR-0009](0009-identity-user-email.md)）。`githive verify` が台帳と照合し、なりすましを事後検出する。
- forge モード：`refs/projects/*` への push は有効署名を必須とし、pre-receive で拒否する。

## 理由

- 開発者は既に SSH 鍵を持っており、鍵管理の追加負担がない。GPG 署名は鍵配布・失効の運用負担が大きく普及していない。
- コミット署名はイベント JSON（メッセージ内）とツリーを同時に覆うため、イベント単位の独自署名を封筒に埋める必要がない。封筒がシンプルに保たれ、素の git（verify-commit）でも検証できる。
- 検証の実装を system git に委譲でき、暗号コードを自前で持たない。

## 帰結

- git 2.34 以上が必須になる（ADR-0002 の要求と同一）。
- 署名なしコミットが混ざったチェーンは verify で「未署名」と報告される。プロジェクトごとの厳格度は運用（forge へ移行するか）で選ぶ。
- Agent にも個別の鍵を発行する運用が前提になる（[11](../11-security.md) の Agent の扱い）。
