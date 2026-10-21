// SPDX-License-Identifier: AGPL-3.0-only

package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"iter"
	"net/url"
	"os"
	"strings"
)

// NewFromURL selects the backend from ST_BLOB_URL (D-17):
//
//	""  or file:///data             local filesystem (default root /data)
//	s3://bucket/prefix?endpoint=https://minio:9000&region=us-east-1&path_style=true&presign=false
//
// S3 credentials come from ST_S3_ACCESS_KEY / ST_S3_SECRET_KEY, never the URL.
func NewFromURL(raw string) (Store, error) {
	switch {
	case raw == "":
		return NewLocal("/data")
	case strings.HasPrefix(raw, "file://"):
		return NewLocal(strings.TrimPrefix(raw, "file://"))
	case strings.HasPrefix(raw, "s3://"):
		u, err := url.Parse(raw)
		if err != nil {
			return nil, err
		}
		q := u.Query()
		s, err := NewS3(u.Host, strings.Trim(u.Path, "/"), q.Get("endpoint"), q.Get("region"),
			os.Getenv("ST_S3_ACCESS_KEY"), os.Getenv("ST_S3_SECRET_KEY"), q.Get("path_style") == "true")
		if err != nil {
			return nil, err
		}
		s.Presign = q.Get("presign") == "true"
		return s, nil
	}
	return nil, fmt.Errorf("blob: unsupported ST_BLOB_URL %q (want file:// or s3://)", raw)
}

// MigrateResult reports a copy between backends.
type MigrateResult struct {
	Copied int
	Bytes  int64
}

// Migrate copies every key from one backend to another and verifies the sha256
// of each copy against the source bytes (`simpletickets blob migrate`).
func Migrate(ctx context.Context, from, to Store, keys iter.Seq[string]) (MigrateResult, error) {
	var res MigrateResult
	for key := range keys {
		rc, info, err := from.Open(ctx, key)
		if err != nil {
			return res, fmt.Errorf("open %s: %w", key, err)
		}
		h := sha256.New()
		got, err := to.Put(ctx, key, io.TeeReader(rc, h), info.Size, info.ContentType)
		rc.Close()
		if err != nil {
			return res, fmt.Errorf("put %s: %w", key, err)
		}
		if want := hex.EncodeToString(h.Sum(nil)); got != want {
			return res, fmt.Errorf("verify %s: sha256 %s != source %s", key, got, want)
		}
		res.Copied++
		res.Bytes += info.Size
	}
	return res, nil
}
