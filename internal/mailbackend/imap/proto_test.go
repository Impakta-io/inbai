// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package imap

import (
	"testing"
	"time"
)

func TestInternalDateReadsTheFetchResponse(t *testing.T) {
	got := internalDate(`* 1 FETCH (UID 5 INTERNALDATE "23-Nov-2026 12:00:00 +0000" BODY[HEADER.FIELDS (SUBJECT)] {12}`)
	want := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %s", got)
	}
	if !internalDate("* 1 FETCH (UID 5)").IsZero() {
		t.Fatal("missing date was parsed")
	}
}
