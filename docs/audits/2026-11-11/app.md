# app 補償コード監査

## 対象と判定の根拠

固定基準は `64e3198`。`c5da0be` の production 50 files を意味的に通読し、`64e3198` までの全 app 差分を再読した（追加 `idresolve/display.go` を含め51 files）。`64e3198..ae350d1` の app 差分はない。行番号は断りがなければ64e基準。テスト構造75 filesは別紙 [app-tests.md](app-tests.md) に委譲しており、全実行経路を網羅したという主張ではない。

監査対象は fallback/default、例外の変換・握り潰し、二重状態、互換分岐、再試行、修復。`docs/01-architecture.md` の Git を正とする契約、`docs/03-sync-and-concurrency.md` の追記・決定論的同期、`docs/features/chat.md` の未知 mention と通知、ADR0020/0023/0031、および関数自身の公開契約を照合した。必要な障害処理と除去可能な設計補償を区別した。

## 主要所見

### A01 設定の不明を対応済みとして受理する経路（確定、修正済み）

分類 DIRECT_CONTRADICTION。`entitychain/entitychain.go:91-128` は存在する config の欠損・壊れたJSON・型不正を対応済みへ縮約し、floatの整数化で1.5も1へ切り捨てた。`fsckapp/fsckapp.go:318-348` と `syncapp/syncapp.go:607-632` に別々の寛容 parser があり、同じ入力の判定が異なる。config ref自体がない day0 と、存在する設定を判定不能な場合は異なる状態である。

8099699 binary に対し正規 canonical event message を保ったまま config projection のみ置換し再現した。`schema_version:"999"` で Issue 作成が成功し fsck は invalid_config、`1.5` では作成とfsckがともに成功した。証拠は `config-proof.json` / `config-fraction-proof.json`、再現コードは `repro-config.py` / `repro-config-fraction.py`。最初のメッセージ改行混入を除去した再実行結果を保存してある。

根本不変条件は「操作前に既存の版情報を正確に分類する」。#191 の 927e1f7 / 610289e で共通 `core/projectconfig.Version`、typed invalid/future、init/sync/append/fsck/CLI判定を統一。欠損refのみday0許可、存在refの欠損/不正/非整数/非正数を拒否。巨大指数を展開せず exact decimal integer を判定する。RevParse/MergeInProgressも missingをexit1だけに限定した。これは版情報の閉包であり meta全schema・tree/data一致・署名信頼の完全検査ではない。

回帰は実Git CLI/MCP noappend、remote init/sync noadopt、fsck分類、day0/積分表記、Git badconfig/nonrepo。全Go、spec212/212、vet、canonical lint、gofmtを通過。F=4 I=8 D=2 Score=64（修正前）。除去対象は重複parserと不明→許可変換であり、将来版のtyped拒否やbootstrapは保持する。

### A02 受理境界が入口ごとに異なる（旧 gap は閉塞、設計負債は残る）

分類 NARROW_ABSTRACTION。c5の `initapp/initapp.go:181-190` は registryだけWalkChainし他refを直接採用した。8099699で `initapp/initapp.go:181-205` に全supported非wiki refのWalkChain/context/schema検査が入り、sync `event_ids.go:12-46`、Writer `entitychain.go:398-404`、verify/fsckにも同契約が追加された。旧init gapを現存不具合として数えない。fsck compactも初期検査後に exact CAS parent を再検査する（`compact.go:85-101`）。

これらは必要なfail closed処理である。ただし受理済み履歴という概念がないため入口が検査セットを列挙し続ける。第一原理案は exactOID＋contract版＋feature/entity context＋検査範囲を保持する VerifiedHistory を一つの境界で生成すること。旧reader/writer、未知kind warning、wikiの別プロファイルを列挙し、全入口の反例試験が揃うまでは重複検査を消せない。

### A03 checkpoint の保存状態を信じず再計算する二重状態（必要な安全策／性能負債）

`entitychain/entitychain.go:199-314` は完全履歴を検査し、checkpoint親もWalkし、Foldした状態と保存snapshotを比較して再計算状態を返す。`fsckapp/compact.go` は派生snapshotを生成する。これは不可信入力が権威ある履歴を上書きすることを防ぐ必要な補償で、検査を省く「最適化」は不可。

