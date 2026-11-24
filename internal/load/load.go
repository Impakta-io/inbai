// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package load

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Options selects one run. Counts are whatever the caller asks for.
type Options struct {
	BaseURL     string
	Token       string
	Prefix      string
	Inboxes     int
	Messages    int
	Concurrency int
	EventWait   time.Duration
	Client      *http.Client
}

// Report is the measurement for one run. Durations are milliseconds.
type Report struct {
	Inboxes    int     `json:"inboxes"`
	Sends      int     `json:"sends"`
	Lists      int     `json:"lists"`
	Reads      int     `json:"reads"`
	Errors     int     `json:"errors"`
	FirstError string  `json:"firstError,omitempty"`
	CreateMS   float64 `json:"createMs"`
	SendP50MS  float64 `json:"sendP50ms"`
	SendP95MS  float64 `json:"sendP95ms"`
	ListP50MS  float64 `json:"listP50ms"`
	ListP95MS  float64 `json:"listP95ms"`
	ReadP50MS  float64 `json:"readP50ms"`
	ReadP95MS  float64 `json:"readP95ms"`
	Events     string  `json:"events"`
	EventLagMS float64 `json:"eventLagMs,omitempty"`
}

type inbox struct {
	ID      string `json:"id"`
	Address string `json:"address"`
}

// Run provisions inboxes, sends, lists, and reads. A failed call is counted and the run continues.
func Run(ctx context.Context, opt Options) (Report, error) {
	if opt.Inboxes < 1 || opt.Messages < 1 || opt.Concurrency < 1 {
		return Report{}, fmt.Errorf("inboxes, messages, and concurrency must be at least 1")
	}
	if opt.BaseURL == "" || opt.Token == "" {
		return Report{}, fmt.Errorf("a base URL and an operator token are required")
	}
	if opt.Prefix == "" {
		opt.Prefix = "load"
	}
	if opt.Client == nil {
		opt.Client = http.DefaultClient
	}
	if opt.EventWait == 0 {
		opt.EventWait = 2 * time.Second
	}
	var report Report
	var mu sync.Mutex
	note := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		report.Errors++
		if report.FirstError == "" && err != nil {
			report.FirstError = err.Error()
		}
	}
	started := time.Now()
	made, err := provision(ctx, opt)
	report.CreateMS = float64(time.Since(started).Microseconds()) / 1000
	if err != nil {
		note(err)
		report.Events = "unavailable"
		return report, nil
	}
	report.Inboxes = len(made)
	var sends, lists, reads []time.Duration
	work := func(n int, fn func() (time.Duration, error), dest *[]time.Duration) {
		sem := make(chan struct{}, opt.Concurrency)
		var group sync.WaitGroup
		for i := 0; i < n; i++ {
			group.Add(1)
			sem <- struct{}{}
			go func() {
				defer group.Done()
				defer func() { <-sem }()
				elapsed, callErr := fn()
				mu.Lock()
				defer mu.Unlock()
				if callErr != nil {
					report.Errors++
					if report.FirstError == "" {
						report.FirstError = callErr.Error()
					}
					return
				}
				*dest = append(*dest, elapsed)
			}()
		}
		group.Wait()
	}
	// The closure above captures dest by pointer but the loop variable would be wrong if reused.
	// Each phase uses its own slice, so call them one at a time.
	sendN := len(made) * opt.Messages
	if len(made) > 0 {
		sendN = len(made)*opt.Messages - 1
	}
	next := 0
	var nextMu sync.Mutex
	work(sendN, func() (time.Duration, error) {
		nextMu.Lock()
		i := next
		next++
		nextMu.Unlock()
		box := made[i%len(made)]
		return opt.post(ctx, "/v1/inboxes/"+box.ID+"/messages", map[string]string{
			"from": box.Address, "to": "reader@example.com", "subject": "load", "text": "load",
		}, http.StatusAccepted)
	}, &sends)
	probe := made[0]
	elapsed, err := opt.post(ctx, "/v1/inboxes/"+probe.ID+"/messages", map[string]string{
		"from": probe.Address, "to": "reader@example.com", "subject": "load", "text": "probe",
	}, http.StatusAccepted)
	probeAt := time.Now()
	if err != nil {
		note(err)
	} else {
		sends = append(sends, elapsed)
	}
	report.Events, report.EventLagMS = opt.waitEvent(ctx, probe.ID, probeAt)
	var listI int
	var listMu sync.Mutex
	work(len(made), func() (time.Duration, error) {
		listMu.Lock()
		i := listI
		listI++
		listMu.Unlock()
		return opt.call(ctx, http.MethodGet, "/v1/inboxes/"+made[i].ID+"/messages", nil, http.StatusOK)
	}, &lists)
	messageID, err := opt.firstMessage(ctx, probe.ID)
	if err != nil {
		note(err)
	} else if messageID != "" {
		elapsed, err = opt.call(ctx, http.MethodGet, "/v1/inboxes/"+probe.ID+"/messages/"+messageID, nil, http.StatusOK)
		if err != nil {
			note(err)
		} else {
			reads = append(reads, elapsed)
		}
	}
	report.Sends = len(sends)
	report.Lists = len(lists)
	report.Reads = len(reads)
	report.SendP50MS, report.SendP95MS = percentiles(sends)
	report.ListP50MS, report.ListP95MS = percentiles(lists)
	report.ReadP50MS, report.ReadP95MS = percentiles(reads)
	return report, nil
}

