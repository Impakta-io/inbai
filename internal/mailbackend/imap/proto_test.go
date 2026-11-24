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

func TestInternalDateForms(t *testing.T) {
	utc := func(d, h int) time.Time { return time.Date(2026, 1, d, h, 0, 0, 0, time.UTC) }
	tests := []struct {
		name string
		text string
		want time.Time
	}{
		{"two digit day", `* 1 FETCH (UID 5 INTERNALDATE "17-Jan-2026 12:00:00 +0000" FLAGS ())`, utc(17, 12)},
		{"space padded day", `* 1 FETCH (UID 5 INTERNALDATE " 1-Jan-2026 12:00:00 +0000" FLAGS ())`, utc(1, 12)},
		{"unpadded day", `* 1 FETCH (UID 5 INTERNALDATE "1-Jan-2026 12:00:00 +0000")`, utc(1, 12)},
		{"last item", `* 1 FETCH (UID 5 INTERNALDATE "17-Jan-2026 12:00:00 +0000")`, utc(17, 12)},
		{"last item padded", `* 1 FETCH (UID 5 INTERNALDATE " 2-Jan-2026 12:00:00 +0000")`, utc(2, 12)},
		{"zone offset", `* 1 FETCH (INTERNALDATE "17-Jan-2026 14:00:00 +0200")`, utc(17, 12)},
		{"negative zone", `* 1 FETCH (INTERNALDATE " 3-Jan-2026 07:00:00 -0500")`, utc(3, 12)},
		{"lower case key", `* 1 FETCH (internaldate "17-Jan-2026 12:00:00 +0000")`, utc(17, 12)},
		{"nil", `* 1 FETCH (INTERNALDATE NIL)`, time.Time{}},
		{"garbage", `* 1 FETCH (INTERNALDATE "yesterday")`, time.Time{}},
		{"absent", `* 1 FETCH (UID 5)`, time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := internalDate(tt.text); !got.Equal(tt.want) {
				t.Fatalf("got %s want %s", got, tt.want)
			}
		})
	}
}
