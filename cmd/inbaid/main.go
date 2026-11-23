// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

// Command inbaid is the control-plane server.
package main

import (
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}
