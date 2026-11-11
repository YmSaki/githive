# GitHive 第一原理アーキテクチャ再検討 — Proposal / 未採用

これは **Phase 6（Architecture Design）の設計提案**である。実装済み・承認済みの仕様ではなく、現行 PR の不具合修正や受け入れ結果を置き換えない。本文の新しい外部契約・順序規則・移行手順は、対応する Phase 4/5 の決定と別 PR が成立するまで現行動作に適用しない。実装コードの変更は行っていない。

観測対象 epoch: `64e3198e28cbf38d9e234fda301e0f5c9af612f0`。分担監査の元 epoch は `c5da0be02e87fa2e221662bea184e77787b1fa12`。`8099699` の embedded schema/ref-context、`f8d740c`/`aa32ce9` の一意な表示 ID、`64e3198` の users 一覧順を差分として考慮する。これ以降の修正は末尾の再評価欄で別に扱う。

適用した方法: `upstream-engineering` の Phase 6 と `upstream-contract-audit`。監査範囲は実装の全行正当性ではなく、補償コードを生む責務・正・信頼・順序・取引・証拠の境界。FACT / PROPOSAL / ASSUMPTION / OPEN を区別する。

## 1. 保持する目的と契約の順序

| ID | 上位の目的・制約 | 保持の意味 |
|---|---|---|
| CON-01 | Git native、外部 DB を正にしない | イベント・設定・状態を Git の到達可能なオブジェクトと ref で表現し、fetch/push と素の git 閲覧を保つ。ローカルの破棄可能な索引を新たな正にしない。 |
| CON-02 | オフラインで作業できる | ローカル保存の成立を外部サービス・ネットワーク成功に依存させない。認証・入力不正・時計等の失敗条件まで無条件に成功させる意味ではない。 |
| CON-03 | 追記監査履歴 | 既存の commit OID、署名、actor、親関係を保持する。修復・移行は新しい記録として行い、過去の JSON の無言書換えや force での忘却をしない。 |
| CON-04 | 人間と AI が同じ文脈を共有する | CLI/MCP/素の git が同じ基底履歴を参照する。表示の違いはよいが、適用結果・部分成功・未配送・検証不足を別の意味へ写像しない。 |
| CON-05 | 決定的な実体化 | 同じ契約版と同じ正当な入力集合から同じ bytes/tree を得る。異内容の同一 ID、未知版、欠損情報を勝手な勝者や成功へ正規化しない。 |

暫定権威順: ユーザー目的 → active な docs/02 のデータ契約・docs/03/10/11 の対象保証 → 明示的に supersede した採用 ADR → schema と独立ベクタ → 実装 → CI/作業規約・過去の QA 記録。README の未来比較表とテストの存在を要件の根拠へ昇格しない。docs/README の「02 が正」と spec/README の「Go が正」は依然整理を要し、この提案では後者を全体の規範権限とは解釈しない。

実際の引用基点: docs/01「書き込みはローカル ref の更新までで完了」「部分的成功…正常系として報告」、docs/02「同じイベント集合から byte 同一」、docs/03 の ref 分離・event-union、docs/11 の Hosted/Forge 境界、ADR-0018 の意味論版、ADR-0020 の checkpoint 独立検証、ADR-0025 の config 同版競合、ADR-0026 の損失を生む順序の拒否、ADR-0030 の埋め込み schema 検証。

## 2. 観測事実と「補償」の分類

FACT: 現行の4層構造そのものが失敗しているとは結論しない。CLI/MCP → app → core の方向、pure materialize/merge、go-git と system git の役割分担は上記目的を実現する妥当な骨格である。問題は同じ保証を別々の入口が再実装し、必要な情報が責務境界で失われ、派生データを安全に使うための証明が各所で後付けされる点にある。

