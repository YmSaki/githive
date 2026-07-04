# 12. forge サーバー設計（githive-forge）

## 位置づけ

forge は「git bare リポジトリ + α」である。
githive のデータ形式はクライアントだけで完結しており、forge は次の 4 つの強制力と付加機能を加えるレイヤにすぎない。

1. push 時の権限強制と署名強制（hosted モードでは助言的だったものの格上げ）。
2. 通知の配信（WebHook、メール）。
3. LFS（大容量ファイル）。
4. CI（イベント駆動の実行アクション）。

この位置づけから導かれる設計原則：**forge が落ちてもデータは壊れず、機能は hosted モード相当に劣化するだけ**にする。
forge 固有の永続状態（ジョブ履歴等）を失っても、リポジトリの refs から再構成できる範囲に留める。

## 全体構成

```
                          +---------------------------------------+
 git client  --- SSH ---> | sshd(OpenSSH) -> githive-shell         |
             --- HTTPS -> | githive-forge (Go, 単一バイナリ)        |
                          |   smart HTTP (git http-backend 相当)   |
                          |   LFS batch API                        |
                          |   Web UI (読み取り専用から開始)          |
                          |   webhook dispatcher                   |
                          |   ci scheduler                         |
                          +----------------+----------------------+
                                           |
                          bare repos (fs)  |  jobs DB (SQLite)
                          lfs store (fs/S3)|
```

- 認証は SSH 公開鍵（users 台帳から authorized_keys を生成）と、HTTPS 用のトークン。
- リポジトリは通常の bare リポジトリ。`git push --mirror` で GitHub との間を自由に移動できる。
- 付随データベースは SQLite から始める（単一ホスト前提。分散化は目指さない）。

## push パイプライン（pre-receive）

pre-receive フックはコア層（`internal/core`）を再利用する Go バイナリで、ref 更新ごとに次を検証する。

1. **名前空間**：予約名前空間（`refs/githive-remote/**`。クライアントの追跡専用）への push だけは常に拒否する。それ以外の受け入れは forge 設定 `namespace_mode` で決める。既定は `open` で、GitHub 等と同様に write 権限を持つ利用者からの任意の refs（`refs/notes/**` 等を含む）を受け入れる。githive 自身が hosted モードでホスティングのこの緩さに依存している以上、forge が素の git より厳格な既定を持つべきではない。`strict` にすると `refs/heads/**`、`refs/tags/**`、`refs/projects/**` と設定で列挙した名前空間以外を拒否する（統制が必要な組織向けのオプトイン）。
2. **権限**：policy のルールに一致する ref はその評価（actor と action：push/create/delete/force）に従う。policy.json の `default` は `refs/projects/**` 内の未一致にのみ適用し、githive 自身の名前空間は常に統制下に置く。
3. **署名**：以降の githive 固有検証（3〜6）は `refs/projects/**` にのみ適用する。`refs/projects/**` への push は全コミットに有効署名を要求。actor と鍵の一致（[11](11-security.md) の検証規則）を確認。
4. **スキーマ**：イベント JSON の封筒検証、kind ごとのペイロード検証、canonical JSON 検証。ts が now + 5 分を超えるイベントは拒否（時計異常の LWW 占拠防止、[03](03-sync-and-concurrency.md)）。
5. **実体化の一致**：新コミットのツリーが、イベントから再計算したツリーと一致すること（改竄されたスナップショットの拒否）。
6. **fast-forward**：`refs/projects/*` は force push を常に拒否。分岐はクライアントの event-union マージで解決させる。

拒否時は理由をクライアントに返す（git は pre-receive の stderr を中継する）。

## 通知配信

post-receive で ref 更新イベントを内部キューに積み、dispatcher が処理する。

- `refs/projects/notify/*` への notify.post を検出し、targets を台帳で展開してメール送信。
- 任意の ref 更新を WebHook として外部 URL へ POST。ペイロードは「ref 名、旧/新ハッシュ、含まれるイベント封筒の配列」。署名ヘッダ（HMAC）を付ける。
- WebHook の登録は wiki でも issue でもなく forge の設定（管理 API）で行う。リポジトリデータに外部 URL と秘密を置かないため。

## 連番の払い出し（オプション機能）

