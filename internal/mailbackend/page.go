// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package mailbackend

import (
	"errors"
	"fmt"
)

const (
	// DefaultLimit is the page size when the caller does not set one.
	DefaultLimit = 50
	// MaxLimit is the largest page a caller may request.
	MaxLimit = 100
)

// ErrPage means the cursor does not name an item in this list.
var ErrPage = errors.New("page cursor was not found")

// Page asks for the items that follow After. An empty After starts at the beginning.
// Limit zero means DefaultLimit.
type Page struct {
	After string
	Limit int
}

// Bound returns the page size, or an error when Limit is outside 1..MaxLimit.
func (p Page) Bound() (int, error) {
	if p.Limit == 0 {
		return DefaultLimit, nil
	}
	if p.Limit < 1 || p.Limit > MaxLimit {
		return 0, fmt.Errorf("limit must be from 1 to %d", MaxLimit)
	}
	return p.Limit, nil
}

// Slice returns the page of items after After. The next cursor is the last
// returned id when more items remain, and empty when this is the last page.
func Slice[T any](items []T, id func(T) string, page Page) ([]T, string, error) {
	limit, err := page.Bound()
	if err != nil {
		return nil, "", err
	}
	start := 0
	if page.After != "" {
		start = -1
		for i, item := range items {
			if id(item) == page.After {
				start = i + 1
				break
			}
		}
		if start < 0 {
			return nil, "", ErrPage
		}
	}
	end := start + limit
	next := ""
	if end < len(items) {
		next = id(items[end-1])
	} else {
		end = len(items)
	}
	if start > len(items) {
		start = len(items)
	}
	return items[start:end], next, nil
}
