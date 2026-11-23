// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

type quotaMail struct {
	fakeMail
	lastSpec mailbackend.InboxSpec
	quotas   map[string]mailbackend.StorageQuota
}

func (q *quotaMail) CreateInbox(ctx context.Context, spec mailbackend.InboxSpec) (string, error) {
	q.lastSpec = spec
	return q.fakeMail.CreateInbox(ctx, spec)
}

func (q *quotaMail) GetStorageQuota(_ context.Context, id string) (mailbackend.StorageQuota, error) {
	if q.quotas == nil {
		return mailbackend.StorageQuota{}, nil
	}
	return q.quotas[id], nil
}

func (q *quotaMail) SetStorageQuota(_ context.Context, id string, limitBytes int64) error {
	if q.quotas == nil {
		q.quotas = map[string]mailbackend.StorageQuota{}
	}
	cur := q.quotas[id]
	cur.LimitBytes = limitBytes
	q.quotas[id] = cur
	return nil
}

func TestStorageQuotaDefaultsAndGrants(t *testing.T) {
	ctx := context.Background()
	mail := &quotaMail{quotas: map[string]mailbackend.StorageQuota{
		"inbox-a": {LimitBytes: DefaultStorageQuotaBytes, UsedBytes: 1234},
	}}
	svc := New(mail)
	svc.Allow("reader", "inbox-a", PermRead)
	svc.Allow("manager", "inbox-a", PermManage)

	if _, err := svc.CreateInbox(ctx, Caller{Operator: true}, mailbackend.InboxSpec{Local: "a"}, ""); err != nil {
		t.Fatal(err)
	}
	if mail.lastSpec.QuotaBytes != DefaultStorageQuotaBytes {
		t.Fatalf("default quota = %d, want %d", mail.lastSpec.QuotaBytes, DefaultStorageQuotaBytes)
	}
	if _, err := svc.CreateInbox(ctx, Caller{Operator: true}, mailbackend.InboxSpec{Local: "b", QuotaBytes: 4096}, ""); err != nil {
		t.Fatal(err)
	}
	if mail.lastSpec.QuotaBytes != 4096 {
		t.Fatalf("explicit quota = %d, want 4096", mail.lastSpec.QuotaBytes)
	}
	if _, err := svc.CreateInbox(ctx, Caller{Operator: true}, mailbackend.InboxSpec{Local: "c", QuotaBytes: -1}, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative quota err = %v", err)
	}

	// Strangers cannot read or set quota.
	if _, err := svc.GetStorageQuota(ctx, Caller{Agent: "stranger"}, "inbox-a"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stranger get err = %v", err)
	}
	if err := svc.SetStorageQuota(ctx, Caller{Agent: "stranger"}, "inbox-a", 1024); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stranger set err = %v", err)
	}

	// Read grant can inspect quota but cannot change it.
	got, err := svc.GetStorageQuota(ctx, Caller{Agent: "reader"}, "inbox-a")
	if err != nil || got.LimitBytes != DefaultStorageQuotaBytes || got.UsedBytes != 1234 {
		t.Fatalf("reader get = %+v err %v", got, err)
	}
	if err := svc.SetStorageQuota(ctx, Caller{Agent: "reader"}, "inbox-a", 1024); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reader set err = %v", err)
	}

	// Manage grant and operator can change quota; negative is rejected.
	if err := svc.SetStorageQuota(ctx, Caller{Agent: "manager"}, "inbox-a", -5); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative set err = %v", err)
	}
	if err := svc.SetStorageQuota(ctx, Caller{Agent: "manager"}, "inbox-a", 2048); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.GetStorageQuota(ctx, Caller{Operator: true}, "inbox-a"); got.LimitBytes != 2048 {
		t.Fatalf("after manage set = %+v", got)
	}
	// Manage on inbox-a cannot touch inbox-b.
	if err := svc.SetStorageQuota(ctx, Caller{Agent: "manager"}, "inbox-b", 2048); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-inbox set err = %v", err)
	}
	// Backends without QuotaBackend return ErrUnsupported.
	plain := New(&fakeMail{})
	if _, err := plain.GetStorageQuota(ctx, Caller{Operator: true}, "inbox-a"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported get err = %v", err)
	}
	if err := plain.SetStorageQuota(ctx, Caller{Operator: true}, "inbox-a", 1024); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported set err = %v", err)
	}
}