根因は外部snapshotに検証済みcacheとしての役割まで持たせたこと。第一原理案は events を唯一の正にし、local cacheをheadOID・fold規則版・検査範囲に結びつける。遠隔snapshotを高速経路にするなら親履歴との対応証拠と旧writer時の失効規則が必要。完全再走査をなくせるという性能保証は現在ない。

### A04 表示snapshotと権威状態、一覧の欠損情報

`issueapp/read_view.go:13-28`、task/chat同構造、`usersapp/read_view.go:16-42`、`notifyapp/read_view.go` は ShallowHistoryError のときだけsnapshotへ移り出所を明示する。`snapshotread/snapshot.go` は構造を検査するが完全履歴の真正性は証明しない。これは意図されたread-onlyプロファイルであり、不正入力一般を握り潰すfallbackではない。更新・ID参照解決はstrict経路を保持する。除去前提は完全履歴が利用可能になること、または操作ごとに証明範囲を要求する別型を用意すること。

一方 `issueapp/read.go:77-87`、task同箇所、`chatapp/read.go:73-83` は既知refのmeta欠損/非objectを黙ってskipし、別のIO/JSONエラーは一覧全体を失敗させる。分類 NARROW_ABSTRACTION。結果型 `[]Meta,error` が filtered/壊れた行/未読取を区別できない。完全性を保証した一覧と見せると総件数を誤解する（静的経路確認、実運用再現未実施）。F=3 I=4 D=2 Score=24。第一原理案は items＋per-ref problems＋readSource＋対象scope。全件replayを一覧に強制する提案ではない。

### A05 unknown mention と resolver 障害の混同

`notifyapp/auto.go:153-158` は resolver の全エラーを未知 @word として無警告で捨てる。`usersapp/mentions.go:26-32,39-42` も user lookup失敗後のgroup探索とmember失敗skipを行う。未知名を無視する契約自体は正しいが、registry IO/破損/不完全履歴は未知名ではない。分類 NARROW_ABSTRACTION（エラー分類喪失は静的に確定、通常postからの環境障害再現は未実施）。F=3 I=4 D=2 Score=24。

第一原理案は UnknownName と LookupFailure をtypedに分離し、前者だけ無視、後者は主操作の成功を維持して auto_notify_failed warningへ返す。任意副作用失敗を主イベントのrollbackに変えない。`notifyapp/auto.go:48-50` の設定read失敗→enabledも未設定既定値と故障が混在するが、故障時の可用性契約は未指定（UNDERSPECIFIED）。同様に `usersapp/agent.go` のProjectName defaultは表示名のfallbackであり認証許可ではない。

### A06 複合操作の一部保存を単一 error に縮約する

`notifyapp/notifyapp.go:139-173` は全IDを事前解決する点は良いが、複数ackの途中失敗で先行ackが永続化済みなのにnil,errorを返す。`usersapp/agent.go:173-194` のuser登録後KeyAdd失敗では秘密鍵を保持する（消すと保存済み参照を壊すため正しい）が、結果はerror文字列中心。wiki saveもlocalref更新後push/cleanup失敗時に空SaveResultへ縮約する分岐がある（`wikiapp/write.go:346-388`）。分類 NARROW_ABSTRACTION、途中失敗を注入しない通常成功試験では観測しにくい。F=3 I=5 D=3 Score=45。

第一原理案は app 所有の CommandResult に committed effects・失敗effects・publication状態・再試行単位を含めること。再送には操作ID/冪等性が必要。append-onlyイベントを巻き戻す、保存済み資格情報を削除する、単にretryを増やす方針は不可。#108のsync部分結果出力は境界上の改善だが全複合操作の結果モデル統一ではない。

### A07 wiki編集sessionの複数の正と局所修復

`wikiapp/write.go:351-355,488-497` は現在refを読み直してCASするが、現在tipがsessionのworktree HEADの祖先かを確認しない。一方 `saveLocallyAfterSyncFailure:463-473` には祖先検査がある。CAS成功だけでは「読み直した新しいtipを古いsessionで上書きしない」を保証しない。外部Git writerが並走する場合の履歴脱落候補（静的確認のみ、実再現/契約確定が未了なので確定bugや完了修正とは数えない）。

