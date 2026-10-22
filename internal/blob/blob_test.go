// SPDX-License-Identifier: AGPL-3.0-only

package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var ctx = context.Background()

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// exercise runs the common contract against any backend.
func exercise(t *testing.T, s Store) {
	t.Helper()
	data := bytes.Repeat([]byte("hello blob "), 1000)
	key := NewKey("attachments", time.Now())
	got, err := s.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if got != sum(data) {
		t.Fatalf("sha %s want %s", got, sum(data))
	}
	info, err := s.Stat(ctx, key)
	if err != nil || info.Size != int64(len(data)) {
		t.Fatalf("stat %+v %v", info, err)
	}
	rc, _, err := s.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rc.Seek(6, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	part := make([]byte, 4)
	if _, err := io.ReadFull(rc, part); err != nil || string(part) != "blob" {
		t.Fatalf("ranged read %q %v", part, err)
	}
	if _, err := rc.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("rc.Seek: %v", err)
	}
	all, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || !bytes.Equal(all, data) {
		t.Fatalf("full read: %d bytes %v", len(all), err)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatal("delete must be idempotent:", err)
	}
	if _, err := s.Stat(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stat after delete: %v", err)
	}
	if _, _, err := s.Open(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("open after delete: %v", err)
	}
	// Short body is rejected and leaves nothing behind.
	if _, err := s.Put(ctx, key, strings.NewReader("abc"), 10, ""); err == nil {
		t.Fatal("short write accepted")
	}
	if _, err := s.Stat(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial blob visible: %v", err)
	}
}

func TestLocalRoundTrip(t *testing.T) {
	s, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exercise(t, s)
	st, _ := os.Stat(s.Root)
	if st == nil {
		t.Fatal("root missing")
	}
}

func TestLocalRejectsTraversal(t *testing.T) {
	s, _ := NewLocal(t.TempDir())
	for _, k := range []string{"../etc/passwd", "/abs", "a//b", "a/./b", "a\\b", ""} {
		if _, err := s.Put(ctx, k, strings.NewReader("x"), 1, ""); err == nil {
			t.Errorf("key %q accepted", k)
		}
	}
}

func TestLocalFileMode(t *testing.T) {
	s, _ := NewLocal(t.TempDir())
	if _, err := s.Put(ctx, "branding/x.png", strings.NewReader("x"), 1, ""); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(s.Root + "/branding/x.png")
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", st.Mode(), err)
	}
}