forge は issue / task の人間向け連番（#123 形式）を到着順で払い出せる。
対応表 `refs/projects/meta/counters` を forge だけが書き、クライアントは対応表があれば `#123` の表示と検索を有効にする。
hosted モードではこの ref が存在しないだけで、他は何も変わらない。

### 割り当てアルゴリズム

```
post-receive（採番対象: refs/projects/issue/*, refs/projects/task/* の create）:
  1. サーバー内ミューテックスを取得（リポジトリ単位。push は並行に来るため必須）
  2. counters ref の現ツリーを読み、未採番のエンティティ ID を到着順に採番
  3. counters.json を更新するコミットを作成し、サーバー鍵で署名
  4. counters ref を fast-forward 更新し、ミューテックスを解放
```

```json
// counters.json
{
  "issue": { "next": 124, "map": { "01j8xq4d...": 123 } },
  "task":  { "next": 46,  "map": { "01j8xt5e...": 45 } }
}
```

- 採番は pre-receive（検証）ではなく post-receive（受理後）で行う。番号は派生データであり、採番の失敗が push を失敗させてはならない。
- counters ref の書き手はサーバーのみなので push 競合が原理的に起きない。エンティティ本体のチェーンには手を入れず、クライアントのデータ形式と hosted 互換に影響しない。
- 番号は永続とし、エンティティが archive されても再利用しない。

### 制約

- 「到着順」はサーバーが push を受理した順であり、作成時刻（ULID）順とは限らない。オフラインで作られたエンティティは push が遅れれば番号も後になる。
- GitHub 等から forge へ移行した場合、既存エンティティは移行時の一括採番（ULID 順）とし、以後は到着順とする。
- 番号が見えるのは sync 後である。CLI は番号を「あれば表示する別名」として扱い、ID（ULID）を正とする。

### 退けた代替案

サーバーがエンティティのチェーンへ `issue.number` イベントを追記する方式は、番号がエンティティに同伴する利点があるものの、ユーザーのチェーンにサーバーが書き込むことになり、署名の検証規則（actor と鍵の対応）に例外を持ち込む。
対応表方式なら「ユーザーのチェーンはユーザーだけが書く」を維持できる。

## LFS

git-lfs の Batch API を実装する。

- エンドポイント：`/<repo>/info/lfs/objects/batch`（JSON、download/upload オペレーション）。
- ストレージはローカル FS から始め、S3 互換をバックエンドとして追加できる interface を切る。
- 認証は HTTPS トークンまたは SSH 経由の `git-lfs-authenticate`。
- githive 機能との接点：wiki の `_assets` と issue 添付（将来イベント `issue.attach` を追加）が LFS ポインタを使えるようになる。

## CI（実行アクション）

GitHub Actions に相当する、イベント駆動のジョブ実行を提供する。

- 定義はリポジトリ内 `.workflows/*.yml` を第一に読み、`.githive/workflows/*.yml` も受け付ける。トリガは `push`（ブランチ）、`projects`（githive イベント。例 `issue.status == resolved`）、`schedule`。
- `.workflows/` という中立な置き場と最小スキーマは、実行系非依存のワークフロー標準（AGENTS.md が agent 指示書で果たした役割の CI 版）を狙った賭けである。成立条件は他実行系の採用なので、スキーマは on / jobs / steps / image 程度の最小に保ち、仕様は githive 本体から独立した文書（12a）として公開する。式言語やキャッシュ等の実行系固有機能をスキーマに足すときは、標準化の芽を摘む変更であることを自覚して判断する。
- forge の scheduler がトリガ評価とジョブのキューイングを行い、runner がコンテナ（Docker/Podman）でステップを実行する。
- runner は forge 本体と分離したプロセスとし、同一ホスト内から始めて、リモート runner（トークン登録制）へ拡張する。
- 実行結果は WebHook と notify.post（`ci` ユーザー名義）で報告する。ログは forge のローカル保存とし、リポジトリには書かない。

```yaml
# .workflows/test.yml の例
on:
  push: { refs: ["refs/heads/main"] }
jobs:
  test:
    image: golang:1.24
    steps:
      - run: go test ./...
```

ワークフロー仕様の詳細設計は CI フェーズ開始時に別ドキュメント（12a）として起こす。
本書ではトリガ・実行・報告の 3 分割と、runner 分離の構造だけを確定とする。

## Web UI

