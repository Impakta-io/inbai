// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Impakta-io/inbai/internal/mailbackend"
	"github.com/Impakta-io/inbai/internal/store"
)

const (
	defaultSyncLimit = 50
	maxGenerate      = 10000
	generateBatch    = 200
)

// ErrInvalid is returned when a request cannot be carried out.
var ErrInvalid = errors.New("invalid request")

// ErrNotFound is returned when a job does not exist or the caller may not read it.
var ErrNotFound = errors.New("not found")

// Inbox is one address created by Generate.
type Inbox struct {
	ID      string `json:"id"`
	Address string `json:"address"`
}

// Job is a bulk create that did not finish inside the request.
type Job struct {
	ID      string  `json:"id"`
	Status  string  `json:"status"`
	Total   int     `json:"total"`
	Created int     `json:"created"`
	Inboxes []Inbox `json:"inboxes,omitempty"`
	// Existing lists the addresses that were already taken and not created.
	Existing []string `json:"existing,omitempty"`
	Error    string   `json:"error,omitempty"`

	// agent started the job. It is empty when an operator did.
	agent string
}

// GenerateRequest asks for many addresses in one domain.
// Wait runs the work before returning. Otherwise a count above the sync limit becomes a job.
type GenerateRequest struct {
	Prefix     string
	Domain     string
	Count      int
	QuotaBytes int64
	Wait       bool
	Progress   func(done, total int)
}

// GenerateResult is either the finished addresses or a job id.
// Existing lists the addresses that were already taken and not created.
type GenerateResult struct {
	Inboxes  []Inbox
	Existing []string
	JobID    string
}

// BulkPayload is the work stored in job_queue for an asynchronous Generate call.
type BulkPayload struct {
	JobID      string `json:"jobId"`
	Prefix     string `json:"prefix"`
	Domain     string `json:"domain"`
	Count      int    `json:"count"`
	QuotaBytes int64  `json:"quotaBytes,omitempty"`
}

func (s *Service) SyncLimit() int {
	if s.syncLimit > 0 {
		return s.syncLimit
	}
	return defaultSyncLimit
}

// SetSyncLimit sets how many addresses Generate will create inside the request when Wait is false.
func (s *Service) SetSyncLimit(n int) {
	s.syncLimit = n
}

// Generate creates count addresses. An operator may do that.
// An agent may do that when it holds inbox:provision on *.
// Local parts are prefix plus a zero-padded index, starting at 00000.
// idemKey is the Idempotency-Key. A repeat of a completed key returns the same inboxes,
// or the same job id when the first request was queued. See reserve for a repeat that is
// still running or names a different request.
func (s *Service) Generate(ctx context.Context, caller Caller, req GenerateRequest, idemKey string) (GenerateResult, error) {
	if err := s.permitProvision(ctx, caller); err != nil {
		return GenerateResult{}, err
	}
	prefix := strings.ToLower(strings.TrimSpace(req.Prefix))
	if prefix == "" || strings.ContainsAny(prefix, " @/\t") {
		return GenerateResult{}, fmt.Errorf("%w: prefix is not a local part", ErrInvalid)
	}
	if req.Count < 1 || req.Count > maxGenerate {
		return GenerateResult{}, fmt.Errorf("%w: count must be from 1 to %d", ErrInvalid, maxGenerate)
	}
	if req.Domain == "" {
		return GenerateResult{}, fmt.Errorf("%w: generate needs a domain", ErrInvalid)
	}
	if req.QuotaBytes < 0 {
		return GenerateResult{}, fmt.Errorf("%w: quotaBytes must be zero or greater", ErrInvalid)
	}
	// The key is bound to the caller only. Prefix and count are in the hash, so the same
	// key with another prefix or count is ErrKeyReused instead of a separate request.
	hold, saved, done, err := s.reserve(ctx, kindGenerate, idempotencyKey(caller, "", idemKey), generateHash(s.mail, prefix, req))
	if err != nil {
		return GenerateResult{}, err
	}
	if !done {
		saved, done, err = s.legacyGenerate(ctx, caller, prefix, req.Count, idemKey)
		if err != nil {
			s.release(ctx, hold)
			return GenerateResult{}, err
		}
		if done {
			// Bind the older answer to this request, so a different one is ErrKeyReused from now on.
			if err := s.complete(ctx, hold, saved); err != nil {
				return GenerateResult{}, err
			}
		}
	}
	if done {
		var result GenerateResult
		if err := json.Unmarshal([]byte(saved), &result); err != nil {
			return GenerateResult{}, err
		}
		return result, nil
	}
	result, err := s.generate(ctx, caller, req, prefix)
	if err != nil {
		s.release(ctx, hold)
		return GenerateResult{}, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return GenerateResult{}, err
	}
	if err := s.complete(ctx, hold, string(raw)); err != nil {
		return GenerateResult{}, err
	}
	return result, nil
}

