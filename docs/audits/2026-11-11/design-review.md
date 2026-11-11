# 第一原理案の独立レビュー

対象: architecture.md stable draft（観測 epoch 64e3198、Proposal / 未採用）。照合: core.md C01–C10、docs/02・03・11、ADR0018/0020/0025/0026/0027。コード変更なし。本レビューは設計提案の実装準備性を評価し、現行PRの不具合や実証結果を追加で偽認定するものではない。

判定: **現形式を保つ責務共有の設計検討は継続可。補償の実除去・cache fast path・通知永続化・意味論移行の実装は条件未充足。** architecture.md の CONDITIONALLY_READY / NOT_READY の区別に同意する。以下は提案を棄却する理由ではなく、対応 Phase 4/5/7 で埋めるべき closure である。

## DR-01 cache のキーと証拠は別物（高優先、実装前必須）

対象: ARC-02/04、移行条件3、AC-03。

良い点: snapshot は authority でない、署名者だけを信用しない、full fold比較と unknown profileを保つ方針は ADR0020 に一致する。

穴: `(head OID, reducer version, scope, trust-policy context)` に値を格納するだけでは、その値を本当に独立検証した証拠にならない。攻撃者が state と cache横のdigestを同時に差替える反例を、キー一致だけでは排除できない。「独立検証済み」という新truthの発行・保存・再利用条件が未定義。

修正提案: cacheの threat boundary（受信Git checkpointは不可信、ローカルcacheを変更できる主体をどこまで脅威に含むか）を明示する。evidence record の issuer、作成時の実全走査、state digest、source OID集合、contract/schema/reducer fingerprint、coverageを型で保持する。同じcache内の自己申告hashだけを証明と呼ばない。保護されたprocess-local evidence、OS保護のlocal store、再検証のどれを採るか選び、対象脅威内で偽造不能と説明できないcacheは拒否／再計算する。

さらに content/materialization証拠と authentication証拠を分ける。docs/11は署名鍵判定に HEAD registry、台帳変更権限判定に親までのregistry、trust rootにpin/唯一rootを使う。一般名 `policy context` だけでなく、必要な users head OID、pin値、関係config、検証profile/adapter能力を依存として列挙する。同じissue headでもregistryにrevokeを追記した場合は認証cacheを無効化する。pure fold内容だけのcacheまで同じ変更で無効にする必要があるとは限らない。

受入追加: state+digest同時改竄、registryのみのrevoke、pinのみ変更、同一版名だがschema bytes変更、snapshot-only証拠の混入で false verified がないこと。

## DR-02 receipt は durable retry identity を代替しない（高優先、保証を限定）

対象: ARC-03「再実行が二重イベントを生まず」、AC-02/05。

穴: 既存の独立 ref を保ったまま、task create成功後link前にprocess終了した場合、receiptそのものが返らない。再試行で新taskIDを採番すればduplicate taskになる。outboxを導入しても元操作との同一性がなければ二重postを防げない。frozen recipientsだけでは冪等性は証明できない。

修正提案: command identity を誰がいつ生成し、どの不変な記録へ残し、同一request再送と別操作を区別するかをPhase5で決める。選択前は `receiptが既知の部分結果を失わない` だけを現形式prototypeの保証とし、exactly-once/二重eventなしを実装済前提にしない。保存済origin IDから再開する方法は、callerがそのIDを失ったcrash/timeoutにも回復路があることを示す。新dedupe/outbox記録がauthorityなら、reachability/競合解決/保持期間/容量上限が必要。

受入追加: create後receipt前、link後通知前、outbox保存後配送応答前に終了し、別process/端末から同じ操作を再開する。ローカル保存成功を取り消さず、duplicate防止できない境界はpartial recoveryとして明示する。

## DR-03 旧readerの書込防止はHostedの能力を超えない（高優先、契約限定）

対象: 移行条件4、AC-04「旧readerは明示非対応」。

穴: config schema_version昇格を観測した対応旧clientは停止できるが、offline旧cloneは旧configで書き続けられる。raw Git writerは任意ref更新が可能で、Hostedにserver enforcementはない。新namespace採用だけでは旧entity refsへの継続書込み／stale viewを止められない。ADR0018もconfig伝播前の経路に封筒vを必要とする。

修正提案: 対象旧clientの具体的な最低版とgating実装、configとfeatureの取得・検査順、旧cloneによる遅延branch再合流の拒否／隔離／変換手順を契約にする。Hostedの保証を「全旧writerを物理的に止める」でなく「新readerは旧意味論区間を新区間として採用しない、観測した対応clientは停止する」と限定する。新namespaceの存在を知らない旧clientの読取りはstaleであり、明示unsupportedを必ず表示できるとは約束しない。