// fakeS3 is an in-memory S3 that checks every request carries a SigV4
// Authorization header and a content sha256 matching the body.
type fakeS3 struct {
	mu   sync.Mutex
	objs map[string][]byte
	ct   map[string]string
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AK/") {
		http.Error(w, "unsigned", http.StatusForbidden)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := r.URL.Path
	switch r.Method {
	case http.MethodPut:
		b, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-Amz-Content-Sha256") != sum(b) {
			http.Error(w, "bad payload hash", http.StatusBadRequest)
			return
		}
		f.objs[key] = b
		f.ct[key] = r.Header.Get("Content-Type")
	case http.MethodHead, http.MethodGet:
		b, ok := f.objs[key]
		if !ok {
			http.Error(w, "nope", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", f.ct[key])
		http.ServeContent(w, r, key, time.Unix(0, 0), bytes.NewReader(b))
	case http.MethodDelete:
		delete(f.objs, key)
		w.WriteHeader(http.StatusNoContent)
	}
}

func TestS3AgainstFake(t *testing.T) {
	f := &fakeS3{objs: map[string][]byte{}, ct: map[string]string{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	s, err := NewS3("bucket", "pfx", srv.URL, "us-east-1", "AK", "SK", true)
	if err != nil {
		t.Fatal(err)
	}
	exercise(t, s)
	if _, err := s.Put(ctx, "kb/a.png", strings.NewReader("png"), 3, "image/png"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.objs["/bucket/pfx/kb/a.png"]; !ok {
		t.Fatalf("path-style key layout: %v", f.objs)
	}
	u, err := s.PresignGet("kb/a.png", time.Minute)
	if err != nil || !strings.Contains(u, "X-Amz-Signature=") || !strings.Contains(u, "/bucket/pfx/kb/a.png?") {
		t.Fatalf("presign %s %v", u, err)
	}
}

// TestS3Integration runs against a real S3-compatible server (MinIO in CI) when
// FD_TEST_S3_URL is set, e.g. s3://test/st?endpoint=http://localhost:9000&path_style=true
// with FD_S3_ACCESS_KEY / FD_S3_SECRET_KEY.
func TestS3Integration(t *testing.T) {
	raw := os.Getenv("FD_TEST_S3_URL")
	if raw == "" {
		t.Skip("FD_TEST_S3_URL not set")
	}
	s, err := NewFromURL(raw)
	if err != nil {
		t.Fatal(err)
	}
	exercise(t, s)
	s3 := s.(*S3)
	key := NewKey("kb", time.Now())
	if _, err := s3.Put(ctx, key, strings.NewReader("presigned!"), 10, "text/plain"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := s3.Delete(ctx, key); err != nil {
			t.Errorf("s3.Delete: %v", err)
		}
	}()
	u, err := s3.PresignGet(key, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "presigned!" {
		t.Fatalf("presigned GET: %d %s", resp.StatusCode, body)
	}
}

func TestNewFromURL(t *testing.T) {
	dir := t.TempDir()
	s, err := NewFromURL("file://" + dir)
	if err != nil || s.Backend() != "local" {
		t.Fatalf("%v %v", s, err)
	}
	t.Setenv("FD_S3_ACCESS_KEY", "a")
	t.Setenv("FD_S3_SECRET_KEY", "b")
	s, err = NewFromURL("s3://bkt/some/prefix?endpoint=http://minio:9000&region=eu-west-1&path_style=true&presign=true")
	if err != nil {
		t.Fatal(err)
	}
	s3 := s.(*S3)
	if s3.Bucket != "bkt" || s3.Prefix != "some/prefix" || !s3.PathStyle || !s3.Presign || s3.signer.region != "eu-west-1" || s3.Endpoint.Host != "minio:9000" {
		t.Fatalf("%+v", s3)
	}
	t.Setenv("FD_S3_SECRET_KEY", "")
	if _, err := NewFromURL("s3://bkt"); err == nil {
		t.Fatal("missing credentials accepted")
	}
	if _, err := NewFromURL("ftp://x"); err == nil {
		t.Fatal("unknown scheme accepted")
	}
}

func TestMigrateVerifiesCopies(t *testing.T) {
	from, _ := NewLocal(t.TempDir())
	to, _ := NewLocal(t.TempDir())
	var keys []string
	for i := 0; i < 5; i++ {
		k := NewKey("attachments", time.Now())
		if _, err := from.Put(ctx, k, strings.NewReader(strings.Repeat("x", i+1)), int64(i+1), ""); err != nil {
			t.Fatalf("from.Put: %v", err)
		}
		keys = append(keys, k)
	}
	res, err := Migrate(ctx, from, to, slices.Values(keys))
	if err != nil || res.Copied != 5 || res.Bytes != 15 {
		t.Fatalf("%+v %v", res, err)
	}
	for _, k := range keys {
		if _, err := to.Stat(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
}

// TestBlobSizeLimit (DB-014): a stream longer than the declared size aborts at
// size+1 bytes and leaves nothing behind.
func TestBlobSizeLimit(t *testing.T) {
	s, _ := NewLocal(t.TempDir())
	var read int64
	r := readerFunc(func(p []byte) (int, error) { read += int64(len(p)); return len(p), nil }) // infinite stream
	if _, err := s.Put(ctx, "attachments/big", r, 1024, ""); err == nil {
		t.Fatal("oversized stream accepted")
	}
	if read > 1024+64*1024 {
		t.Fatalf("read %d bytes of an infinite stream; must stop near the limit", read)
	}
	if _, err := s.Stat(ctx, "attachments/big"); !errors.Is(err, ErrNotFound) {
		t.Fatal("partial blob left behind")
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }
