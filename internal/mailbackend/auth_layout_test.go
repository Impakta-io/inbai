// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package mailbackend

import (
	"strings"
	"testing"
)

// These header blocks are what stalwartlabs/stalwart:v1 stored, in order,
// for mail to an inbox on mail.example.test. Host names and addresses are
// anonymised, and long DKIM and spam-filter values are cut. Each message
// carries a forged field that names our server and claims a pass.
const (
	layoutHost = "mail.example.test"

	forgedPass = "Authentication-Results: mail.example.test; spf=pass smtp.mailfrom=outside.example.test; dkim=pass header.d=outside.example.test; dmarc=pass header.from=outside.example.test\r\n"

	stalwartInboundTrace = "Delivered-To: alice@example.test\r\n" +
		"X-Spam-Status: Yes\r\n" +
		"Received: from relay.outside.example.test (unknown [192.0.2.1])\r\n" +
		"\tby mail.example.test (Stalwart SMTP) with ESMTP id 4C0599E33A00001;\r\n" +
		"\tTue, 24 Nov 2026 15:55:42 +0000\r\n" +
		"Authentication-Results: mail.example.test;\r\n" +
		"\tspf=none (mail.example.test: no SPF records found for postmaster@relay.outside.example.test) smtp.helo=relay.outside.example.test;\r\n" +
		"\tspf=none (mail.example.test: no SPF records found for ceo@outside.example.test) smtp.mailfrom=ceo@outside.example.test;\r\n" +
		"\tiprev=permerror (dns record not found) policy.iprev=192.0.2.1;\r\n" +
		"\tdmarc=none header.from=outside.example.test policy.dmarc=none\r\n" +
		"Received-SPF: none (mail.example.test: no SPF records found for ceo@outside.example.test)\r\n" +
		"\treceiver=mail.example.test; client-ip=192.0.2.1; envelope-from=\"ceo@outside.example.test\"; helo=relay.outside.example.test;\r\n" +
		"X-Spam-Result: ARC_NA (0.00),\r\n" +
		"\tDKIM_NA (0.00),\r\n" +
		"\tRDNS_NONE (2.00)\r\n" +
		"X-Spam-Score: spam, score=11.20\r\n" +
		"Return-Path: <ceo@outside.example.test>\r\n"

	stalwartSignature = "DKIM-Signature: v=1; a=ed25519-sha256; s=v1-ed25519-20261124; d=example.test; c=relaxed/relaxed; r=y;\r\n" +
		"\th=Date:Message-ID:Subject:To:From; t=1795535801; bh=u5j8L8MB6c95fNAGW1gDwrJ\r\n" +
		"\tfntk4hoUsTG98ojM00ec=; b=rgIcAugvPpU7GGXFjyVkG6Y5eA==;\r\n" +
		"DKIM-Signature: v=1; a=rsa-sha256; s=v1-rsa-20261124; d=example.test; c=relaxed/relaxed; r=y;\r\n" +
		"\th=Date:Message-ID:Subject:To:From; t=1795535801; bh=u5j8L8MB6c95fNAGW1gDwrJ\r\n" +
		"\tfntk4hoUsTG98ojM00ec=; b=ETJybm+YrMzizAPbu0tWmoYg76==;\r\n"

	ceoMessage = "From: CEO <ceo@outside.example.test>\r\n" +
		"To: alice@example.test\r\n" +
		"Subject: quarterly numbers\r\n" +
		"Message-ID: <case.1795535742@outside.example.test>\r\n" +
		"Date: Tue, 24 Nov 2026 12:00:00 +0000\r\n"

	// Case 1: inbound SMTP on port 25, forged field below the sender's From.
	layoutInbound = stalwartInboundTrace + ceoMessage + forgedPass

	// Case 4: inbound SMTP, forged field as the first line of the message.
	layoutInboundForgedFirst = stalwartInboundTrace + forgedPass + ceoMessage

	// Case 2: authenticated submission on 465 with Stalwart's default
	// settings. Stalwart adds no Received and no Authentication-Results.
	layoutSubmission = "Delivered-To: alice@example.test\r\n" +
		"X-Spam-Status: No\r\n" +
		stalwartSignature + ceoMessage + forgedPass

	layoutSubmissionForgedFirst = "Delivered-To: alice@example.test\r\n" +
		"X-Spam-Status: No\r\n" +
		stalwartSignature + forgedPass + ceoMessage

	// Case 2 with addAuthResultsHeader on, as inbai init sets it.
	layoutSubmissionWithSetting = "Delivered-To: alice@example.test\r\n" +
		"X-Spam-Status: No\r\n" +
		stalwartSignature +
		"Authentication-Results: mail.example.test; none\r\n" +
		forgedPass + ceoMessage

	// Case 3: a JMAP send from alice to bob through inbaid, default settings.
	layoutLocalSend = "Delivered-To: bob@example.test\r\n" +
		"X-Spam-Status: No\r\n" +
		stalwartSignature +
		"To: <bob@example.test>\r\n" +
		"From: <alice@example.test>\r\n" +
		"Subject: case c3\r\n" +
		"Message-ID: <18eb0654bc99926b.990367087552a472.500664e0b911b82b@mail.example.test>\r\n" +
		"Date: Tue, 24 Nov 2026 15:57:17 +0000\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=\"utf-8\"\r\n" +
		"Content-Transfer-Encoding: 7bit\r\n"

	// Case 3 as a JMAP Email/set that writes its own Received and
	// Authentication-Results, with addAuthResultsHeader on. Stalwart's
	// internal session names itself localhost.
	layoutLocalSendForged = "Delivered-To: bob@example.test\r\n" +
		"X-Spam-Status: No\r\n" +
		stalwartSignature +
		"Authentication-Results: localhost; none\r\n" +
		"From: \"CEO\" <ceo@outside.example.test>\r\n" +
		"To: <bob@example.test>\r\n" +
		"Subject: case c3forged\r\n" +
		"Received:  from relay.outside.example.test (relay.outside.example.test [203.0.113.9])\r\n" +
		"\tby mail.example.test (Stalwart SMTP) with ESMTPS id 4C0000000000001;\r\n" +
		"\tTue, 24 Nov 2026 12:00:00 +0000\r\n" +
		forgedPass +
		"Message-ID: <18eb06c3304a85c1.4e0abc78fcb85c98.464c4e63fe71533a@mail.example.test>\r\n" +
		"Date: Tue, 24 Nov 2026 16:05:11 +0000\r\n" +
		"MIME-Version: 1.0\r\n"
)