受入追加: migration前にoffline化した旧cloneが操作して後日push、configだけ／entityだけをfetchするclone、raw Gitで旧v追加。署名済み旧eventは保全し、静かに新順序へ変換しない。

## DR-04 migration authority と到達性の全依存集合（高優先、format採用前必須）

対象: 移行条件4–6、単一journal/transaction root案。

良い点: hash文字列はGit reachabilityにならない、旧commitをrebase/resignしない、invalid履歴を勝手に正規化しないという制約は適切。

残余: `旧headを保存したmigration record` の書き手がどの旧trust root/policyで承認されるか、複数refのどの同時観測点を移行するか、新namespaceで旧署名をどう検証するかは未決定。先にnewstateを信用して新trustrootを選ぶと循環する。旧issue headのみ保持して元users/config/rootを失うと、元署名のOIDは残っても検証文脈を再現できない。

修正提案: migration入力は全feature headsだけでなく必要なmeta/config、users、trust-root根拠をOID集合として固定し、変動をCAS/transaction/再読込で検出する。新recordの権限は旧区間の検証済みauthorityで判定し、単独snapshotや自己登録rootでbootstrapしない。全依存objectを親/tree等のGit辺で保全し、必要refだけfetchしたclean clone + GC後にも再現できることを示す。複数source heads/pin競合を人の明示決定なしに一つへ丸めない。

受入追加: registry/configが移行作業中に変化、複数trustroots、移行recordの無権限署名、古いrefs削除後GCとclean fetch。元segmentの署名・権限・foldを独立に再計算できるかを観測する。

## DR-05 fresh proof は mutable State pointer と同義でない（中優先、API詳細設計）

対象: ARC-01/02/04の accepted history、typed state、QueryView。

穴: exact OID付きevidenceがあっても、materialize.Stateはmutable mapを含み、FoldFromは渡されたstateを更新する。証拠に結合したsnapshotをcallerが後で変更できるAPIでは、検証済みラベルだけが残る。ViewStateとValidatedRegistryが同じmap型を流れると、shallow projectionがwrite authorityへ入り得る。

修正提案: evidenceとimmutable入力/出力のownershipを同時に決める。opaque AcceptedHistory / MaterializedAt / AuthenticatedRegistry と read-only SnapshotView を型上区別し、更新時にcopyまたは所有権移転し旧証拠を失効させる。contextは現在のref名だけでなく実OIDを含める。純cacheと署名証拠を同じmutableポインタにぶら下げない。

受入追加: cache stateを返した後のcaller mutation、View API値のwrite authority入力、refが進んだ後の旧proof再利用を検出する。

## DR-06 typed-state化・Scratch削除の安全な小単位（中優先、除去候補の具体化）

対象: core C03/C04、ARC-01/04。

現callgraphでは StateFromMap/ScratchDecoder はtests以外から呼ばれず、checkpointBaseは親をfull foldしてtyped stateを返す。この復元plumbingの削除と、StateToMap wireや現reducersのScratchを削除する変更を分けるべき。前者は実callgraphを閉じれば小さい局所変更、後者はpure foldのbyte contractとcheckpoint既存payloadを含む。

修正提案: Step1ではdecoder registration/decoder-only helpersの無参照確認と削除だけを候補とする。Step2はfeature typed-state ownershipを設計し、StateToMap projection bytesを独立vectorsで維持する。StateToMapそのもの、snapshot入力検証、現行fullfold fallbackを「dead checkpoint層」として一括削除しない。wire/fold結果が変わればADR0018に従い二つの版を扱う。

受入追加: git checkpoint/compact/fsck使用と全fold vectorsが不変、malicious snapshot拒否が保たれ、旧checkpointは正常fullfoldへfallbackすること。

## 変更不要と判断した境界

- CAS、repo lock、bounded retry、strict codec、schema/ref context、ID collision/key orderは必要条件として明記され、無条件削除の提案ではない。
- revoked keysをallowed_signersに残し過去署名を検証する契約と、fingerprint不明時の保守的fallbackは維持が必要。stable KeyID案は旧pub文字列をsilent normalizeしない限り妥当な検討対象。
- migration proposalは現行foldを変えず、Phase4/5に戻すと宣言している。現行PRの動作を新truthに合わせ直す根拠にはならない。
- single journal/manifestは比較案であり、追加refを作れば分散取引が解決するとは主張していない。採用判断は未了で妥当。

## 未監査・判定限界

新formatの具体schema、migration実装、durable outbox実装、cache証明実装は存在しないため、その正しさや署名保全の実証は未完了。ここでの六件は実装バグIssueとしてのF/I/D採点対象ではなく、未採用案のcontract closure条件である。現行のlocale/RevParse/config不具合は別実装修正の担当であり、設計案への賛否で成否を変更しない。