| 現在の仕組み・根拠 | 分類 | 除去を約束できない理由 / 改善できる余地 |
|---|---|---|
| Writer.AppendOnto の親 OID 再読込・CAS 再試行、repo 単位 in-process lock、sync の再計画 | **必要な競合制御** | 複数プロセス・端末が同時に更新できるから必要。関数数を減らしても競合は消えない。保存境界の一箇所へ所有を集める余地はある。 |
| fetch/push、資格情報・Git エラー分類、非 FF retry と恒久拒否の分離 | **必要な輸送境界** | ネットワーク分断と相手側更新は設計から消せない。再試行可能性と部分結果を共通の結果モデルへ載せる余地がある。 |
| strict codec、schema/ref-context、ID 衝突、user key/entity order、署名と trust-root 検査 | **必要な信頼境界** | 外部の素の git 書き手を許すため、現在の書き手が型安全でも読込検査は残る。正規化した同一モデルの再検査を削減することは可能。 |
| init、sync、read、verify、fsck での個別受入チェック | **入口の分散による重複候補** | 消してよいのは、全入口が同じ受入判定へ必ず到達し、各入口の強度差が明示された後だけ。8099699 は旧 init の users-only 検査を他 feature へ広げたので、旧所見を現行の無検査として引用しない。 |
| full WalkChain → checkpoint 探索 → checkpoint 親を再 walk/fold/比較 | **未証明 cache を使う選択に由来する補償** | 今の snapshot は正を保証しないため独立再計算は正しい防御。snapshot を無条件で信頼して除去してはいけない。 |
| ULID の壁時計順に対する時計ずれ拒否、create/key の順序検査 | **順序モデルの選択に由来する補償** | 現規則の下では損失防止に必要。因果順への変更は format/fold 契約の変更であり、単純 refactor では除去できない。 |
| CLI/MCP の通知前後 read、task 作成後 link、成功/失敗別の部分結果包み直し | **command 境界・結果情報の不足に由来する補償候補** | 保存成功と配送/副作用の成功を1bitで表せない。単一 command receipt と side-effect 計画を app が返すまで adapter の補償を消さない。 |
| []map → []any、日付/数値の schema 適応、base64、短縮 ID の表示 | **表現境界に必要なものと偶然の変換が混在** | canonical bytes と外部プロトコルの差は残る。型制約を根に持つ反復変換は一箇所へ寄せられる。表示一意性自体は利用者契約であり消さない。 |
| OS 別の Git helper・fault injection・独立 spec・配布物 QA | **検証責務** | 行数を減らすため除去すると誤った PASS を増やす。後述 scripts.md の主体・結果・依存の結合を改善する。 |

## 3. 統合するアーキテクチャ案（まず現行形式を保持）

PROPOSAL ARC-01: まず format を変えず、正と責務を次の概念へ分ける。全項目を新パッケージへ機械的に分割する提案ではない。既存関数へ責務を割り当て、2個目の同じ保証が現れたときにだけ共通化する。

```text
CLI / MCP / future UI
        ↓ command + request context
Application commands ──→ CommandOutcome / QueryView
        ↓
Project repository / replication coordinator
  ├─ accepted immutable history at exact OID
  ├─ contract/version + config/trust context
  ├─ materializer + typed state / derived view
  ├─ append CAS + owned local transaction
  └─ transport + remote-qualified tracking
        ↓
Git objects / refs + system Git / SSH
```

| 所有者 | 一意に所有すること | 所有しないこと |
|---|---|---|
| Contract/read boundary | strict decode、版・schema・ref文脈、ID同一性、既存順序規則。適用した contract fingerprint と OID を伴う受入結果。 | UI の exit code、ネットワーク成功、署名が正しいから内容も正しいという推論。 |
| Materializer | 受入済みイベントからの pure fold、正規ツリー描画。結果を決めるすべての意味論に版を付ける。 | Git I/O、現在時刻・乱数・ネットワーク・読み取り時の可変設定による結果変化。 |
| Project repository | 正確な head に対する読込と保存、CAS、repo-local lock、派生 cache の無効化。 | ユーザーに代わる設定勝者の選択、CLI/MCP 固有の JSON。 |
| Application command | task 作成+link 等の一つの利用者操作、observed from、解決済み actor/reference、適用 delta、通知計画とローカル保存の receipt。 | printer、shell、MCP IsError、既知の partial outcome の破棄。 |
| Replication coordinator | init/import/sync の受入ポリシーを共通化し、remote/tracking/capability と per-ref 結果を保持。 | 署名・schema・materialization の一部を未定義の成功として代用すること。 |
| View/presenter | History view と Snapshot view、検証の対象・強度、出力表現/exit mapping。 | 「snapshot が読めた」から「履歴が検証済み」への昇格。 |
| Evidence tooling | 対象バイナリ・ソース・規則・実行環境・適用範囲を束ねた検証記録。 | 一時ログを新しいプロジェクトの正にすること、過去 SHA の PASS を新 SHA へ移すこと。 |

依存方向は保持する。Contract/Materializer は app/CLI を import しない。core が Project command を知る設計にはしない。go-git/system Git のハイブリッドも、クロス OS の署名・通信互換の証拠なしに単一ライブラリへ統合しない。

### ARC-02: 一つの受入モデル、複数の明示した強度

Context/根拠: CON-03〜05、初回採用・通常同期・既存 raw refs・snapshot reader・verify の経路。

PROPOSAL: `Unknown` を `Supported` に丸めない config 状態を持ち、`Absent / Supported / Unsupported / Corrupt` と、その操作に必要な情報を明示する。meta/config ref 自体が無い day0 の既存許可と、ref が存在するのに config.json が欠損・破損・非整数・非正値である場合を分ける。後者は既存 schema 契約の不適合であり、typed invalid_config による拒否は新しい初期化必須条件の導入ではない。進行中の ADR-0032 修正はこの区別で別途再anchorする。新たに day0 を禁止する判断を行うなら、それだけが Phase 5 の契約変更となる。

