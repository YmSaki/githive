// SPDX-License-Identifier: AGPL-3.0-or-later

// Command githive-forge is the githive forge server
// (docs/12-forge-server.md, ADR-0019). It is a separate binary from the MIT
// githive CLI and is licensed AGPL-3.0-or-later (ADR-0010); see LICENSE in
// this directory.
//
// P8 starts with the pre-receive hook's namespace and fast-forward checks
// (ADR-0035). serve / shell / admin and the remaining push pipeline steps
// are added in later PRs.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		if code, ok := err.(exitCode); ok {
			os.Exit(int(code))
		}
		fmt.Fprintln(os.Stderr, "githive-forge:", err)
		os.Exit(2)
	}
}

// exitCode lets a subcommand finish with a specific status after it has
// already reported its own diagnostics.
type exitCode int

func (c exitCode) Error() string { return fmt.Sprintf("exit status %d", int(c)) }