読み取り専用ビューから始める：issue 一覧/詳細、task ボード、chat、wiki レンダリング、通知ストリーム。
次の段階で書き込み UI（issue 起票・コメント・task 操作）を足す。
これにより git を持たないブラウザ利用者（PM、QA 等）が参加でき、Backlog / Jira の主要ユースケースを代替できる（[00](00-vision.md) の既知の制約の解除）。
実装方針：UI は React で開発し、静的ビルド成果物を go:embed でバイナリに同梱する。
React を選ぶ理由はデザイン面の資産（Storybook 等のコンポーネント開発環境）であり、実行形態の理由ではない。
Node のビルドパイプラインは開発時にのみ必要で、forge の実行時依存と単一バイナリ配布（デプロイ形態の節）は変わらない。

### Web 書き込みと代理署名

ブラウザには利用者の SSH 秘密鍵が無いため、Web UI からの書き込みは本人署名できない。
forge は **forge 署名鍵**でコミットに署名し、イベントの actor には操作した利用者の identity（email）を記録する。

- users 台帳に forge を `kind: server` のユーザーとして登録し、その鍵を「委任署名者」とする。
- 検証規則への追加：forge 鍵による署名は、任意の actor の代行として有効とみなす（委任）。信頼モデルは「サーバーを信頼する」であり、中央管理型トラッカーと同等。
- 本人署名（CLI/TUI/Agent 経由）と代理署名はイベントログ上で区別でき、監査時に「本人の鍵による操作か、Web 経由か」を追跡できる。
- Web セッションの認証は email + パスワードまたは OIDC とし、SSH 鍵とは独立に管理する。

## 将来機能の棚卸し（PR、Release ほか）

主要 forge（GitHub、GitLab、Gitea/Forgejo）の機能を、githive の線引きで分類しておく。
線引きの原理は本書の位置づけと同じで、**データは refs エンティティに、強制と保管だけを forge に置く**。
データ側に置けた機能は hosted モードと Agent からもそのまま使え、forge は後から強制力を足すだけになる。

| 機能 | 分類 | 判断 |
|------|------|------|
| PR / コードレビュー | データ + 強制 | 議論・承認・変更依頼は issue と同型の refs エンティティ（`refs/projects/review/<id>`、対象ブランチとコミットを指す）として設計できる。前例は git-appraise。forge 固有なのは「承認が揃うまで merge を拒否する」強制だけで、pre-receive + policy の拡張で足りる。v1 の非目標（[00](00-vision.md)）は維持しつつ、v2 候補の筆頭 |
| Release | データ + 保管 | タグ + リリースノート + 成果物一覧は refs エンティティ。バイナリ成果物の保管のみ LFS / オブジェクトストア（P10）の仕事 |
| branch protection / protected tags | 強制 | policy の `refs/heads/**` ルールと fast-forward 規則の拡張であり、実装が安く価値が高い。forge の早期候補 |
| required approvals / status checks | 強制 | review エンティティと CI（P11）の結果を merge 可否に接続する。PR 強制とセットで初めて意味を持つ |
| fork 管理 | サービス | サーバー内 fork（オブジェクト共有）。pull 型貢献フロー（[00](00-vision.md) の既知の制約の解除策）とセットで検討する |
| pull ミラーリング | サービス | GitHub 併用期の移行手段として価値が高い。push mirror は素の git で足りる |
| コード検索（Web） | サービス | Agent とローカルクライアントは clone を grep すれば足りる。ブラウザ利用者向けのみ。優先度低 |
| パッケージレジストリ | 保管 | 重量級であり非目標とする。ghcr / npm 等の既存レジストリと併用すればよい |
| stars / discovery / プロフィール | ネットワーク | 追わない。GitHub の堀であり、小規模 forge の勝負所ではない。機能水準の現実的な参照点は Gitea/Forgejo |

この表は構想であり、Horizon 4（[13](13-roadmap.md)）のコミットメントには含まれない。
着手時は各行を feature 仕様（review は features/review.md）として起こしてから実装する。

## デプロイ形態

- 単一バイナリ + 設定ファイル（TOML）。systemd unit の例を同梱する。
- 必須依存は system git と OpenSSH のみ。SQLite は埋め込み。
- バックアップ対象は bare リポジトリ群と LFS ストア。jobs DB は消えても運用が継続できる。
