// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package mailbackend

import "testing"

const ourServer = "mail.example.com"

func TestAuthenticationOfReadsTheHeader(t *testing.T) {
	header := "mail.example.com; spf=pass smtp.mailfrom=example.com; dkim=pass header.d=example.com; dmarc=fail"
	got := AuthenticationOf([]string{header}, ourServer, true)
	if got.SPF != "pass" || got.DKIM != "pass" || got.DMARC != "fail" || got.Spam != "spam" {
		t.Fatalf("%+v", got)
	}
	clean := AuthenticationOf([]string{"mail.example.com; spf=none; dkim=temperror"}, ourServer, false)
	if clean.SPF != "none" || clean.DKIM != "temperror" || clean.DMARC != "" || clean.Spam != "" {
		t.Fatalf("%+v", clean)
	}
	if got := AuthenticationOf([]string{"mail.example.com; spf=not-a-result"}, ourServer, false); got.SPF != "" {
		t.Fatalf("%+v", got)
	}
}

func TestAuthenticationOfIgnoresAForgedHeaderBelowOurs(t *testing.T) {
	headers := []string{
		"mail.example.com; spf=fail smtp.mailfrom=bank.example; dkim=none; dmarc=fail header.from=bank.example",
		"mail.example.com; spf=pass; dkim=pass; dmarc=pass",
	}
	got := AuthenticationOf(headers, ourServer, false)
	if got.SPF != "fail" || got.DKIM != "none" || got.DMARC != "fail" {
		t.Fatalf("%+v", got)
	}
}

func TestAuthenticationOfDoesNotFallThroughToALowerHeader(t *testing.T) {
	headers := []string{
		"mail.example.com; spf=fail",
		"mx.example; spf=pass; dkim=pass; dmarc=pass",
		"mail.example.com; dkim=pass; dmarc=pass",
	}
	got := AuthenticationOf(headers, ourServer, false)
	if got.SPF != "fail" || got.DKIM != "" || got.DMARC != "" {
		t.Fatalf("%+v", got)
	}
	none := AuthenticationOf([]string{"mail.example.com; none", "mail.example.com; dmarc=pass"}, ourServer, false)
	if none != (Authentication{}) {
		t.Fatalf("%+v", none)
	}
}

func TestAuthenticationOfIgnoresOtherServers(t *testing.T) {
	for _, header := range []string{
		"mx.example; spf=pass; dkim=pass; dmarc=pass",
		"mail.example.com.evil; spf=pass; dkim=pass; dmarc=pass",
		"evil.mail.example.com; spf=pass; dkim=pass; dmarc=pass",
		"mail.example.co; spf=pass; dkim=pass; dmarc=pass",
		"evil.example (mail.example.com); spf=pass; dkim=pass; dmarc=pass",
		"(mail.example.com) evil.example; spf=pass; dkim=pass; dmarc=pass",
		"spf=pass; dkim=pass; dmarc=pass",
		"mail.example.com evil; spf=pass",
	} {
		if got := AuthenticationOf([]string{header}, ourServer, true); got != (Authentication{Spam: "spam"}) {
			t.Errorf("%q: %+v", header, got)
		}
	}
	if got := AuthenticationOf([]string{"mail.example.com; spf=pass"}, "", false); got.SPF != "" {
		t.Fatalf("an empty authserv-id matched: %+v", got)
	}
	if got := AuthenticationOf(nil, ourServer, false); got != (Authentication{}) {
		t.Fatalf("%+v", got)
	}
}

func TestAuthenticationOfComparesTheAuthservIDWithoutCase(t *testing.T) {
	for _, header := range []string{
		"MAIL.Example.COM; spf=pass",
		`"mail.example.com"; spf=pass`,
		"  mail.example.com 1 ; spf=pass",
		"(stalwart) mail.example.com (v1); spf=pass",
	} {
		if got := AuthenticationOf([]string{header}, "Mail.Example.Com", false); got.SPF != "pass" {
			t.Errorf("%q: %+v", header, got)
		}
	}
	if got := AuthenticationOf([]string{"mail.example.com 2; spf=pass"}, ourServer, false); got.SPF != "" {
		t.Fatalf("read an unknown version: %+v", got)
	}
}

func TestAuthenticationOfParsesCommentsReasonsAndProperties(t *testing.T) {
	header := "mail.example.com;\r\n" +
		"\tspf=none (dmarc=pass; dkim=pass) reason=\"dmarc=pass; dkim=pass\" smtp.mailfrom=a.example;\r\n" +
		"\tdkim = fail (bad signature) header.d=a.example header.b=\"abc/def=\";\r\n" +
		"\tDKIM/1=Pass header.d=b.example;\r\n" +
		"\tdmarc=(policy (nested) here)fail header.from=a.example"
	got := AuthenticationOf([]string{header}, ourServer, false)
	if got.SPF != "none" || got.DKIM != "pass" || got.DMARC != "fail" {
		t.Fatalf("%+v", got)
	}
	first := AuthenticationOf([]string{"mail.example.com; dkim=fail; dkim=neutral"}, ourServer, false)
	if first.DKIM != "fail" {
		t.Fatalf("%+v", first)
	}
}

func TestAuthenticationOfSkipsAMalformedHeader(t *testing.T) {
	for _, header := range []string{
		"mail.example.com; spf=pass (unclosed",
		`mail.example.com; spf=pass reason="unclosed`,
		"mail.example.com; spf=pass)",
		"",
	} {
		if got := AuthenticationOf([]string{header}, ourServer, false); got != (Authentication{}) {
			t.Errorf("%q: %+v", header, got)
		}
	}
	got := AuthenticationOf([]string{"mail.example.com; spf=pass (unclosed", "mail.example.com; spf=fail"}, ourServer, false)
	if got != (Authentication{}) {
		t.Fatalf("read past a malformed header: %+v", got)
	}
	below := AuthenticationOf([]string{"mail.example.com; spf=fail", "mail.example.com; spf=pass (unclosed"}, ourServer, false)
	if below.SPF != "fail" {
		t.Fatalf("%+v", below)
	}
}
