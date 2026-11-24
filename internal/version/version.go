// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package version

// Version is the current project version. Release builds set this with -ldflags -X.
var Version = "0.0.0-dev"

const (
	// CLI is the operator command name.
	CLI = "inbai"
	// Server is the control-plane command name.
	Server = "inbaid"
)

// String returns the name and version printed by a binary.
func String(name string) string {
	return name + " " + Version
}
