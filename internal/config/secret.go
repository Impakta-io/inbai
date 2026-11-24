// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package config

import "fmt"

const (
	// MinSigningKeyBytes is the shortest signing key the server accepts.
	// Access tokens are HMAC-SHA256 signed, and any agent holds one. A short
	// key lets that agent search for the key offline and then mint operator tokens.
	// 32 bytes is the HMAC-SHA256 output size and what the server generates.
	MinSigningKeyBytes = 32

	// MinOperatorTokenLen is the shortest operator token the server and CLI accept.
	// The token is a bearer secret that anyone who can reach the server may guess.
	// 32 characters of random hex is 128 bits, which is out of reach of guessing.
	// Generated tokens are 68 characters.
	MinOperatorTokenLen = 32
)

// CheckOperatorToken reports whether token is long enough to be an operator token.
func CheckOperatorToken(token string) error {
	if len(token) < MinOperatorTokenLen {
		return fmt.Errorf("the operator token (INBAI_OPERATOR_TOKEN) is %d characters. The minimum is %d. Generate one with: openssl rand -hex 32", len(token), MinOperatorTokenLen)
	}
	return nil
}

// CheckSigningKey reports whether key is long enough to sign access tokens.
// An empty key is allowed because the server then generates one.
func CheckSigningKey(key []byte) error {
	if len(key) > 0 && len(key) < MinSigningKeyBytes {
		return fmt.Errorf("the signing key (INBAI_SIGNING_KEY) is %d bytes. The minimum is %d. Generate one with: openssl rand -hex 32", len(key), MinSigningKeyBytes)
	}
	return nil
}
