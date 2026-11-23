// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

// ErrNotFound is returned when a job does not exist.
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
	Error   string  `json:"error,omitempty"`
}

// GenerateRequest asks for many addresses in one domain.
// Wait runs the work before returning. Otherwise a count above the sync limit becomes a job.
type GenerateRequest struct {
	Prefix   string
	Domain   string
	DomainID string
	RoleID   string
	Count    int
	Wait     bool
	Progress func(done, total int)
}

// GenerateResult is either the finished addresses or a job id.
type GenerateResult struct {
	Inboxes []Inbox
	JobID   string
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

// Generate creates count addresses. Only an operator may do that.
// Local parts are prefix plus a zero-padded index, starting at 00000.
func (s *Service) Generate(ctx context.Context, caller Caller, req GenerateRequest, idemKey string) (GenerateResult, error) {
	if !caller.Operator {
		return GenerateResult{}, ErrForbidden
	}
	prefix := strings.ToLower(strings.TrimSpace(req.Prefix))
	if prefix == "" || strings.ContainsAny(prefix, " @/\t") {
		return GenerateResult{}, fmt.Errorf("%w: prefix is not a local part", ErrInvalid)
	}
	if req.Count < 1 || req.Count > maxGenerate {
		return GenerateResult{}, fmt.Errorf("%w: count must be from 1 to %d", ErrInvalid, maxGenerate)
	}
	if req.Domain == "" || req.DomainID == "" || req.RoleID == "" {
		return GenerateResult{}, fmt.Errorf("%w: generate needs a domain and a role", ErrInvalid)
	}
	key := idempotencyKey(caller, prefix+"\n"+fmt.Sprint(req.Count), idemKey)
	if key != "" {
		saved, ok, err := s.savedGenerate(ctx, key)
		if err != nil {
			return GenerateResult{}, err
		}
		if ok {
			return saved, nil
		}
	}
	specs := make([]mailbackend.InboxSpec, req.Count)
	for i := range specs {
		specs[i] = mailbackend.InboxSpec{
			DomainID: req.DomainID,
			RoleID:   req.RoleID,
			Local:    fmt.Sprintf("%s%05d", prefix, i),
		}
	}
	if req.Wait || req.Count <= s.SyncLimit() {
		made, err := s.createAll(ctx, specs, req.Domain, req.Progress)
		if err != nil {
			return GenerateResult{}, err
		}
		result := GenerateResult{Inboxes: made}
		if key != "" {
			if err := s.rememberGenerate(ctx, key, result); err != nil {
				return GenerateResult{}, err
			}
		}
		return result, nil
	}
	id, err := newID()
	if err != nil {
		return GenerateResult{}, err
	}
	job := &Job{ID: id, Status: "running", Total: req.Count}
	if err := s.putJob(ctx, job); err != nil {
		return GenerateResult{}, err
	}
	result := GenerateResult{JobID: id}
	if key != "" {
		if err := s.rememberGenerate(ctx, key, result); err != nil {
			return GenerateResult{}, err
		}
	}
	go s.finishJob(job, specs, req.Domain)
	return result, nil
}

// Job returns a copy of a bulk-create job.
func (s *Service) Job(id string) (Job, error) {
	if s.db != nil {
		row, ok, err := s.db.Job(context.Background(), id)
		if err != nil {
			return Job{}, err
		}
		if !ok {
			return Job{}, ErrNotFound
		}
		job := Job{ID: row.ID, Status: row.Status, Total: row.Total, Created: row.Created, Error: row.Error}
		if len(row.Inboxes) > 0 && string(row.Inboxes) != "[]" {
			if err := json.Unmarshal(row.Inboxes, &job.Inboxes); err != nil {
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
		return s.db.SaveJob(ctx, store.JobRow{ID: job.ID, Status: job.Status, Total: job.Total, Inboxes: []byte("[]")})
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
	made, err := s.createAll(context.Background(), specs, domain, nil)
	if s.db != nil {
		row := store.JobRow{ID: job.ID, Total: job.Total, Inboxes: []byte("[]")}
		if err != nil {
			row.Status = "failed"
			row.Error = err.Error()
		} else {
			raw, encErr := json.Marshal(made)
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
	job.Created = len(made)
	job.Status = "done"
}

func (s *Service) createAll(ctx context.Context, specs []mailbackend.InboxSpec, domain string, progress func(done, total int)) ([]Inbox, error) {
	made := make([]Inbox, 0, len(specs))
	for start := 0; start < len(specs); start += generateBatch {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := start + generateBatch
		if end > len(specs) {
			end = len(specs)
		}
		ids, err := s.mail.CreateInboxes(ctx, specs[start:end])
		if err != nil {
			return nil, err
		}
		if len(ids) != end-start {
			return nil, fmt.Errorf("created %d addresses, want %d", len(ids), end-start)
		}
		for i, id := range ids {
			address := specs[start+i].Local + "@" + domain
			made = append(made, Inbox{ID: id, Address: address})
			if err := s.BindInbox(ctx, id, address); err != nil {
				return nil, err
			}
		}
		if progress != nil {
			progress(len(made), len(specs))
		}
	}
	return made, nil
}

func (s *Service) savedGenerate(ctx context.Context, key string) (GenerateResult, bool, error) {
	if s.db != nil {
		raw, ok, err := s.db.Idempotency(ctx, "generate", key)
		if err != nil || !ok {
			return GenerateResult{}, ok, err
		}
		var result GenerateResult
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return GenerateResult{}, false, err
		}
		return result, true, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result, ok := s.generated[key]
	return result, ok, nil
}

func (s *Service) rememberGenerate(ctx context.Context, key string, result GenerateResult) error {
	if s.db != nil {
		raw, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return s.db.SaveIdempotency(ctx, "generate", key, string(raw))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generated == nil {
		s.generated = map[string]GenerateResult{}
	}
	s.generated[key] = result
	return nil
}

func copyJob(job *Job) Job {
	out := *job
	if job.Inboxes != nil {
		out.Inboxes = append([]Inbox(nil), job.Inboxes...)
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
