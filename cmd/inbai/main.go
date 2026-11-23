// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

// Command inbai is the operator command-line client.
package main

import "os"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}
