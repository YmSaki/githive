// Command githive is the entry point for the githive CLI
// (docs/01-architecture.md「リポジトリ構成」). P0 only wires up the binary
// skeleton; subcommands land starting P1 (docs/13-roadmap.md).
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "githive: no subcommands implemented yet (P0 core-only build)")
	os.Exit(1)
}
