# ライセンス：AGPL-3.0-or-later

このディレクトリ以下は githive の Forge 専用ソースであり、**GNU Affero General Public License v3.0 以降（AGPL-3.0-or-later）** で提供する。全文は同じディレクトリの [LICENSE](LICENSE) にある。各ソースファイルの先頭にも `SPDX-License-Identifier: AGPL-3.0-or-later` を置く。

リポジトリのそれ以外の部分（共有する `internal/core`・`internal/app`、CLI の `cmd/githive`、spec、docs など）は MIT のままである（ルートの [LICENSE](../../LICENSE)）。
Forge は MIT の共有コードを利用するが、共有コードの側から Forge 専用コードを import することはない。この境界は `scripts/check-license-boundary.py` が CI で検査する。

方針は [ADR-0010](../../docs/adr/0010-licensing.md) と [ADR-0019](../../docs/adr/0019-forge-in-monorepo.md) を参照。
