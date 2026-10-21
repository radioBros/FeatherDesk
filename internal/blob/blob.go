// SPDX-License-Identifier: AGPL-3.0-only

// Package blob stores attachments, branding assets, KB images and raw inbound
// mail. Local filesystem is the default; S3-compatible storage is optional (D-17).
package blob

import (
	"context"
	"errors"
	"io"
	"time"
)

var ErrNotFound = errors.New("blob: not found")

type Info struct {
	Key         string
	Size        int64
	ContentType string
	SHA256      string
	ModTime     time.Time
}

// Store is implemented by the local and S3 backends.
type Store interface {
	// Put streams r into key and returns the hex sha256 of the bytes written.
	// size may be -1 when unknown. Writes are atomic: readers never see a partial blob.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (sha256 string, err error)
	Open(ctx context.Context, key string) (io.ReadSeekCloser, Info, error)
	Stat(ctx context.Context, key string) (Info, error)
	Delete(ctx context.Context, key string) error
	// Backend is "local" or "s3" (for /admin/storage and backup decisions).
	Backend() string
}