受入結果は「履歴完全性」「データ/参照整合」「実体化整合」「署名/台帳認証」を別の次元として持つ。未知 kind の forward compatibility、legacy v、meta/wiki の例外、shallow snapshot は各々別 path として列挙する。単一 `verified=true` が何を意味するかを曖昧にしない。

除去条件: init、sync（初回/FF/diverged）、append、show/list、verify、fsck が同じモデルを使い、対象 ref/context/profile を変えた反例で誤った成功が到達しないこと。判定自体を一度にまとめても、より強い保証に必要な追加計算は残す。

Tradeoff: 一度の full validation は高価。OID + contract/reducer fingerprint + policy/input context による証明済み cache を導入するまでは、性能のための省略をしない。ローカル cache はオフラインで作れ、削除して Git から復元できる必要がある。受入証拠・materialized state・snapshot-only view は所有権も分離する。mutable mapのpointerにverifiedラベルを付けるだけでは足りず、非公開のimmutable値、copy、又は明示的な所有権移転と証拠失効を選ぶ。callerによる後続変更を受入済みstateとして使わせない。

### ARC-03: CommandOutcome を app の公開結果にする

Context/根拠: docs/01 の local-first と部分成功、CON-02/04。boundaries 分担監査では auto-sync の成功結果に含む trust-root 採用/保持の警告が adapter で捨てられるケースを実再現している（分担成果に根拠を保存）。この不具合の修正は提案採用を待たず継続する。

PROPOSAL: `local commit persisted`、`current ref still includes it`、`side effects recorded`、`delivery accepted / pending / rejected / not requested`、`per-ref conflict and recovery` を一つの receipt に保持する。CLI/MCP は同じ receipt を異なる wire 表現へ写す。成功/失敗で data の所有を別にし、後で nested error に戻す構造を減らす。authoritative ref を別履歴へ採用し直す場合は、元の未配送commitの保存場所と回復性をreceiptへ含める。reflogだけを恒久的なデータ保全とみなさず、必要な旧headをGC後も到達可能にするGit ref等の持続的anchorを先に確保する。anchorの自動配送は未公開データを公開する判断を含むので勝手に行わない。

通知は短期的には app command が frozen recipient と origin event ID を含む計画を生成する。後続 delivery は冪等である必要がある。receipt を返すだけでは、crash 後の再実行の重複は防げない。OperationID を永続化して同一入力digestへ結合する方式か、既存の origin event を探索・再調停する方式を別の決定点とする。JSON-RPC request ID や同じ本文だけを同一操作とみなさず、利用者が意図的に同じコメントを再投稿する場合と区別する。新しい idempotency key の公開は Phase 5 で契約化する。Git で保存する durable outbox か、origin event からの決定的な通知 projection かは別の選択であり、まだ決定していない。後者を選ぶ場合、recipient と rule version を creation 時点へ束縛し、後の group/registry 変更で過去の通知を変えない。manual notify.post と ack の監査は残す。

除去条件: task作成後のlink失敗・通知生成失敗・sync成功だがlocal trust-rootが入れ替わる場合でも、全 transport が同じ receipt を出し、既知の部分結果が失われず、local 保存成功を network failure が取り消さないこと。二重生成を防ぐ保証を公開する場合は、receipt前のcrashも含めたdurable OperationID/再調停を別に証明する。per-ref Git 操作をひとつの global transaction と偽称しない。

互換影響: CLI/MCP の現行出力を破壊しない adapter を先に用意し、追加情報・新 error code は Phase 5 で固定する。既存イベントは変更不要だが、新しい outbox/projection を公開 ref の正へ含めるなら schema/通知読者の移行が必要。

### ARC-04: snapshot は authority ではなく派生 view/cache

FACT: 現在は受信 checkpoint の state と max_event_id を親履歴から検証する。これは署名された偽 snapshot の固定化を防ぐ正しい防御であり、削除対象ではない。一方、full walk 後にもう一度 checkpoint 親を full walk/fold する構造は、最適化になっていない。

PROPOSAL: (A) cache がないときは一つの正当な全走査結果を読込・ID/order 検査・snapshot 比較へ共有する。(B) 独立検証済み cache は `(head OID, contract/reducer version, scope, trust-policy context)` に結合し、依存が違えば再計算する。snapshotOnly view は明示し、完全履歴の認証に使わない。cache entry の「誰が作ったか」だけでも、input key だけでも内容証明にならない。validation component が独立再計算したという発行経路と保存先の信頼境界を明示し、state digest・source OID 集合・ruleset fingerprint・config OID に結合する。認証結果は内容整合のcacheと分け、評価に使った正確な registry HEAD/親台帳 OID・trust-root pin・鍵/設定・Git/SSH adapter profile にも結合する。由来を検証できない永続entryは受信snapshotと同じ扱いにし、必要ならsession内の非公開capabilityによるcacheから始める。