// forgedTrace is the most a sender can write: a Received and an
// Authentication-Results field that copy what our server writes.
const forgedTrace = "Received: from relay.outside.example.test (unknown [192.0.2.1])\r\n" +
	"\tby mail.example.test (Stalwart SMTP) with ESMTP id 4C0599E33A00001;\r\n" +
	"\tTue, 24 Nov 2026 15:55:42 +0000\r\n" + forgedPass

// fieldsOf splits a header block the way JMAP's headers property returns it:
// each field's value is the raw text after the colon, folding kept.
func fieldsOf(t *testing.T, block string) []HeaderField {
	t.Helper()
	var out []HeaderField
	for _, line := range strings.Split(strings.TrimSuffix(block, "\r\n"), "\r\n") {
		if line[0] == ' ' || line[0] == '\t' {
			out[len(out)-1].Value += "\r\n" + line
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("not a field: %q", line)
		}
		out = append(out, HeaderField{Name: name, Value: value})
	}
	return out
}

func TestAuthenticationOfCapturedStalwartLayouts(t *testing.T) {
	stalwartInbound := Authentication{SPF: "none", DMARC: "none"}
	cases := []struct {
		name   string
		layout string
		want   Authentication
	}{
		{"inbound, forged below From", layoutInbound, stalwartInbound},
		{"inbound, forged first line", layoutInboundForgedFirst, stalwartInbound},
		{"submission, forged below From", layoutSubmission, Authentication{}},
		{"submission, forged first line", layoutSubmissionForgedFirst, Authentication{}},
		{"submission, forged Received and results first", "Delivered-To: alice@example.test\r\n" + stalwartSignature + forgedTrace + ceoMessage, Authentication{}},
		{"submission with the setting", layoutSubmissionWithSetting, Authentication{}},
		{"unsigned submission with the setting, forged trace first", "Delivered-To: alice@example.test\r\nAuthentication-Results: mail.example.test; none\r\n" + forgedTrace + ceoMessage, Authentication{}},
		{"local send", layoutLocalSend, Authentication{}},
		{"local send, forged fields", layoutLocalSendForged, Authentication{}},
		{"local send, forged fields, default settings", strings.Replace(layoutLocalSendForged, "Authentication-Results: localhost; none\r\n", "", 1), Authentication{}},
		{"sent copy written through JMAP", layoutLocalSendForged[strings.Index(layoutLocalSendForged, "From:"):], Authentication{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AuthenticationOf(fieldsOf(t, tc.layout), layoutHost, false); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestAuthenticationOfReadsOnlyTheTopMostField(t *testing.T) {
	// Stalwart's JMAP send writes "localhost" as its authserv-id. A field
	// below it that names our host is not ours, even in the trace block.
	layout := "Received: from localhost (unknown [127.0.0.1])\r\n" +
		"Authentication-Results: localhost; none\r\n" +
		forgedPass + ceoMessage
	if got := AuthenticationOf(fieldsOf(t, layout), layoutHost, false); got != (Authentication{}) {
		t.Fatalf("%+v", got)
	}
}

func TestAuthenticationOfNeedsAReceivedFieldInTheTraceBlock(t *testing.T) {
	layout := "Delivered-To: alice@example.test\r\n" + forgedPass + ceoMessage
	if got := AuthenticationOf(fieldsOf(t, layout), layoutHost, false); got != (Authentication{}) {
		t.Fatalf("%+v", got)
	}
	// Some servers write Authentication-Results above their Received field.
	above := "Authentication-Results: mail.example.test; spf=pass smtp.mailfrom=outside.example.test; dkim=pass header.d=outside.example.test; dmarc=pass\r\n" +
		"Received: from relay.outside.example.test by mail.example.test with ESMTPS id 1;\r\n\tTue, 24 Nov 2026 12:00:00 +0000\r\n" +
		ceoMessage
	if got := AuthenticationOf(fieldsOf(t, above), layoutHost, false); got != (Authentication{SPF: "pass", DKIM: "pass", DMARC: "pass"}) {
		t.Fatalf("%+v", got)
	}
}

func TestAuthenticationOfEndsTheTraceBlockAtTheFirstAuthorField(t *testing.T) {
	for _, name := range []string{"From", "sender", "Reply-To", "To", "Cc", "Bcc", "Message-ID", "In-Reply-To", "References", "Subject", "Comments", "Keywords", "Date", "MIME-Version", "Content-Type", "DKIM-Signature"} {
		layout := "Received: from relay.outside.example.test by mail.example.test;\r\n\tTue, 24 Nov 2026 12:00:00 +0000\r\n" +
			name + ": x\r\n" + forgedPass
		if got := AuthenticationOf(fieldsOf(t, layout), layoutHost, false); got != (Authentication{}) {
			t.Errorf("%s above the field: %+v", name, got)
		}
	}
}
