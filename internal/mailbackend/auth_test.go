// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package mailbackend

import "testing"

func TestAuthenticationOfReadsTheHeader(t *testing.T) {
	header := "mail.example.com; spf=pass smtp.mailfrom=example.com; dkim=pass header.d=example.com; dmarc=fail"
	got := AuthenticationOf(header, true)
	if got.SPF != "pass" || got.DKIM != "pass" || got.DMARC != "fail" || got.Spam != "spam" {
		t.Fatalf("%+v", got)
	}
	clean := AuthenticationOf("spf=none; dkim=temperror", false)
	if clean.SPF != "none" || clean.DKIM != "temperror" || clean.DMARC != "" || clean.Spam != "" {
		t.Fatalf("%+v", clean)
	}
	if got := AuthenticationOf("spf=not-a-result", false); got.SPF != "" {
		t.Fatalf("%+v", got)
	}
}