// generate does the work of a Generate call that holds its key. Inside the sync limit or
// with Wait it creates the addresses. Otherwise it queues a job and returns its id, and the
// job is then the durable record of the request.
func (s *Service) generate(ctx context.Context, caller Caller, req GenerateRequest, prefix string) (GenerateResult, error) {
	quotaBytes := req.QuotaBytes
	if quotaBytes == 0 {
		quotaBytes = s.storageQuota
	}
	specs := make([]mailbackend.InboxSpec, req.Count)
	for i := range specs {
		specs[i] = mailbackend.InboxSpec{
			Local:      fmt.Sprintf("%s%05d", prefix, i),
			QuotaBytes: quotaBytes,
		}
	}
	if req.Wait || req.Count <= s.SyncLimit() {
		made, existing, err := s.createAll(ctx, specs, req.Domain, req.Progress)
		if err != nil {
			return GenerateResult{}, err
		}
		return GenerateResult{Inboxes: made, Existing: existing}, nil
	}
	id, err := newID()
	if err != nil {
		return GenerateResult{}, err
	}
	job := &Job{ID: id, Status: "running", Total: req.Count}
	if !caller.Operator {
		job.agent = caller.Agent
	}
	if err := s.putJob(ctx, job); err != nil {
		return GenerateResult{}, err
	}
	if s.db != nil {
		payload, err := json.Marshal(BulkPayload{
			JobID:      id,
			Prefix:     prefix,
			Domain:     req.Domain,
			Count:      req.Count,
			QuotaBytes: quotaBytes,
		})
		if err != nil {
			return GenerateResult{}, err
		}
		if _, _, err := s.db.Enqueue(ctx, "bulk.generate", id, payload, s.now().UTC()); err != nil {
			return GenerateResult{}, err
		}
	}
	if s.db == nil {
		go s.finishJob(job, specs, req.Domain)
	}
	return GenerateResult{JobID: id}, nil
}

// RunBulkJob executes one queued bulk-create job and updates its status row.
func (s *Service) RunBulkJob(ctx context.Context, payload BulkPayload) error {
	quotaBytes := payload.QuotaBytes
	if quotaBytes == 0 {
		quotaBytes = s.storageQuota
	}
	specs := make([]mailbackend.InboxSpec, payload.Count)
	for i := range specs {
		specs[i] = mailbackend.InboxSpec{
			Local:      fmt.Sprintf("%s%05d", payload.Prefix, i),
			QuotaBytes: quotaBytes,
		}
	}
	made, existing, err := s.createAll(ctx, specs, payload.Domain, nil)
	if s.db == nil {
		return err
	}
	row := store.JobRow{ID: payload.JobID, Total: payload.Count, Inboxes: []byte("[]")}
	if err != nil {
		row.Status = "failed"
		row.Error = err.Error()
		_ = s.db.UpdateJob(ctx, row)
		return err
	}
	raw, encErr := encodeJobInboxes(made, existing)
	if encErr != nil {
		row.Status = "failed"
		row.Error = encErr.Error()
		_ = s.db.UpdateJob(ctx, row)
		return encErr
	}
	row.Status = "done"
	row.Created = len(made)
	row.Inboxes = raw
	return s.db.UpdateJob(ctx, row)
}