除去条件: cache の state、covered IDs、因果/順序境界、feature/entity、元 head と reducer が一致し、cache 消失・偽造・古い版・branch merge・ID衝突・時計ずれで reference full fold と同一結果か、明示的に拒否すること。全機能・全互換路に対して確かめて初めて redundant walk を減らす。

Tradeoff: 安全な cache はローカル運用状態を増やすが、外部 DB ではなく破棄可能な派生データに限定できる。Git の checkpoint を真として扱う設計は採らない。checkpoint が256KiBを超える問題を解く blob 化は event protocol の別変更であり、この局所共有だけから自動採用しない。

### ARC-05: Git の資源/競合は正当な境界として残す

- system Git の CAS/lock、remote の FF 制約、部分受理、恒久拒否は保持する。retry を一箇所に寄せても retry 条件・予算・観測結果は隠さない。
- ローカルの複数 ref 更新を原子的にするなら Git の transaction capability を確認する。remote の multi-ref push が全て原子的であると仮定しない。対応しない remote では部分結果を表す。
- remote tracking は将来的に remote identity を含む名前空間へ分けられる。これは project history でなく派生追跡の移行であり、旧 tracking を正とせず各 remote の fetch で再構成する。prune/validation を無根拠に外さない。
- wiki の通常 Git 履歴・人の編集競合は独立 adapter として残す。一時 worktree の削除権は「パスが temp らしい」ことでなく、自分が所有して作った資源の記録に基づく。手動 worktree を推測で消さない。


### 分担core監査から追加する除去候補と保留事項

- FACT C-03: epoch64eの `StateFromMap` / `ScratchDecoder` は production callgraph から外れ、現在はtestsの復元確認だけで使われる。一方 `StateToMap` は checkpoint の発行・照合で現役である。Step1は前者のdecoder登録/decoder-only helperについて、全buildtag/生成物も含む参照確認とテスト根拠の移行を行った後の小単位の除去候補。Step2のtyped-state再設計とは切り離す。wireのstate形状、StateToMap、現reducerのScratch、snapshot照合/fullfold fallbackを一括で「dead checkpoint層」として消さない。
- PROPOSAL C-04: `Meta / Collections / Scratch` のdual map stateを、feature単位のtyped domain state一つとreadable projectionへ整理する。型のdefault/復元helperを減らせる可能性があるが、既存bytesと意味論を保持するadapter又は明示epoch移行が条件である。
- PROPOSAL C-06: 鍵材料のidentityをparsed public-key blobからのKeyIDへ固定し、commentを独立metadataにする。現行exact-pub-stringのrevokeと材料fingerprintの違いを隠す拒否分岐を減らすには旧variantの明示対応/quarantineが必要。旧失効の意図を推測して署名を作り直さない。
- FACT C-08: OpenSSH/Git出力のfingerprint取得に失敗したときの保守的失効判定は、安全性を維持するfallbackである。structured signer evidenceへ替えるなら、supported adapter版・主体/鍵同一性・未知fingerprint時の拒否を同等以上に検証してからにする。
- C-01 locale重複環境とC-02 Git失敗→不存在の写像は別の実バグ修正トラック。設計だけで直ったとせず、確定修正commitと実subprocess/Git反例の閉塞を末尾へ追記する。

### 分担app監査の統合（production 51 files）

[app.md](app.md) はc5→64eの全app差分を意味読取りし、configは実再現、mention/wiki/session等は静的所見として分けている。この設計案は後者を再現済みproduct bugへ昇格しない。

- A01/A02: 既存config破損→許可は#191で共有parserへ修正。initの旧users-only受入は8099699で閉塞。単一AcceptedHistoryは現行検査の削除許可ではなく、全入口が必要なschema/ref/context/ID/orderを再列挙する誤りを減らす所有の案である。
- A04: shallow由来のProjectionViewと完全履歴stateの区別を維持する。加えて、一覧の`[]Meta,error`だけではfilterされた行・欠損row・読取不能rowを区別しにくい。QueryOutcomeにitems/per-ref problems/source/scopeを持つ案は、完全性の定義をPhase5で固定してから公開する。全一覧のfull replayを要求する案ではない。
- A05: 未知mentionを無視するのは正当だが、LookupFailureとUnknownNameは別。元操作をrollbackせず、副作用失敗をwarningへ保つ結果モデルにする。設定未指定の既定と設定読取故障の既定を混ぜない。現在の全error→未知名経路は静的確認であり、通常postからのIO障害再現は未実施。
- A06: 複数ack・Agent登録/key追加・wiki local保存/push/cleanupの途中失敗は、先行永続化と後続失敗を一つのerrorへ縮めない。CommandOutcomeは各effectの到達性と回復を保存する。新しい全操作atomic/rollback/exactly-onceを既存契約に捏造しない。
- A07: wiki SessionはbaseOID/worktreeHEAD/published head/所有資源の関係を持つ案とする。baseに対する競合判定、明示merge、cleanup成功前にpointerを失わないことを検証義務にする。現在の並行writer上書き懸念は未実再現なので、確定bug・修正完了数には含めない。

