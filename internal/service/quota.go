// Copyright 2026 Impakta-io
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Impakta-io/inbai/internal/mailbackend"
)

// ErrUnsupported is returned when the configured mail backend does not support the operation.
var ErrUnsupported = errors.New("not supported by mail backend")

// SetDefaultStorageQuota sets the storage cap applied to new inboxes when the caller does not
// specify one. Zero disables the default storage cap.
func (s *Service) SetDefaultStorageQuota(bytes int64) {
	if bytes < 0 {
		bytes = 0
	}
	s.storageQuota = bytes
}

// GetStorageQuota reads the disk storage cap and usage for one inbox.
// An operator, a read grant, or a manage grant on that inbox may read it.
func (s *Service) GetStorageQuota(ctx context.Context, caller Caller, inbox string) (mailbackend.StorageQuota, error) {
	if inbox == "" || strings.ContainsAny(inbox, " \t") {
		return mailbackend.StorageQuota{}, fmt.Errorf("%w: quota needs an inbox", ErrInvalid)
	}
	if err := s.permitAllowlistRead(ctx, caller, inbox); err != nil {
		return mailbackend.StorageQuota{}, err
	}
	qb, ok := s.mail.(mailbackend.QuotaBackend)
	if !ok {
		return mailbackend.StorageQuota{}, ErrUnsupported
	}
	return qb.GetStorageQuota(ctx, inbox)
}

// SetStorageQuota sets or clears the disk storage cap for one inbox.
// limitBytes zero clears the cap. An operator or an agent with manage on that inbox may set it.
func (s *Service) SetStorageQuota(ctx context.Context, caller Caller, inbox string, limitBytes int64) error {
	if inbox == "" || strings.ContainsAny(inbox, " \t") {
		return fmt.Errorf("%w: quota needs an inbox", ErrInvalid)
	}
	if limitBytes < 0 {
		return fmt.Errorf("%w: limitBytes must be zero or greater", ErrInvalid)
	}
	if err := s.permitManage(ctx, caller, inbox); err != nil {
		return err
	}
	qb, ok := s.mail.(mailbackend.QuotaBackend)
	if !ok {
		return ErrUnsupported
	}
	if err := qb.SetStorageQuota(ctx, inbox, limitBytes); err != nil {
		return err
	}
	return s.record(ctx, caller, "quota.set", inbox)
}