// Job returns a copy of a bulk-create job.
// An operator may read any job. An agent may read a job it started, and only while it
// holds inbox:provision on *, because the job lists the inboxes that grant created.
// Any other caller gets ErrNotFound, the same as for an unknown id.
// A job with no recorded agent was started by an operator or predates the record.
func (s *Service) Job(ctx context.Context, caller Caller, id string) (Job, error) {
	if !caller.Operator {
		ok, err := s.allowed(ctx, caller, provisionScope, PermProvision)
		if err != nil {
			return Job{}, err
		}
		if !ok {
			return Job{}, ErrNotFound
		}
	}
	job, err := s.loadJob(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if !caller.Operator && (caller.Agent == "" || job.agent != caller.Agent) {
		return Job{}, ErrNotFound
	}
	return job, nil
}

func (s *Service) loadJob(ctx context.Context, id string) (Job, error) {
	if s.db != nil {
		row, ok, err := s.db.Job(ctx, id)
		if err != nil {
			return Job{}, err
		}
		if !ok {
			return Job{}, ErrNotFound
		}
		job := Job{ID: row.ID, Status: row.Status, Total: row.Total, Created: row.Created, Error: row.Error, agent: row.Agent}
		if len(row.Inboxes) > 0 && string(row.Inboxes) != "[]" {
			if err := decodeJobInboxes(row.Inboxes, &job); err != nil {
				return Job{}, err
			}
		}
		return job, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	return copyJob(job), nil
}

func (s *Service) putJob(ctx context.Context, job *Job) error {
	if s.db != nil {
		return s.db.SaveJob(ctx, store.JobRow{ID: job.ID, Status: job.Status, Total: job.Total, Inboxes: []byte("[]"), Agent: job.agent})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jobs == nil {
		s.jobs = map[string]*Job{}
	}
	s.jobs[job.ID] = job
	return nil
}

func (s *Service) finishJob(job *Job, specs []mailbackend.InboxSpec, domain string) {
	made, existing, err := s.createAll(context.Background(), specs, domain, nil)
	if s.db != nil {
		row := store.JobRow{ID: job.ID, Total: job.Total, Inboxes: []byte("[]")}
		if err != nil {
			row.Status = "failed"
			row.Error = err.Error()
		} else {
			raw, encErr := encodeJobInboxes(made, existing)
			if encErr != nil {
				row.Status = "failed"
				row.Error = encErr.Error()
			} else {
				row.Status = "done"
				row.Created = len(made)
				row.Inboxes = raw
			}
		}
		_ = s.db.UpdateJob(context.Background(), row)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		job.Status = "failed"
		job.Error = err.Error()
		return
	}
	job.Inboxes = made
	job.Existing = existing
	job.Created = len(made)
	job.Status = "done"
}

// createAll creates the addresses and returns the new inboxes and the
// addresses that were already taken. Those are skipped, not returned as
// inboxes. When every address was taken, nothing was created and it is ErrConflict.
func (s *Service) createAll(ctx context.Context, specs []mailbackend.InboxSpec, domain string, progress func(done, total int)) ([]Inbox, []string, error) {
	made := make([]Inbox, 0, len(specs))
	var existing []string
	for start := 0; start < len(specs); start += generateBatch {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		end := start + generateBatch
		if end > len(specs) {
			end = len(specs)
		}
		ids, err := s.mail.CreateInboxes(ctx, specs[start:end])
		if err != nil {
			return nil, nil, err
		}
		if len(ids) != end-start {
			return nil, nil, fmt.Errorf("created %d addresses, want %d", len(ids), end-start)
		}
		for i, id := range ids {
			address := specs[start+i].Local + "@" + domain
			if id == "" {
				existing = append(existing, address)
				continue
			}
			made = append(made, Inbox{ID: id, Address: address})
			if err := s.BindInbox(ctx, id, address); err != nil {
				return nil, nil, err
			}
		}
		if progress != nil {
			progress(len(made)+len(existing), len(specs))
		}
	}
	if len(made) == 0 && len(existing) > 0 {
		return nil, nil, fmt.Errorf("%w: all %d addresses already exist, none was created", ErrConflict, len(existing))
	}
	return made, existing, nil
}

// jobInboxes is a job row's inboxes value when some addresses already
// existed. Without any, the value is the bare inbox array, as in older rows.
type jobInboxes struct {
	Inboxes  []Inbox  `json:"inboxes"`
	Existing []string `json:"existing"`
}

func encodeJobInboxes(made []Inbox, existing []string) ([]byte, error) {
	if len(existing) == 0 {
		return json.Marshal(made)
	}
	return json.Marshal(jobInboxes{Inboxes: made, Existing: existing})
}

func decodeJobInboxes(raw []byte, job *Job) error {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '{' {
		var both jobInboxes
		if err := json.Unmarshal(trimmed, &both); err != nil {
			return err
		}
		job.Inboxes, job.Existing = both.Inboxes, both.Existing
		return nil
	}
	return json.Unmarshal(raw, &job.Inboxes)
}

// legacyGenerate is the payload an older server stored for a completed generate. Those rows
// are keyed by caller, prefix, and count, and have no request hash. Keys held in memory do
// not outlive the process, so only the database has them. The payload is a GenerateResult
// in JSON, the same as the payload complete stores now.
func (s *Service) legacyGenerate(ctx context.Context, caller Caller, prefix string, count int, idemKey string) (string, bool, error) {
	key := idempotencyKey(caller, prefix+"\n"+strconv.Itoa(count), idemKey)
	if s.db == nil || key == "" {
		return "", false, nil
	}
	return s.db.Idempotency(ctx, kindGenerate, key)
}

func copyJob(job *Job) Job {
	out := *job
	if job.Inboxes != nil {
		out.Inboxes = append([]Inbox(nil), job.Inboxes...)
	}
	if job.Existing != nil {
		out.Existing = append([]string(nil), job.Existing...)
	}
	return out
}

func newID() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}