これらはARC02/03の候補責務と対応し、未知や部分的結果を空値/成功へ写す箇所を一つの場所で監査できるようにする。ただし型名を追加しただけで保存済みeffectの証拠や冪等性は得られない。

## 4. より根本的な選択肢 — Phase 4/5 へ戻す必要がある

以下は選択済みではない。局所的な共通化で消せない補償だけに関係する。

| 選択 | 保持/改善できるもの | 代償と未決定点 |
|---|---|---|
| 現行 per-entity refs と ULID 全順序を保持 | 最小の format 変更、既存 git show/log と reader 互換。ARC-01〜05 の責務整理は可能。 | 依存を破る時計ずれは引き続き拒否する。raw users の依存順序・未知 kind・version profile の検査を残す。 |
| 次版イベントで明示的な causal dependencies + deterministic concurrent tie-break | 観測した create/user/key より後という意味を wall clock から切り離せる。 | causal metadata を含む event identity、欠落依存/循環/競合の規則、旧イベントの意味、caps/DoS の定義が必要。同じ旧イベント集合に新しい順序を無言適用しない。 |
| project 単一 journal（複合 command を一つの commit に記録） | cross-ref side-effect の保存を一つの authority/CAS にでき、config/registry の観測点が明確。 | 全 project が同じ ref で競合する。大規模 snapshot と clone、既存 per-entity refs/素の git 閲覧、特定 feature の部分取得を変更する。必要な CAS/retry はむしろ集中する。 |
| per-entity refs + project transaction root/manifest | 現行 views を保ちつつ command 単位の集約点を設けられる可能性。 | JSON に commit hash を書くだけでは Git reachability/transfer を保てない。親として参照する等の設計が必要。rootだけ正でentityrefsが派生なら、古いreadersのstale view問題も定義し直す必要。複雑さが減る実証前には推奨しない。 |

暫定の推奨: まず現行形式を保持する ARC-01〜04 の計測可能な prototype を別 PR で評価する。causal ordering や単一 journal は、「オフライン可用性・部分同期・単純さのどれを優先するか」を上位で決めてから比較する。複雑さを別の manifest に移しただけで削減と数えない。

## 5. immutable 履歴を保つ移行条件

1. 現行 format 内の責務整理: wire/canonical/fold の reference 差分ゼロを証明し、旧 commit を読めるまま関数の所有だけを移す。独立基準を保ち、同じ新実装を expected にして比較しない。
2. 新 receipt: 旧 CLI/MCP shape を再現する adapter を先に入れ、追加情報を段階的に公開する。旧 reader を「全部理解した」と扱う汎用 success にしない。
3. cache/追跡索引: 破棄して同じ Git history から再構成できる。各 entry の依存版と OID を記録し、異なる reducer/policy を同じ cache として使わない。索引を消しても project data は失われない。
4. causal/単一 journal 等の新 format: 旧 head を保存した明示的な migration record/新 namespace から始める。旧 segment の意味論版、入力 head、検証済み state/hash、tool/contract fingerprint を束縛する。旧 commit を再生成・rebase・resign しない。対応クライアントには新しい区間の版を検査する gating が必要。ただし Hosted の offline 旧cloneやraw Gitの書き込みを全世界で禁止できるわけではない。新クライアントは後から届いた旧版branchを検出し、勝手に新しい区間へ混ぜず拒否/quarantine/明示移行へ送る。config/registryを含む依存OIDが取得・受入済みになってからfeatureを採用し、不足時に最新値やdefaultを推測しない。旧readerの協調拒否とサーバー強制を混同しない。
5. migration recordの権限は、固定した旧users/config/trust-rootと必要な全feature headのOID集合に基づく旧authorityで判定する。新snapshotが正しいことを仮定して新trust-rootを選ぶ循環bootstrapをしない。移行中のregistry/config変更は再読込又は適切なGit transaction/CASで検出する。分散した全端末の「最新」を原子的に取得できると仮定せず、選んだ履歴cutと遅れて届く更新の扱いを契約化する。
6. new migration snapshot はイベントメッセージにすべて埋めなくてもよいが、その場合は Git で到達可能な blob/tree と検証規則を定義する。hash文字列だけを残して GC/transfer で失う案は不可。blob化・サイズ例外・新 schema は別途 Phase 5/7 の責任。
7. 不正/既存曖昧履歴は自動的に正へ補正しない。quarantine/読取専用/明示解決のどれを提供するか決め、元の署名・親・ID と診断を残す。Hosted に信頼できる大域時刻やサーバー強制があるという仮定は追加しない。

## 6. 実証可能な受け入れ条件（提案の検証義務）

