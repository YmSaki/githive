# ADR-0021: JSON を厳格に読み、イベント容量とネストを制限する

- 状態：採用
- 日付：2026-11-11
- 関連：issue #84、#79、docs/02、docs/11

## 文脈

encoding/json の通常 Decode は最初の値だけを読み、重複キーを後勝ちにし、不正 UTF-8 と孤立 surrogate を置換する。Go の map から作った非整数 v も切り捨てられていた。深い入力は canonical JSON のインデントで二次的に膨らみ、256 KiB の仕様上限も実装されていなかった。

## 決定

- generic JSON 読み取りは、単一の JSON 値と末尾の空白だけを許す。重複キーは escape を解いた後の文字列で判定し、階層を問わず拒否する。
- 不正 UTF-8 と孤立 surrogate は置換せず拒否する。正しい surrogate pair は Unicode scalar として受け付ける。
- 配列とオブジェクトの入れ子は最大128段。入力パース・pretty encoder・compact encoderに同じ上限を適用する。循環する Go の値もこの上限で停止する。
- イベント封筒の v は整数で、対応版の範囲内でなければならない。小数と整数への overflow を拒否する。
- 全 kind のイベント JSON は256 KiB（262144 bytes）以下とする。サイズには canonical JSON のインデントと末尾 LF を含み、人間向け summary と区切りの空行は含まない。raw 入力にも同じ上限をパース前に適用し、compact 入力の canonical 化後にも確認する。
- checkpoint も同じ上限とする。巨大な full state の compaction は失敗するが、対象 ref と元の履歴は変更しない。通常の読み取り・追記は checkpoint がなくても動作する。
- サイズ超過の書き込みは `event_too_large`、終了コード2。fsck は外部由来の上限超過イベントを同じ finding code で報告する。
- generic JSON（実体化ツリー等）へ256 KiB の容量制限は加えない。容量は1イベントの制約であり、長期に蓄積する状態の制約ではない。ネストとUnicodeの制限は共通で適用する。

## 帰結

正当な既存イベントの fold 規則と canonical bytes は変わらない。これまで仕様に反して受け入れていたデータは明示的なエラーになる。リポジトリの schema_version と封筒 v は昇格しない。Go と Python の strict JSON ベクタを共有し、受理・拒否の差を検査する。

checkpoint が大きくなるケースの将来の高速化は、別 blob 等の設計を別途決める。現時点で例外の大容量イベントを許して読込上限を曖昧にしない。
