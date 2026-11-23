// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

// Command inbaid is the control-plane server.
// This build only prints its version.
package main

import (
	"fmt"
	"os"

	"github.com/Impakta-io/inbai/internal/version"
)

func main() {
	fmt.Println(version.String(version.Server))
	os.Exit(0)
}
