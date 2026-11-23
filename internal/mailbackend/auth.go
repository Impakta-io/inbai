// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package mailbackend

import "strings"

// Authentication is what the mail server recorded about a message.
// An empty field means that result was not on the message.
type Authentication struct {
	SPF   string `json:"spf,omitempty"`
	DKIM  string `json:"dkim,omitempty"`
	DMARC string `json:"dmarc,omitempty"`
	Spam  string `json:"spam,omitempty"`
}

var authValues = map[string]struct{}{
	"pass": {}, "fail": {}, "softfail": {}, "neutral": {}, "none": {},
	"temperror": {}, "permerror": {}, "policy": {}, "unknown": {}, "bestguesspass": {},
}

// AuthenticationOf reads SPF, DKIM, and DMARC from an Authentication-Results header.
// junk sets Spam to "spam". A message that is not marked junk leaves Spam empty.
func AuthenticationOf(header string, junk bool) Authentication {
	var out Authentication
	lower := strings.ToLower(header)
	out.SPF = authValue(lower, "spf=")
	out.DKIM = authValue(lower, "dkim=")
	out.DMARC = authValue(lower, "dmarc=")
	if junk {
		out.Spam = "spam"
	}
	return out
}

func authValue(header, key string) string {
	at := strings.Index(header, key)
	if at < 0 {
		return ""
	}
	rest := header[at+len(key):]
	end := 0
	for end < len(rest) && rest[end] >= 'a' && rest[end] <= 'z' {
		end++
	}
	value := rest[:end]
	if _, ok := authValues[value]; !ok {
		return ""
	}
	return value
}
