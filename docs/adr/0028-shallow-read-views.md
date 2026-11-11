# ADR-0028: shallow 履歴の閲覧は明示的な snapshot view とする

## 状態

採用。

## 決定

不完全な project ref の閲覧は、通常の履歴 fold と別の read-view API を使い、head tree の現在状態を返す。CLI/MCP は read_source=snapshot、history_complete=false、verified=false を明示する。署名・履歴・整合性を検証したという意味ではない。通常の完全履歴の応答は変えない。

既存 Markdown 投影は元の本文が LF で終わらない場合に LF を補うため、元のイベント本文 `x` と `x\n` を区別できない。read view は本文を trim せず保存された Markdown 本文を返し、コメント・ノート・メッセージに body_source=rendered_markdown を付ける。元の本文の byte 一致を主張しない。body.md と JSON/JSONL の値はそのまま読む。

内部の参照解決、権限、署名、変更は read view を利用しない。project ref の到達可能な shallow 境界を検出し、不完全な履歴に依存する変更・log・verify・fsck・merge は shallow_history と取得すべき ref の案内を返す。自動 fetch/deepen や remote の推測は行わない。ソースブランチだけの shallow は、完全な project refs を阻害しない。

通常の破損・不正な snapshot はエラーであり、shallow 以外のエラーから snapshot へ自動 fallback しない。新しいツリーファイルや reducer 変更は加えず、schema_version を上げない。
