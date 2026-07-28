# ADR-0014: SSH 署名の「付与」も openssh に委譲する

- 状態：採用
- 日付：2026-07-28

## 文脈

[ADR-0007](0007-ssh-signing.md) は、なりすまし対策として git 標準の SSH コミット署名を使うと決めた。しかし P3 で実装されたのは検証側だけである。`internal/core/sign` は `BuildAllowedSigners` / `VerifyCommit` など検証系の API しか持たず、コミットを作る `internal/core/chain` は go-git の `object.Commit` を `PGPSignature` 未設定のままエンコードしていた。

その結果、`commit.gpgsign = true` が設定されたリポジトリでも githive が書くイベントコミット・マージコミット・チェックポイントコミットはすべて無署名になり、`githive verify` は健全なリポジトリの全コミットに `invalid_or_missing_signature` を積んだ。[01-architecture.md](../01-architecture.md)「SSH 署名は設定に応じて sign が付与する」と [13-roadmap.md](../13-roadmap.md) P3「署名付与」は事実に反していた。

決めるべきことは 2 つある。署名をどう生成するか、そして署名するかどうかの設定解決をどの層が持つか。

## 決定

### 1. 署名の生成も openssh の `ssh-keygen -Y sign` に委譲する

[11-security.md](../11-security.md)「実装上の注意」の「暗号処理を自前実装しない」と [ADR-0002](0002-go-and-hybrid-git-access.md)「署名検証は system git に委譲」の精神を、署名の生成側にもそのまま適用する。Go の SSH ライブラリで署名を組み立てることはしない。

手順は次のとおり。

1. go-git でコミット本体（tree・parents・author・committer・message）を組み立てる。
2. `Commit.EncodeWithoutSignature` で「`gpgsig` ヘッダを除いたコミットオブジェクトの内容」を得る。これは git が署名対象とするペイロードと同一である（git は検証時、コミットバッファから `gpgsig` ヘッダとその継続行を取り除いた残りを署名対象とする）。
3. ペイロードを一時ファイルに書き、`<program> -Y sign -n git -f <user.signingkey> <payload>` を実行する。`<payload>.sig` に armored 署名が出る。
4. 得られた署名を `object.Commit.PGPSignature` に設定して再エンコードし、オブジェクトストアに格納する。go-git は `PGPSignature` を `gpgsig` ヘッダ（継続行はスペース 1 個字下げ）として書き出す。これは git 本体の形式と互換である。

`<program>` は git と同じく `gpg.ssh.program` 設定に従い、未設定なら `ssh-keygen` とする。git 本体がこの設定で署名プログラムを差し替えられる以上、githive が `ssh-keygen` を直接決め打ちすると、両者が同じリポジトリで異なる鍵を使う状態が起きうるためである。

ペイロードは標準入力ではなく**一時ファイル**で渡す。`ssh-keygen -Y sign -` は標準入力を受け付けるが、`gpg.ssh.program` に差し替えられた実装が標準入力を受け付ける保証がない（git 本体も一時ファイルを使う）。一時ファイルと `.sig` は `os.CreateTemp` で作り確実に削除する（[11-security.md](../11-security.md)「実装上の注意」の一時ファイル方針、既存の `WriteAllowedSignersTempFile` と同じ流儀）。

### 2. 設定解決は app 層が行い、`core/chain` は署名関数を受け取るだけにする

`core/chain` から git config を読ませない。`chain` はコミットの永続化だけを知る層であり、「このリポジトリで署名するべきか」はアプリケーションの判断である。

- `core/chain` は `Signer func(payload []byte) (string, error)` 型と可変長オプション `WithSigner` を公開する。`WithSigner` を渡さなければ従来どおり無署名。
- 設定解決は `core/sign.ResolveSigner(ctx, dir)` に集約する。`commit.gpgsign` / `gpg.format` / `user.signingkey` / `gpg.ssh.program` を読み、署名する場合のみ `Signer` を返す。この関数が署名ドメインの `core/sign` にあるのは、鍵パスの解釈（`~` 展開・リテラル公開鍵の一時ファイル化・`key::` 接頭辞）が署名の知識だからである。
- コミットを作る 3 つの app（`entitychain.Writer.Append`、`initapp`、`syncapp.mergeAndAdvance`）が `ResolveSigner` を呼び、結果を `chain` に渡す。`fsckapp` のチェックポイントは `entitychain.Writer` 経由なので自動的に署名される。

### 3. 署名設定が不完全なら黙って無署名にせずエラーにする

`commit.gpgsign = true` なのに `gpg.format` が `ssh` でない、または `user.signingkey` が未設定の場合はエラーで停止する。git 本体も同じ状況でコミットを失敗させる。「署名するつもりだったのに無署名で書かれていた」が最も危険な失敗であり、静かな縮退は選ばない。

CLI では終了コード 5（環境不備、[10-cli-spec.md](../10-cli-spec.md)「終了コード」）、`--json` の `error.code` は `signing_misconfigured` とする。`githive doctor` の signing チェックが同じ条件を警告として報告しており、文言と条件を揃える。

`commit.gpgsign` が false または未設定なら従来どおり無署名で動作する。署名は [ADR-0007](0007-ssh-signing.md) の hosted モードの決定どおり任意のままである。

## 理由

- 暗号処理を自前で持たない方針を、検証だけでなく生成にも一貫させることで、鍵形式・アルゴリズム・FIDO トークン等の対応を openssh の更新に追随させられる。
- ペイロードを `EncodeWithoutSignature` から取ることで、「go-git が書くバイト列」と「署名対象のバイト列」が定義上ずれない。自前でコミットオブジェクトを組み立て直すと、go-git の将来のエンコード変更で署名が壊れる。
- 設定解決を app 層に置くと、`entitychain` / `initapp` / `syncapp` が同じヘルパを共有でき、`core/chain` はテストで署名を差し込みやすい純粋な形（関数を受け取るだけ）に保たれる。

## 帰結

- 署名の有無はコミット OID を変えるが、**ツリーのバイト列は一切変えない**。[02-data-model.md](../02-data-model.md)「決定性の不変条件」は OID ではなく実体化ツリーについての要求であり、event-union マージは元々 OID の差異を前提に収束する（[ADR-0004](0004-merge-by-event-union.md)）。署名の有無が異なるクローン同士でも実体化結果は一致する。
- openssh 8.2p1 未満（`-Y sign` 非対応）の環境では署名を有効にできない。署名を使わない運用は従来どおり可能なので、必須要件は上げない。相互運用テストは `ssh-keygen -Y sign` / `git verify-commit` が使えない環境ではスキップする。
- wiki（[ADR-0003](0003-event-sourcing-commit-chain.md) によりイベントソーシング非適用）は `gitx.CommitAll` 経由で system git がコミットするため、以前から `commit.gpgsign` が効いている。本 ADR の対象外である。
- 署名生成は 1 コミットにつき子プロセスを 1 回起動する。書き込みコマンドの体感速度に影響するが、`commit.gpgsign` を有効にした利用者が git 本体でも負っているコストと同じである。
