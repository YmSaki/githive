# ADR-0030: 既存スキーマと ref 文脈を検証境界で照合する

## 状態

採用（Issue #116）。

## 決定

- kind と ref feature、per-entity ref の ID と envelope.entity が一致することを検査する。sync / init の取り込みで不一致を採用せず、fsck / verify は対象コミットを報告する。
- `spec/schemas/*.json` 自体を Go の embed で配布し、既に依存している google/jsonschema-go の Draft2020-12 検証を使う。コピーした別スキーマ、実行環境のファイル、ネットワーク上の参照には依存しない。
- data の既知 kind の制約を検査し、型・required・enum 等をスキーマから得る。未知フィールドを保持し、空の edit、opaque な source.kind、due の pattern だけを使う既存の制約、既知の旧 envelope.v の受理を変えない。JSON 数値の integer 判定は丸め前に確認する。
- 未知 kind は従来の forward compatibility に従って reducer で無視し、検査コマンドは警告する。ref の feature/entity 不一致と、既知 kind の不正 data は別の破損として扱う。
- 生のイベント本文が canonical encoding と一致するか fsck で照合する。イベント自体を書き換えない。
- fold の意味論と実体化ツリーは変更しない。schema_version は据え置く。追加の鍵・email・policy の app 安全検査は維持する。

## 制限

状態遷移が reducer に無視された理由の完全な列挙は別の監査である。スキーマに宣言していない暦の実在性やメール配送可能性を新たに要求しない。未知 kind を解釈・実行する機能を追加するものではない。
