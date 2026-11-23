// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package mailbackend

import (
	"errors"
	"testing"
)

func TestSlicePagesByID(t *testing.T) {
	items := []Message{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	id := func(m Message) string { return m.ID }
	first, next, err := Slice(items, id, Page{Limit: 2})
	if err != nil || next != "b" || len(first) != 2 || first[0].ID != "a" {
		t.Fatalf("first %+v next %s err %v", first, next, err)
	}
	second, next, err := Slice(items, id, Page{After: next, Limit: 2})
	if err != nil || next != "" || len(second) != 1 || second[0].ID != "c" {
		t.Fatalf("second %+v next %s err %v", second, next, err)
	}
	if _, _, err := Slice(items, id, Page{After: "missing", Limit: 1}); !errors.Is(err, ErrPage) {
		t.Fatalf("missing cursor %v", err)
	}
	if _, _, err := Slice(items, id, Page{Limit: MaxLimit + 1}); err == nil {
		t.Fatal("oversized limit was accepted")
	}
}
