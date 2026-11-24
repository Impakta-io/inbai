// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const snapshotVersion = 1

// CursorRow is the JMAP state stored for one inbox.
type CursorRow struct {
	Inbox   string `json:"inbox"`
	Address string `json:"address"`
	State   string `json:"state"`
}

// DeliveryRow is one webhook delivery, including whether it finished.
// Reason is why a failed delivery was dropped without being sent. An older file has none.
type DeliveryRow struct {
	ID             int64     `json:"id"`
	SubscriptionID string    `json:"subscriptionId"`
	EventID        int64     `json:"eventId"`
	Attempt        int       `json:"attempt"`
	Next           time.Time `json:"next"`
	Done           bool      `json:"done"`
	Failed         bool      `json:"failed"`
	Reason         string    `json:"reason,omitempty"`
}

// legacyOperatorAgent is the agent name the server stored for an operator's subscription
// before subscriptions recorded who created them.
const legacyOperatorAgent = "operator"

// UnmarshalJSON reads a subscription from a backup. A file written before subscriptions
// recorded who created them has no operator field. Such a row counts as an operator's
// when its agent is legacyOperatorAgent, the same rule the database migration applies.
func (s *Subscription) UnmarshalJSON(raw []byte) error {
	type plain Subscription
	var row struct {
		plain
		Operator *bool `json:"operator"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&row); err != nil {
		return err
	}
	*s = Subscription(row.plain)
	if row.Operator != nil {
		s.Operator = *row.Operator
	} else {
		s.Operator = s.Agent == legacyOperatorAgent
	}
	return nil
}

// GrantRow is one inbox permission.
type GrantRow struct {
	Agent      string `json:"agent"`
	Inbox      string `json:"inbox"`
	Permission string `json:"permission"`
}

// QuotaRow is one daily send counter.
type QuotaRow struct {
	Bucket string `json:"bucket"`
	Used   int    `json:"used"`
}

// IdempotencyRow is one completed request key.
type IdempotencyRow struct {
	Kind    string `json:"kind"`
	Key     string `json:"key"`
	Payload string `json:"payload"`
}

// JobSnap is one bulk-create job. Inboxes is the JSON array stored with the job.
// Agent is the agent that started it. A file written before jobs recorded their agent omits it,
// and such a job restores as one only an operator can read.
type JobSnap struct {
	ID      string          `json:"id"`
	Status  string          `json:"status"`
	Total   int             `json:"total"`
	Created int             `json:"created"`
	Error   string          `json:"error,omitempty"`
	Inboxes json.RawMessage `json:"inboxes"`
	Agent   string          `json:"agent,omitempty"`
}

// RetentionRow is the number of days one scope keeps mail. Zero disables deletion.
type RetentionRow struct {
	Scope string `json:"scope"`
	Days  int    `json:"days"`
}

// AuditRow is one operator action. It does not contain secrets or message bodies.
type AuditRow struct {
	ID     int64     `json:"id"`
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Target string    `json:"target"`
}

// ClientRow is one hashed client credential. The plaintext secret is not stored.
type ClientRow struct {
	ID         string `json:"id"`
	Agent      string `json:"agent"`
	SecretHash []byte `json:"secretHash"`
	Operator   bool   `json:"operator"`
}

// Snapshot is the inbAI database. Message bodies are not in it.
// A nil slice was written by an older file that did not include that table.
// An empty slice means the table was empty.
type Snapshot struct {
	Version       int              `json:"version"`
	Cursors       []CursorRow      `json:"cursors"`
	Events        []Record         `json:"events"`
	Subscriptions []Subscription   `json:"subscriptions"`
	Deliveries    []DeliveryRow    `json:"deliveries"`
	Suppressions  []Suppression    `json:"suppressions"`
	Grants        []GrantRow       `json:"grants"`
	Quotas        []QuotaRow       `json:"quotas"`
	Idempotency   []IdempotencyRow `json:"idempotency"`
	Jobs          []JobSnap        `json:"jobs"`
	Clients       []ClientRow      `json:"clients"`
	Suspensions   []string         `json:"suspensions"`
	Audits        []AuditRow       `json:"audits"`
	Retention     []RetentionRow   `json:"retention"`
	Allowlists    []AllowlistRow   `json:"allowlists"`
	SigningKey    []byte           `json:"signingKey"`
	Queue         []QueueJob       `json:"queue"`
}

// Export copies the log. Postgres reads it in one transaction.
func (m *Memory) Export(context.Context) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.init()
	snap := emptySnapshot()
	for inbox, cur := range m.cursors {
		snap.Cursors = append(snap.Cursors, CursorRow{Inbox: inbox, Address: cur.address, State: cur.state})
	}
	sort.Slice(snap.Cursors, func(i, j int) bool { return snap.Cursors[i].Inbox < snap.Cursors[j].Inbox })
	snap.Events = append(snap.Events, m.events...)
	for _, sub := range m.subs {
		snap.Subscriptions = append(snap.Subscriptions, sub)
	}
	sort.Slice(snap.Subscriptions, func(i, j int) bool { return snap.Subscriptions[i].ID < snap.Subscriptions[j].ID })
	for _, item := range m.deliveries {
		snap.Deliveries = append(snap.Deliveries, DeliveryRow{
			ID: item.id, SubscriptionID: item.subID, EventID: item.eventID,
			Attempt: item.attempt, Next: item.next, Done: item.done, Failed: item.failed, Reason: item.reason,
		})
	}
	for address, reason := range m.blocked {
		snap.Suppressions = append(snap.Suppressions, Suppression{Address: address, Reason: reason})
	}
	sort.Slice(snap.Suppressions, func(i, j int) bool { return snap.Suppressions[i].Address < snap.Suppressions[j].Address })
	return snap, nil
}

// Import replaces the log with the snapshot.
func (m *Memory) Import(_ context.Context, snap Snapshot) error {
	if err := snap.check(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cursors = map[string]memCursor{}
	m.byAddress = map[string]string{}
	m.subs = map[string]Subscription{}
	m.blocked = map[string]string{}
	m.events = nil
	m.deliveries = nil
	m.nextEvent = 0
	m.nextDel = 0
	for _, cur := range snap.Cursors {
		address := strings.ToLower(strings.TrimSpace(cur.Address))
		m.cursors[cur.Inbox] = memCursor{address: address, state: cur.State}
		if address != "" {
			m.byAddress[address] = cur.Inbox
		}
	}
	m.events = append([]Record(nil), snap.Events...)
	for _, event := range m.events {
		if event.ID > m.nextEvent {
			m.nextEvent = event.ID
		}
	}
	for _, sub := range snap.Subscriptions {
		m.subs[sub.ID] = sub
	}
	for _, item := range snap.Deliveries {
		m.deliveries = append(m.deliveries, memDelivery{
			id: item.ID, subID: item.SubscriptionID, eventID: item.EventID,
			attempt: item.Attempt, next: item.Next, done: item.Done, failed: item.Failed, reason: item.Reason,
		})
		if item.ID > m.nextDel {
			m.nextDel = item.ID
		}
	}
	for _, item := range snap.Suppressions {
		m.blocked[strings.ToLower(strings.TrimSpace(item.Address))] = item.Reason
	}
	return nil
}

// Encode writes a snapshot. The caller treats the bytes as a secret because webhook secrets are included.
func Encode(snap Snapshot) ([]byte, error) {
	if err := snap.check(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(snap); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Decode reads a snapshot.
func Decode(raw []byte) (Snapshot, error) {
	var snap Snapshot
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&snap); err != nil {
		return Snapshot{}, err
	}
	if err := snap.check(); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

func (s Snapshot) check() error {
	if s.Version != snapshotVersion {
		return fmt.Errorf("snapshot version %d is not supported", s.Version)
	}
	events := map[int64]struct{}{}
	for _, event := range s.Events {
		if event.ID <= 0 || event.Inbox == "" || event.MessageID == "" || (event.Kind != "created" && event.Kind != "destroyed") {
			return fmt.Errorf("event %d is incomplete", event.ID)
		}
		if _, dup := events[event.ID]; dup {
			return fmt.Errorf("event %d is duplicated", event.ID)
		}
		events[event.ID] = struct{}{}
	}
	subs := map[string]struct{}{}
	for _, sub := range s.Subscriptions {
		if sub.ID == "" || sub.Inbox == "" || sub.URL == "" || sub.Secret == "" {
			return fmt.Errorf("subscription %s is incomplete", sub.ID)
		}
		if _, dup := subs[sub.ID]; dup {
			return fmt.Errorf("subscription %s is duplicated", sub.ID)
		}
		subs[sub.ID] = struct{}{}
	}
	seen := map[int64]struct{}{}
	for _, item := range s.Deliveries {
		if item.ID <= 0 {
			return fmt.Errorf("delivery %d is incomplete", item.ID)
		}
		if _, dup := seen[item.ID]; dup {
			return fmt.Errorf("delivery %d is duplicated", item.ID)
		}
		seen[item.ID] = struct{}{}
		if _, ok := events[item.EventID]; !ok {
			return fmt.Errorf("delivery %d names missing event %d", item.ID, item.EventID)
		}
		if _, ok := subs[item.SubscriptionID]; !ok {
			return fmt.Errorf("delivery %d names missing subscription %s", item.ID, item.SubscriptionID)
		}
	}
	for _, grant := range s.Grants {
		if grant.Agent == "" || grant.Inbox == "" || grant.Permission == "" {
			return fmt.Errorf("grant for %s is incomplete", grant.Agent)
		}
	}
	for _, quota := range s.Quotas {
		if quota.Bucket == "" || quota.Used < 0 {
			return fmt.Errorf("quota %q is incomplete", quota.Bucket)
		}
	}
	for _, item := range s.Idempotency {
		if item.Kind == "" || item.Key == "" {
			return fmt.Errorf("idempotency key is incomplete")
		}
	}
	for _, job := range s.Jobs {
		if job.ID == "" || job.Status == "" {
			return fmt.Errorf("job %s is incomplete", job.ID)
		}
	}
	for _, client := range s.Clients {
		if client.ID == "" || client.Agent == "" || len(client.SecretHash) == 0 {
			return fmt.Errorf("client %s is incomplete", client.ID)
		}
	}
	for _, agent := range s.Suspensions {
		if agent == "" {
			return fmt.Errorf("suspension is incomplete")
		}
	}
	seenAudit := map[int64]struct{}{}
	for _, row := range s.Audits {
		if row.ID <= 0 || row.Actor == "" || row.Action == "" || row.Target == "" || row.At.IsZero() {
			return fmt.Errorf("audit %d is incomplete", row.ID)
		}
		if _, dup := seenAudit[row.ID]; dup {
			return fmt.Errorf("audit %d is duplicated", row.ID)
		}
		seenAudit[row.ID] = struct{}{}
	}
	for _, row := range s.Retention {
		if row.Scope == "" || row.Days < 0 {
			return fmt.Errorf("retention %q is incomplete", row.Scope)
		}
	}
	for _, row := range s.Allowlists {
		if row.Inbox == "" || len(row.Senders) == 0 {
			return fmt.Errorf("allowlist %q is incomplete", row.Inbox)
		}
	}
	seenQueue := map[int64]struct{}{}
	for _, row := range s.Queue {
		if row.ID <= 0 || row.Kind == "" {
			return fmt.Errorf("queue job %d is incomplete", row.ID)
		}
		if _, dup := seenQueue[row.ID]; dup {
			return fmt.Errorf("queue job %d is duplicated", row.ID)
		}
		seenQueue[row.ID] = struct{}{}
	}
	return nil
}

func emptySnapshot() Snapshot {
	return Snapshot{
		Version:       snapshotVersion,
		Cursors:       []CursorRow{},
		Events:        []Record{},
		Subscriptions: []Subscription{},
		Deliveries:    []DeliveryRow{},
		Suppressions:  []Suppression{},
		Grants:        []GrantRow{},
		Quotas:        []QuotaRow{},
		Idempotency:   []IdempotencyRow{},
		Jobs:          []JobSnap{},
		Clients:       []ClientRow{},
		Suspensions:   []string{},
		Audits:        []AuditRow{},
		Retention:     []RetentionRow{},
		Allowlists:    []AllowlistRow{},
		SigningKey:    []byte{},
		Queue:         []QueueJob{},
	}
}
