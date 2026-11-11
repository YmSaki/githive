# ADR-0032: 存在するプロジェクト設定の互換性不明を許可扱いにしない

- 状態：採用
- 日付：2026-11-11
- 関連：ADR-0018、ADR-0030、補償コード監査

## 文脈

schema_version のガードは、config ref が存在してもファイル欠損・JSON 破損・型違いを無条件で許可し、小数を int へ切り捨てていた。fsck は同じ設定を不正と報告できる一方、通常の読み書きは成功し、互換性の最低要求が不明なまま fold を実行できた。

## 決定

config ref の不存在は、従来どおり init 前の day-zero として許可する。存在する ref は config.json と正の整数 schema_version を必須とし、欠損・破損・非 object・文字列・小数・0 以下を typed invalid_config（終了コード5）として拒否する。将来の整数版は既存の typed schema_unsupported として拒否する。

整数性は JSON Schema と同じ数値意味で判断する。1、1.0、1e0 は同じ版とする一方、float64 丸めで整数になる小数は認めない。巨大指数を展開せず、入力長に比例する数字列検査で扱う。通常の読み書き、sync の両側、init の既存／追跡設定、fsck、meta merge が core/projectconfig の同一パーサーを使う。

init は追跡側 config を検査してから authoritative ref を一つでも採用する。sync も判定後に ref を進める。不正設定を空プロジェクトや version 0 へ補正しない。Git の rev-parse で ref 不存在と見なせるのは missing を表す exit 1 のみであり、設定破損や非リポジトリ等の実行失敗を不存在へ変換しない。

## 帰結

破損設定下では fsck による診断と明示的な復旧が必要になる。データ形式や有効なイベント集合の fold 結果は変えず、schema_version は昇格しない。既存の supported version 1 と init 前のローカル作業は維持する。

このガードが検査するのは互換性判断に必要な版情報である。meta の全項目に対する汎用 schema 検査、config.json と過去の meta.init data の再計算照合、既存の設定履歴の自動修復を実装したとは扱わない。