| ID / 対象 | 反証可能なシナリオ | 合格の観測 |
|---|---|---|
| AC-01 共有受入境界 | 同じ invalid schema/ref/entity/ID/order の履歴を init、sync初回、FF、merge、直接ローカルrefに与える。 | 入口ごとの許可profileを除き、採用拒否と診断が一致。failureで対象refを進めず、historyを忘れない。 |
| AC-02 receipt | local write成功後、push恒久拒否、trust root採用/保持、notify失敗、compound link失敗を注入。 | local commit OID、現在到達性、deliveryと回復性がCLI/MCPで同じ意味を保つ。成功の警告も消えない。 |
| AC-03 cache | 同じheadのstateだけ改変、reducer/policyだけ変更、旧版cache、cache全削除、merge後に利用。 | false verified/PASSなし。reference全走査と同じか適切に拒否。削除後もGitだけから復元。 |
| AC-04 immutable migration | 旧clone・旧署名・旧raw Git閲覧を保持したまま新formatを導入し、旧readerが操作。 | 旧OID/到達性不変。対応readerは明示非対応を報告し、新readerはoffline旧clone/raw Gitが後から作った旧branchを検出して自動混入させない。全旧書き手の操作禁止までは保証しない。metadata先行/不足/競合fetchでも境界を独立に再現できる。 |
| AC-05 notification | recipient registryやrule版を後から変更し、offline再同期・同command再試行。 | 過去の宛先/意味が変わらず、重複生成/未記録の成功がない。manual post/ackも閲覧可能。 |
| AC-06 offline/concurrency | remote停止、二端末同時更新、別プロセスCAS負け、rename/delete/lock障害。 | local-firstの定義どおり保存状態を保持し、必要な再試行だけ行う。network失敗をlocal rollbackへ変えない。 |
| AC-04b migration権限/到達性 | 移行中のregistry/config更新、無権限署名、複数root、旧ref削除後のGC、clean cloneで必要refのみfetch。 | 旧authorityの承認と全依存objectが再現可能。自己登録/新snapshotだけで信頼を作らず、元segmentの署名・権限・foldを独立検証できる。 |
| AC-07 独立性/互換 | 正常/境界/未知版/欠損の共通ベクタを旧互換reader、新reader、Python参照で比較。 | 期待bytes/状態/拒否条件が独立契約に一致。正当な未知フィールドを保存し、未知kindの保証範囲を明示。 |
| AC-03b evidence ownership | stateとdigestの同時改竄、registry/pinのみ更新、同じ版名でschema bytes更新、返却mapのcaller mutation。 | contentと認証cacheの適切な失効、snapshotOnlyのauthority混入拒否、古いproofの再利用拒否。 |
| AC-02b durable operation | create保存後receipt前、link後notify前、delivery応答前のcrashを別process/端末から再開。 | 選択したOperationID/調停契約の保証を満たす。未選択ならexactly-onceと呼ばずpartial recoveryを示す。 |
| AC-08 保守性 | 境界前後で保証に必要な走査数、重複の実装箇所、error写像箇所、必要なケース数を計測。 | 安全性を落とさず重複が減ったことを示す。未設定/unknownを隠して条件分岐数だけ減らす案は不合格。数値目標は計測前に捏造しない。 |

## 7. 契約監査の所見・open closure

以下の3件はbaselineに対する設計上のroot整理で、DIRECT_CONTRADICTION/highが2件、NARROW_ABSTRACTION/mediumが1件。#191後の閉塞を末尾に記し、これを現在openのrelease bug件数とは数えない。未採用案のDR01〜06は別のclosure義務でありproduct bugのF/I/D件数へ混ぜない。

### UCA-ARC-01 — 部分成功を伝える契約と情報落ち（DIRECT_CONTRADICTION / high）

- Contract A: docs/01 の部分的成功報告と CON-02/04。
- Contract B / finding location: c5da の `cmd/githive/root.go:syncIfEnabled` が成功した Sync の per-ref 結果・warningを使わない経路。boundaries担当が remote trust-root pin の新規users変更を伴うケースを実再現。これは設計仮説ではなく分担再現事実として扱う。
- Closure: local command commit → auto-sync trust-root resolution → current ref / recovery warning → CLI/MCP receipt。この warning edge が欠落すると、直前commitがcurrentrefから失われても単なる成功として見える。
- 最小反例: 無関係な2台帳、remote側をpin、localにuser追加、auto-syncがremote採用。要件では保存と現在到達性/回復を区別して示すが、adapterがwarningを落とす。
- Earliest invalid artifact: 該当 accepted implementation baseline の adapter写像。上流の部分成功要件そのものを変える必要はない。新 CommandOutcome は再発を減らす選択肢であり、このバグ修正の前提ではない。
- 必要解決: 既知の保存/配送/採用結果を全transportに保ち、成功した副次処理の結果も情報落ちさせない。64e以後の警告修正は別に再anchorする。

### UCA-ARC-02 — day0不存在と既存config破損を同じ許可へ写像（DIRECT_CONTRADICTION / high）