`cleanup:508-515` はWorktreeRemove失敗時にもconfigをunsetしsession pointerを失う可能性、`filesWithConflictMarkers:576-578` はReadFile全失敗を削除扱いする。後続stage/tree検査が別途拒否する場合もあり、不正受理を証明したとは言わない。`syncapp/wiki.go:86-87` はStat全エラーを編集なしへ縮約し、Edit側のNotExistだけを区別する処理と異なる。

第一原理案は session のbaseOID/worktreeHEAD/local publicationを明示し、保存時はbaseに対するCASか非FF拒否・明示merge、cleanupは所有資源削除成功後にpointerを解除する。remote push後tracking更新失敗をbest effortにすること、offline編集用のworktree保持は必要な回復処理である。障害をsilentにすることと資源を残すことを分ける。

## 消してはいけない補償と削除条件

|機構|現状の意味|削除・統合の前提|
|---|---|---|
|Writer AppendOntoのCAS再構築、status_parent|正確な親に依存するfromを再生成、lost update防止|永続化backendの同等な原子的更新契約|
|syncのbounded nonFF retry、per-ref結果、errors.Join|分散競合と部分成功を表す|競合自体は消せない。結果所有をappへ集約する|
|Fetch prune|消えたremote refのtrackingを更新|remote inventoryの別の信頼できる更新方式|
|process lock|go-git object/ref更新の同一process競合防止|backend側の保証とWindows含む反例試験|
|信頼root pin/divergence処理|信頼の選択と同期収束を分ける|信頼判断を単純mergeへ置換しない。pin選択結果を型で表す|
|全IDのweaklink、拡張source.kind|offline/拡張schema契約|契約変更と旧reader/writer移行なしに存在確認を強制しない|
|#130 shortest unique prefix|同じresolver候補集合上で表示IDを使えるようにする|JSON full IDは維持。filter/page前の全候補計算が必要|
|verifyのunknownkind warning、早期構造失敗|未対応拡張と確定破損の区別|warningをPASS同義にせず検査範囲を明示|

`doctorapp/doctorapp.go:140-175` のremote時刻走査は全tracking refをWalkChainしwiki raw commitで診断がwarningへ落ちる候補がある。これは認証のfailopenではなく診断の対象scopeの不一致（未再現）。`verifyapp.VerifyAll` の途中環境エラーで先行reportを失う点もA06の結果モデルに含む。rendererの型assert/default/skipはtyped fold状態の契約に依存するため、それだけを新たな破損受理bugと数えない。型付stateと入口検査への移行前に単純削除しない。

## 全ファイル coverage ledger

各行は production ファイル全体（記載行数まで）の意味読取り。T=必須な障害/競合処理、D=設計補償/境界整理候補、P=主経路/型・投影。表の所見番号がないファイルも読取り対象に含む。テストは別紙の構造監査でありこの表の意味読取り件数に混ぜない。

