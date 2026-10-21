// SPDX-License-Identifier: AGPL-3.0-only

package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Prefixes used under the store root. Backup archives exactly these.
var Prefixes = []string{"attachments", "branding", "kb", "inbound"}

// NewKey returns a time-bucketed key such as "attachments/2026/10/<uuidv7>".
func NewKey(prefix string, now time.Time) string {
	now = now.UTC()
	return fmt.Sprintf("%s/%04d/%02d/%s", prefix, now.Year(), int(now.Month()), uuid.Must(uuid.NewV7()).String())
}

// ValidKey rejects absolute paths, traversal and empty segments.
func ValidKey(key string) error {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") {
		return fmt.Errorf("blob: invalid key %q", key)
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("blob: invalid key %q", key)
		}
	}
	return nil
}

// Local stores blobs as files under Root (default /data). Writes go to a temp
// file in Root/.tmp and are renamed into place, so readers never see partial data.
type Local struct {
	Root string
}

func NewLocal(root string) (*Local, error) {
	if root == "" {
		root = "/data"
	}
	if err := os.MkdirAll(filepath.Join(root, ".tmp"), 0o700); err != nil {
		return nil, err
	}
	return &Local{Root: root}, nil
}

func (l *Local) Backend() string { return "local" }

func (l *Local) path(key string) (string, error) {
	if err := ValidKey(key); err != nil {
		return "", err
	}
	return filepath.Join(l.Root, filepath.FromSlash(key)), nil
}

func (l *Local) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (string, error) {
	dst, err := l.path(key)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Join(l.Root, ".tmp"), "put-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	h := sha256.New()
	src := io.Reader(ctxReader{ctx, r})
	if size >= 0 {
		src = io.LimitReader(src, size+1) // abort at size+1 bytes instead of reading an oversized stream to the end
	}
	n, err := io.Copy(io.MultiWriter(tmp, h), src)
	if err == nil && size >= 0 && n != size {
		err = fmt.Errorf("blob: short write: %d of %d bytes", n, size)
	}
	if err == nil {
		err = tmp.Chmod(0o600)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (l *Local) Open(ctx context.Context, key string) (io.ReadSeekCloser, Info, error) {
	p, err := l.path(key)
	if err != nil {
		return nil, Info{}, err
	}
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, Info{}, ErrNotFound
	}
	if err != nil {
		return nil, Info{}, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, Info{}, err
	}
	return f, Info{Key: key, Size: st.Size(), ModTime: st.ModTime()}, nil
}

func (l *Local) Stat(ctx context.Context, key string) (Info, error) {
	p, err := l.path(key)
	if err != nil {
		return Info{}, err
	}
	st, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return Info{}, ErrNotFound
	}
	if err != nil {
		return Info{}, err
	}
	return Info{Key: key, Size: st.Size(), ModTime: st.ModTime()}, nil
}

// Delete is idempotent: deleting a missing key is not an error.
func (l *Local) Delete(ctx context.Context, key string) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
