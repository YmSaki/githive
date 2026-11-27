# ADR-0035: 追跡側の状態は同じ fetch の私有名前空間から一組で読む

- 状態: 採用
- 対象: `syncapp.FetchSnapshot`、`syncapp.DiscoverRefs`、`syncapp.Sync` の各ラウンド、`githive wp start` の事前 fetch

## 背景

`wp start` の事前検査と、その後の sync の各ラウンドでは、追跡側の状態を事前 fetch の結果から一組として取る必要がある。追跡側の状態とは、候補 WP ID 集合、各 ref の tip、`meta/config` の OID、各 ref の不存在を指す（workpackage 仕様 §9 手順 1・3）。fetch 後に `refs/githive-remote/*` を読み直すと、直後に別プロセスが別 remote を fetch した結果が混ざる。その結果、別 remote の `meta/config` で schema 検査を通過したり、別 remote に既にある ref を `up-to-date` と誤判定して配送が漏れたりする。

`gitx.FetchPruned` は結果を返さない。`git fetch --porcelain` は更新のない ref も出力するが、git 2.41 以上が必要である。これは要求版（2.34 以上、[01](../01-architecture.md)）を満たさない。

## 決定

`syncapp.FetchSnapshot` は、1 回の `git fetch --prune` で次の 2 つの refspec を同時に取る。

- 通常の追跡 refspec：`+refs/projects/*:refs/githive-remote/*`
- 私有 refspec：`+refs/projects/*:refs/githive-snapshot/<ULID>/*`

`<ULID>` は呼び出しごとに新しく採番し、他のプロセスはこの名前空間に書かない。そのため私有名前空間の内容は、その fetch の結果そのものである。私有名前空間は読み取った直後に `update-ref --stdin` の 1 トランザクションで削除し、結果は `RemoteSnapshot`（ローカル ref 名 → OID）としてメモリに保持する。

- `Sync` は、自分で fetch するラウンドではそのラウンドの `RemoteSnapshot` を使う。`checkSchemaBothSides` と `planRef` は追跡 ref 名を読まない。
- `DiscoverRefs` も `FetchSnapshot` で fetch し、候補 ref をローカル ref とその `RemoteSnapshot` のキーの和から列挙する。`SkipInitialFetch` は、その `RemoteSnapshot` を受け取る。最初のラウンドの `checkSchemaBothSides` と `planRef` も、追跡 ref ではなく、受け取った `RemoteSnapshot` を使う。
- `wp start` は、事前 fetch の `RemoteSnapshot` をローカル CAS の完了まで固定して使う。

## 影響

- `refs/githive-remote/*` の更新内容は従来と変わらず、通信も増えない。
- fetch と削除の間でプロセスが異常終了すると、`refs/githive-snapshot/*` が残る。残った ref は githive の読み書き・検証の対象外で、`git update-ref -d` で消せる。自動では掃除しない。並行する別の呼び出しの名前空間を誤って消さないためである。
- `githive sync` の最初のラウンド（discovery の fetch を再利用する経路）も本 ADR の対象とする。discovery の fetch の直後に別 remote の fetch が追跡 ref を上書きしても、候補列挙・schema 検査・`up-to-date` 判定は影響を受けない。