|64e3198のpath (`internal/app/` 以下)|読取範囲|分類/主責務|関連|
|---|---|---|---|
|`apperr/apperr.go`|1–32|P|typed error|
|`chatapp/chatapp.go`|1–177|P/T/D|A04|
|`chatapp/read.go`|1–120|P/T/D|A04|
|`chatapp/read_view.go`|1–39|P/T/D|A04|
|`chatapp/tree.go`|1–81|P/T/D|A04|
|`doctorapp/doctorapp.go`|1–225|P/D|診断scope|
|`entitychain/clock.go`|1–44|T/D|A01,A02,A03|
|`entitychain/entitychain.go`|1–457|T/D|A01,A02,A03|
|`fsckapp/compact.go`|1–234|T/D|A01,A02,A03|
|`fsckapp/fsckapp.go`|1–387|T/D|A01,A02,A03|
|`fsckapp/recompute.go`|1–139|T/D|A01,A02,A03|
|`fsckapp/wiki.go`|1–37|T/D|A01,A02,A03|
|`idresolve/display.go`|1–69|P/T|#129,#130|
|`idresolve/idresolve.go`|1–161|P/T|#129,#130|
|`initapp/initapp.go`|1–284|T/D|A01,A02|
|`issueapp/issueapp.go`|1–104|P/T/D|A04|
|`issueapp/operations.go`|1–150|P/T/D|A04|
|`issueapp/read.go`|1–175|P/T/D|A04|
|`issueapp/read_view.go`|1–39|P/T/D|A04|
|`issueapp/status_parent.go`|1–31|P/T/D|A04|
|`issueapp/tree.go`|1–99|P/T/D|A04|
|`logapp/logapp.go`|1–140|P/T|履歴表示|
|`notifyapp/auto.go`|1–192|P/D|A04,A05,A06|
|`notifyapp/notifyapp.go`|1–182|P/D|A04,A05,A06|
|`notifyapp/read.go`|1–115|P/D|A04,A05,A06|
|`notifyapp/read_view.go`|1–24|P/D|A04,A05,A06|
|`notifyapp/tree.go`|1–69|P/D|A04,A05,A06|
|`snapshotread/snapshot.go`|1–261|T/D|A04|
|`syncapp/discovery.go`|1–108|T/D|A01,A02,A06,A07|
|`syncapp/event_ids.go`|1–46|T/D|A01,A02,A06,A07|
|`syncapp/meta_conflict.go`|1–26|T/D|A01,A02,A06,A07|
|`syncapp/syncapp.go`|1–634|T/D|A01,A02,A06,A07|
|`syncapp/trustroot.go`|1–186|T/D|A01,A02,A06,A07|
|`syncapp/wiki.go`|1–90|T/D|A01,A02,A06,A07|
|`taskapp/operations.go`|1–102|P/T/D|A04|
|`taskapp/read.go`|1–150|P/T/D|A04|
|`taskapp/read_view.go`|1–39|P/T/D|A04|
|`taskapp/status_parent.go`|1–31|P/T/D|A04|
|`taskapp/taskapp.go`|1–101|P/T/D|A04|
|`taskapp/tree.go`|1–82|P/T/D|A04|
|`usersapp/agent.go`|1–272|P/D|A05,A06|
|`usersapp/email_ownership.go`|1–23|P/D|A05,A06|
|`usersapp/keys.go`|1–93|P/D|A05,A06|
|`usersapp/mentions.go`|1–92|P/D|A05,A06|
|`usersapp/policy.go`|1–69|P/D|A05,A06|
|`usersapp/read_view.go`|1–77|P/D|A05,A06|
|`usersapp/tree.go`|1–57|P/D|A05,A06|
|`usersapp/usersapp.go`|1–348|P/D|A05,A06|
|`verifyapp/verifyapp.go`|1–360|T/D|A02,A06|
|`wikiapp/wikiapp.go`|1–86|T/D|A07|
|`wikiapp/write.go`|1–636|T/D|A07|

## 証拠と移行の受入条件

- 51 production filesの読取＋c5→64e全差分再anchor。116追加のschema/context検査、ignored status warning、users listの共通sort、130表示ID差分を確認した。
- 実再現した設定版不整合と静的懸念を区別した。wiki並行writer、mention IO、doctor raw wikiは未実行なので確定再現とはしない。
- #191追加テストは監査epoch後の `revision_errors_test.go`、`projectconfig/version_test.go`、`cmd/githive/config_guard_test.go`。既存75 app testsの構造件数には含まない。
- 再設計の受入反例:同じOIDをinit/sync/append/fsckが異判定しない、shallow表示が書込根拠にならない、snapshot偽装が正へ昇格しない、unknown mentionとIO故障を区別する、部分保存済み操作の再送で重複しない、session中の別writerを失わない、旧writerや未知kindの互換プロファイルを明示する。
- 提案採用は未決。既存fixを再設計待ちにせず、検査削除は上記反例と旧履歴移行条件を満たしてから行う。

## A07 追補: 0cb7bf5で最小反例を確認

`repro-wiki-local-advance.py` / `wiki-local-cas-proof.json` は実Gitで同一baseからeditorと別writerのsiblingを作り、旧current再読CASが別writerを現在履歴から落とすことを確認した。新祖先guardの手順は分岐を拒否しcurrentを保持する。これはGo app実行の代替ではなくref更新アルゴリズムの再現。修正は両保存末尾で祖先検査後のexactcurrent CAS、typed競合・両OID/path案内、worktree/config保持。Go regressionはpublic no-sync Saveとpush-tail、明示merge後の再保存を含むがPID枯渇により未実行。F=3 I=8 D=3 Score=72。ADR0034。