- Contract A: docs/02/meta.schema の既存 meta/config は config.json と正の整数 schema_version を持つこと、および操作前の対応版検査。Contract B: 64e の `entitychain.CheckSchemaSupportedAt` は既存refの missing/malformed/non-integer をfsckへ委ね、nilを返す経路を持つ。
- 反例/closure: meta/config ref は存在するが config.json が壊れている、又は schema_version が欠落/非整数であり、feature履歴は読める。既存configが契約を満たさないのに、day0と同じ許可へ正規化される。config読取→nil→操作許可の情報欠落である。
- Earliest invalid artifact: 既存refの不正を「未初期化で許可」に折り畳む実装の写像。上流のmeta schemaを変更する必要はない。day0でref自体が無い場合を禁止する新設計とは区別する。
- 必要解決: absent-ref許可を保持し、present-but-invalidはtyped invalid_configで拒否する。root担当のADR-0032修正が進行中であり、確定commitとテストで再anchorして閉塞を確認する。本提案は既存schema不正の拒否にPhase5承認を要求しない。

### UCA-ARC-03 — ID全順序とオフライン依存（NARROW_ABSTRACTION / medium）

- Contract A: オフラインの複数actorが同じ履歴上で後続操作する目的。Contract B: docs/02 の壁時計由来ULID順をfold順とする選択と、ADR0026の損失防止拒否。
- 反例: 進んだ時計のcreateを観測した端末が、遅い時計のままcommentを追記しようとする。現在は消失を防ぐため拒否する。これはデータ安全性の修正でありバグ扱いしてguardを消さない。
- Open: 要求するオフライン可用性に、どのclock-skew範囲と回復操作を含むか。要件が拒否を許すなら現在のprofileを保てる。許さなければ順序表現をPhase4/5で再検討する必要がある。
- Root候補: docs/02 の順序モデルが利用可能性の前提を明示していないこと。ユーザー目的から無制限の時計異常耐性まで捏造しない。

対象にしたclosure roots: 入力受入、projection/cache、command部分結果、history移行、署名と内容の別判定、QAのfreshness。認証鍵侵害下の全シナリオ、Forge運用、全OSの資源挙動、すべての既存Issue、将来formatの完全なproofは未監査。未知kind、旧版ごとの規範優先、通知の生成時点の依存集合にはopen edgeがある。

## 8. 分担テスト監査を設計へ結び付ける

[app-tests](app-tests.md) の75ファイルと [boundary-tests](boundary-tests.md) の42ファイルは、case/helper/fixture/skip/expectedの構造監査であり、全case・全OSの再実行や全assertionの独立証明ではない。この区別を新アーキテクチャの採用証拠にも保つ。

| 分担監査の観測 | 設計への拘束 |
|---|---|
| 各packageのTestMainはglobal/system Git設定を隔離し、実のfixture identity/署名前提を作る。 | 環境補正を製品fallbackと混同して削除しない。利用者設定・実配布・署名capabilityは別のprofileで検証する。 |
| CAS/native wrapperやGit hookは発火count・contender ancestry・最終OIDまで観測する。 | fault seamを削るなら同じ競合窓の実発火証拠を残す。errorだけ返すmockで代替しない。 |
| shortcut/fullfoldや保存tree比較は同じreducer/TreeFilesをexpectedに使う。 | 経路差・改竄を検出できるがreducer共通bugには弱い。独立spec/正負例を残す。 |
| in-memory MCP handler試験は実Gitを使うがstdio起動・framingを通らない。 | registry/handler契約と配布物stdioのE2Eを別のsubjectとして残す。 |
| 明示syncの部分結果試験はauto-syncを通らない。 | ARC03 receipt義務は両経路で閉じる。#189は明示sync試験が緑でも残り得る具体例。 |
| short-IDの曖昧fixtureはvalid履歴を持たないrefを使う場合がある。 | ref-name解決だけの狙いでは妥当。そこでshow/署名/保存の安全性まで証明したと扱わない。 |
| shallow試験は実depth1 fetch後にremoteを削除し、offline snapshot readとstrict操作拒否を分ける。 | QueryViewとAcceptedHistoryの型/保証の区別を維持する。partial取得時に未検証をauthoritative stateへ昇格しない。 |
| POSIX editor/署名tool欠如には明示skipがある。 | platform/capability選択とaggregateを分ける。実OS CIでも未実行項目を自動的にPASSにしない。 |

これらは必要な検証上の補償であり、productionの型整理で消えるとは限らない。将来のinterface変更時にも、保存されたGitオブジェクト・ref・per-command receipt・プロトコル境界を独立に観測するテストを残す。

## 9. Phase 状態と次の判断

