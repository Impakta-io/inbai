// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

// Command inbai is the operator command-line client.
// This build only prints its version.
package main

import (
	"fmt"
	"os"

	"github.com/Impakta-io/inbai/internal/version"
)

func main() {
	fmt.Println(version.String(version.CLI))
	os.Exit(0)
}
