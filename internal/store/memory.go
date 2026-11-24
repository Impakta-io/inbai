// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// Memory is an in-process log for tests. Production uses Postgres, which survives a restart.
type Memory struct {
	mu         sync.Mutex
	nextEvent  int64
	nextDel    int64
	cursors    map[string]memCursor
	byAddress  map[string]string
	events     []Record
	subs       map[string]Subscription
	deliveries []memDelivery
	blocked    map[string]string
}

type memCursor struct {
	address string
	state   string
}

type memDelivery struct {
	id      int64
	subID   string
	eventID int64
	attempt int
	next    time.Time
	done    bool
	failed  bool
}

func (m *Memory) init() {
	if m.cursors == nil {
		m.cursors = map[string]memCursor{}
		m.byAddress = map[string]string{}
		m.subs = map[string]Subscription{}
		m.blocked = map[string]string{}
	}
}

func (m *Memory) Ping(context.Context) error { return nil }

func (m *Memory) Remember(_ context.Context, inbox, address string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	address = strings.ToLower(strings.TrimSpace(address))
	cur := m.cursors[inbox]
	cur.address = address
	m.cursors[inbox] = cur
	if address != "" {
		m.byAddress[address] = inbox
	}
	return nil
}

func (m *Memory) State(_ context.Context, inbox string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	cur, ok := m.cursors[inbox]
	if !ok || cur.state == "" {
		return "", false, nil
	}
	return cur.state, true, nil
}

func (m *Memory) InboxByAddress(_ context.Context, address string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	inbox, ok := m.byAddress[strings.ToLower(strings.TrimSpace(address))]
	return inbox, ok, nil
}

func (m *Memory) Inboxes(context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	out := make([]string, 0, len(m.cursors))
	for inbox := range m.cursors {
		out = append(out, inbox)
	}
	sort.Strings(out)
	return out, nil
}

// Forget drops the cursor and address binding for an inbox. Events already logged stay.
func (m *Memory) Forget(_ context.Context, inbox string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	delete(m.cursors, inbox)
	for address, owner := range m.byAddress {
		if owner == inbox {
			delete(m.byAddress, address)
		}
	}
	return nil
}

func (m *Memory) Put(_ context.Context, inbox, address, state string, records []Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	cur := m.cursors[inbox]
	if address = strings.ToLower(strings.TrimSpace(address)); address != "" {
		cur.address = address
		m.byAddress[address] = inbox
	}
	if state != "" {
		cur.state = state
	}
	m.cursors[inbox] = cur
	for _, record := range records {
		m.nextEvent++
		record.ID = m.nextEvent
		record.Inbox = inbox
		m.events = append(m.events, record)
		for _, sub := range m.subs {
			if sub.Inbox != inbox {
				continue
			}
			m.nextDel++
			m.deliveries = append(m.deliveries, memDelivery{id: m.nextDel, subID: sub.ID, eventID: record.ID, next: time.Now()})
		}
	}
	return nil
}

func (m *Memory) Since(_ context.Context, inbox string, after int64, limit int) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	if limit <= 0 {
		limit = 50
	}
	var out []Record
	for _, record := range m.events {
		if record.Inbox == inbox && record.ID > after {
			out = append(out, record)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

// Head returns the id of the newest event for an inbox, or 0 when it has none.
func (m *Memory) Head(_ context.Context, inbox string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.events) - 1; i >= 0; i-- {
		if m.events[i].Inbox == inbox {
			return m.events[i].ID, nil
		}
	}
	return 0, nil
}

func (m *Memory) Subscribe(_ context.Context, sub Subscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	m.subs[sub.ID] = sub
	return nil
}

func (m *Memory) Claim(_ context.Context, now time.Time) (Delivery, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	for i := range m.deliveries {
		item := &m.deliveries[i]
		if item.done || item.failed || item.next.After(now) {
			continue
		}
		item.attempt++
		item.next = now.Add(30 * time.Second)
		sub := m.subs[item.subID]
		var event Record
		for _, record := range m.events {
			if record.ID == item.eventID {
				event = record
				break
			}
		}
		return Delivery{ID: item.id, Attempt: item.attempt, URL: sub.URL, Secret: sub.Secret, Event: event}, true, nil
	}
	return Delivery{}, false, nil
}

func (m *Memory) Ack(_ context.Context, id int64, _ time.Time) error {
	return m.finish(id, true)
}

func (m *Memory) Retry(_ context.Context, id int64, next time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.deliveries {
		if m.deliveries[i].id == id {
			m.deliveries[i].next = next
			return nil
		}
	}
	return nil
}

func (m *Memory) Fail(_ context.Context, id int64, _ time.Time) error {
	return m.finish(id, false)
}

func (m *Memory) finish(id int64, ok bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.deliveries {
		if m.deliveries[i].id == id {
			if ok {
				m.deliveries[i].done = true
			} else {
				m.deliveries[i].failed = true
			}
			return nil
		}
	}
	return nil
}

func (m *Memory) Suppress(_ context.Context, address, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	address = strings.ToLower(strings.TrimSpace(address))
	if address == "" || !strings.Contains(address, "@") {
		return nil
	}
	if _, exists := m.blocked[address]; exists {
		return nil
	}
	m.blocked[address] = reason
	return nil
}

func (m *Memory) Suppressed(_ context.Context, address string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	reason, ok := m.blocked[strings.ToLower(strings.TrimSpace(address))]
	return reason, ok, nil
}

func (m *Memory) Suppressions(context.Context) ([]Suppression, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	out := make([]Suppression, 0, len(m.blocked))
	for address, reason := range m.blocked {
		out = append(out, Suppression{Address: address, Reason: reason})
	}
	return out, nil
}