- Phase1 PARTIAL: ユーザー目的は明示。成功の運用指標は未固定。
- Phase2 PARTIAL: 本監査は設計提案まで。実装の採用/移行範囲・期限は未決定。
- Phase3 PARTIAL: 人/Agent・offline・raw Gitのworkflowは既存資料から抽出。失敗時の回復UXは一部未定義。
- Phase4 REOPENED: offline可用性とclock/未知kind/partial outcomeの厳密な保証が必要。day0と既存config破損の区別は既存schema契約のclosure修正であり、新たな要件変更としない。
- Phase5 REOPENED: receipt/profile、新formatを選ぶ場合の互換/移行境界が必要。
- Phase6 PARTIAL: ARC-01〜05の候補責務を提示。採用比較と全closureは未完了。
- Phase7 NOT_STARTED（提案について）: 既存実装を新提案の詳細設計完了とはみなさない。

Verdict: 現formatを保つ責務共有prototypeは **CONDITIONALLY_READY**。順序・authority・通知永続化・単一journalへの再設計実装は **NOT_READY**。既存不具合を直す作業を止める必要はなく、この提案の採否で現行PRの検証結果を改変しない。

## 10. 再anchor・分担成果の取り込み状況

scripts/CI補償は別紙 `scripts.md`。core/app/boundaries担当の完全なinventoryは [core](core.md)、[app](app.md)、[boundaries](boundaries.md)、[app-tests](app-tests.md)、[boundary-tests](boundary-tests.md) を参照する。[独立設計レビュー](design-review.md) のDR01〜06を上記ARC02/03/04・migration・ACへ反映した。特に init 受入について旧c5daと8099699の差分を反映し、表示ID/一覧ソートの修正を未対応と数えない。以後のepoch差分は未承認の提案の内容と混同せず追記する。


### 独立レビュー closure 対応表

| Review | 本提案で補った場所 | 未決定として残すもの |
|---|---|---|
| DR-01 cache issuer/証拠/context | ARC02/04、AC03b | process内/保護local store/再検証の選択と脅威モデル。key一致だけで証明しない。 |
| DR-02 durable retry identity | ARC03、AC02b | OperationIDの生成・永続・retention・再開protocol。現receipt保証だけからexactly-onceを言わない。 |
| DR-03 offline旧writer | migration4、AC04 | supported旧版とold-branch quarantine/変換の具体契約。Hosted全writerの禁止は約束しない。 |
| DR-04 migration authority/到達性 | migration5/6、AC04b | 全依存OIDcut、旧authority承認と更新競合の取得/再検査方式。 |
| DR-05 mutable state/proof | ARC02、AC03b | immutable/copy/ownership transferをPhase7で選ぶ。viewをwrite authorityへ渡さない。 |
| DR-06 dead decoder小単位 | core追加C03、AC07/08 | 全callgraph/互換試験を閉じてからdecoderだけを除去。typed state/wire変更とは別。 |


### 実装修正トラックのepoch後差分（提案の採用とは独立）

| 対象 | 64eからの変化 | 本稿の再評価 |
|---|---|---|
| #190 locale | candidate `ae350d15145186ffbc9189ec500a6332f881d53b` がGo os/execのlast-winsに合わせてLC_ALLを上書きし、実subprocess regressionを追加。 | core C01の旧違反を現epoch修正後の未修正bugとして扱わない。locale/adapter境界自体の必要性は変わらない。 |
| #191 Git失敗分類 | `c4fc9c799b7f20393c979c96a8da7a2718bfec9b` がRevParseのmissing exit1と実行失敗を分離。 | 不在と不明を共通typed結果にする提案の局所closure。全Git adapterが構造化済みという主張には広げない。 |
| #191 config | `6ef0bb4e63b3c949029b4568e6f43a0649f14610` / ADR0032。day0を保持し、present-invalidをinvalid_config、future versionをschema_unsupportedとする共有`core/projectconfig` parserへ統合。 | UCA-ARC02の旧反例はこの修正の回帰対象となった。現契約closureのbugfixであって、新規の初期化必須profileではない。meta全項目・全歴史整合を検査したとは言わない。 |
| #189 auto-sync結果/回復 | boundary再現から後続修正を継続中。warning伝達に加え、置換前local headの持続的recovery refを必要条件として検討。 | UCA-ARC01は確定修正commitと事例の閉塞が揃うまで解決済みとしない。receiptを失わない設計とimmutable保全条件を追加した。 |
| importlint | raw-string反例は開発補助bugとして別バックログ管理。現候補runtime修正へ追加しない。 | scripts.mdを参照。既存製品にcanonical迂回があるとの主張ではない。 |

これらのsource差分と追加testの存在を確認したが、設計担当が新epochで最終配布QA/全CIを再実行したわけではない。最終のsource/binary/QA/承認結合はPRの固定した証拠へ委ねる。

### 最終統合差分の注記

`de420b0` の durable recovery ref と配送結果保持、`0cb7bf5` の本文/鍵正規化分離、`dfc4ebe` の Wiki 祖先確認は現行形式の不具合修正である。再設計提案の採用や実装完了とはしない。検証済み基点と最終CIの区別は [監査の再評価](README.md#統合後の再評価) に従う。
