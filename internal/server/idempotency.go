// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// idempotency implements the Idempotency-Key header (PLT-020): the first
// response (status < 500) is stored for 24 h per (principal, key) and replayed
// for an identical request; a different body under the same key is a 422; a
// concurrent duplicate is a 409. Rows are pruned by the store prune job.
type idempotency struct {
	db       *store.DB
	mu       sync.Mutex
	inflight map[string]bool
}

func newIdempotency(db *store.DB) *idempotency {
	return &idempotency{db: db, inflight: map[string]bool{}}
}

const maxStoredBody = 256 << 10

type captureWriter struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (c *captureWriter) WriteHeader(code int) {
	if c.status == 0 {
		c.status = code
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *captureWriter) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	if c.buf.Len() < maxStoredBody {
		c.buf.Write(b)
	}
	return c.ResponseWriter.Write(b)
}

func (c *captureWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func (i *idempotency) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		p := PrincipalFrom(r.Context())
		if key == "" || p == nil {
			next.ServeHTTP(w, r)
			return
		}
		if len(key) > 200 {
			problems.Write(w, r, problems.Validation(problems.Field("Idempotency-Key", "max_length", "at most 200 characters")))
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			problems.Write(w, r, err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		h := sha256.New()
		h.Write([]byte(r.Method + " " + r.URL.Path + "\n"))
		h.Write(body)
		hash := hex.EncodeToString(h.Sum(nil))
		ctx := r.Context()

		var storedHash, respBody string
		var status int
		err = i.db.Read().QueryRowContext(ctx, `SELECT request_hash, status_code, response_body FROM idempotency_keys WHERE principal_id = ? AND key = ?`, p.UserID, key).Scan(&storedHash, &status, &respBody)
		if err == nil {
			if storedHash != hash {
				problems.Write(w, r, problems.New(problems.SlugIdempotencyMismatch, ""))
				return
			}
			w.Header().Set("Idempotency-Replayed", "true")
			w.Header().Set("Content-Type", "application/json")
			if status >= 400 {
				w.Header().Set("Content-Type", "application/problem+json")
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(respBody))
			return
		}
		if !errors.Is(store.NotFound(err), store.ErrNotFound) {
			problems.Write(w, r, err)
			return
		}
		ik := p.UserID + "|" + key
		i.mu.Lock()
		if i.inflight[ik] {
			i.mu.Unlock()
			problems.Write(w, r, problems.New(problems.SlugIdempotencyInFlight, ""))
			return
		}
		i.inflight[ik] = true
		i.mu.Unlock()
		defer func() {
			i.mu.Lock()
			delete(i.inflight, ik)
			i.mu.Unlock()
		}()
		cw := &captureWriter{ResponseWriter: w}
		next.ServeHTTP(cw, r)
		if cw.status == 0 || cw.status >= 500 || cw.buf.Len() >= maxStoredBody {
			return
		}
		_ = i.db.Tx(ctx, func(tx *store.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO idempotency_keys (principal_id, key, request_hash, status_code, response_body, created_at) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (principal_id, key) DO NOTHING`,
				p.UserID, key, hash, cw.status, cw.buf.String(), store.Now())
			return err
		})
	})
}