func provision(ctx context.Context, opt Options) ([]inbox, error) {
	status, raw, err := opt.request(ctx, http.MethodPost, "/v1/inboxes/generate", map[string]any{
		"prefix": opt.Prefix, "count": opt.Inboxes,
	})
	if err != nil {
		return nil, err
	}
	if status == http.StatusCreated {
		var body struct {
			Inboxes []inbox `json:"inboxes"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, err
		}
		if len(body.Inboxes) != opt.Inboxes {
			return nil, fmt.Errorf("created %d inboxes, want %d", len(body.Inboxes), opt.Inboxes)
		}
		return body.Inboxes, nil
	}
	if status != http.StatusAccepted {
		return nil, fmt.Errorf("generate returned %d", status)
	}
	var accepted struct {
		JobID string `json:"jobId"`
	}
	if err := json.Unmarshal(raw, &accepted); err != nil {
		return nil, err
	}
	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("generate job %s did not finish", accepted.JobID)
		case <-ticker.C:
			status, raw, err = opt.request(ctx, http.MethodGet, "/v1/jobs/"+accepted.JobID, nil)
			if err != nil {
				return nil, err
			}
			if status != http.StatusOK {
				return nil, fmt.Errorf("job returned %d", status)
			}
			var job struct {
				Status  string  `json:"status"`
				Error   string  `json:"error"`
				Inboxes []inbox `json:"inboxes"`
			}
			if err := json.Unmarshal(raw, &job); err != nil {
				return nil, err
			}
			if job.Status == "running" {
				continue
			}
			if job.Status != "done" {
				return nil, fmt.Errorf("generate job %s", job.Error)
			}
			if len(job.Inboxes) != opt.Inboxes {
				return nil, fmt.Errorf("created %d inboxes, want %d", len(job.Inboxes), opt.Inboxes)
			}
			return job.Inboxes, nil
		}
	}
}

func (opt Options) waitEvent(ctx context.Context, inboxID string, started time.Time) (string, float64) {
	status, raw, err := opt.request(ctx, http.MethodGet, "/v1/events?inbox="+inboxID+"&after=0&timeout=0", nil)
	if err != nil || status == http.StatusServiceUnavailable {
		return "unavailable", 0
	}
	if status != http.StatusOK {
		return "unavailable", 0
	}
	if hasCreated(raw) {
		return "seen", float64(time.Since(started).Microseconds()) / 1000
	}
	deadline, cancel := context.WithTimeout(ctx, opt.EventWait)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.Done():
			return "empty", float64(time.Since(started).Microseconds()) / 1000
		case <-ticker.C:
			status, raw, err = opt.request(ctx, http.MethodGet, "/v1/events?inbox="+inboxID+"&after=0&timeout=0", nil)
			if err != nil || status != http.StatusOK {
				continue
			}
			if hasCreated(raw) {
				return "seen", float64(time.Since(started).Microseconds()) / 1000
			}
		}
	}
}

func hasCreated(raw []byte) bool {
	var body struct {
		Events []struct {
			Kind string `json:"kind"`
		} `json:"events"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return false
	}
	for _, event := range body.Events {
		if event.Kind == "created" {
			return true
		}
	}
	return false
}

func (opt Options) firstMessage(ctx context.Context, inboxID string) (string, error) {
	status, raw, err := opt.request(ctx, http.MethodGet, "/v1/inboxes/"+inboxID+"/messages", nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("list returned %d", status)
	}
	var body struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", err
	}
	if len(body.Messages) == 0 {
		return "", nil
	}
	return body.Messages[0].ID, nil
}

func (opt Options) post(ctx context.Context, path string, body any, want int) (time.Duration, error) {
	return opt.call(ctx, http.MethodPost, path, body, want)
}

func (opt Options) call(ctx context.Context, method, path string, body any, want int) (time.Duration, error) {
	started := time.Now()
	status, _, err := opt.request(ctx, method, path, body)
	elapsed := time.Since(started)
	if err != nil {
		return 0, err
	}
	if status != want {
		return 0, fmt.Errorf("%s %s returned %d", method, path, status)
	}
	return elapsed, nil
}

func (opt Options) request(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, opt.BaseURL+path, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+opt.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := opt.Client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return res.StatusCode, nil, err
	}
	return res.StatusCode, raw, nil
}

func percentiles(samples []time.Duration) (float64, float64) {
	if len(samples) == 0 {
		return 0, 0
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	at := func(p float64) float64 {
		index := int(p * float64(len(sorted)-1))
		return float64(sorted[index].Microseconds()) / 1000
	}
	return at(0.50), at(0.95)
}
