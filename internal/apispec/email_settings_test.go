// SPDX-License-Identifier: AGPL-3.0-only

package apispec_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/radioBros/FeatherDesk/api"
	"github.com/radioBros/FeatherDesk/internal/apispec"
	"github.com/radioBros/FeatherDesk/internal/config"
	"github.com/radioBros/FeatherDesk/internal/crypto"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/notify"
	"github.com/radioBros/FeatherDesk/internal/server"
	"github.com/radioBros/FeatherDesk/internal/settings"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// adminToken authenticates every request as one admin PAT (bearer requests
// are CSRF-exempt).
type adminToken struct{ id string }

func (a adminToken) Authenticate(r *http.Request) (*domain.Principal, error) {
	if r.Header.Get("Authorization") == "" {
		return nil, nil
	}
	return &domain.Principal{UserID: a.id, Kind: domain.KindStaff, Role: domain.RoleAdmin, Tier: 3, TokenID: "tok"}, nil
}

// emailServer mounts the real notify module (GET/PUT /admin/email) on the
// real router, with env as the process environment.
func emailServer(t *testing.T, db *store.DB, env map[string]string) http.Handler {
	t.Helper()
	getenv := func(k string) string { return env[k] }
	box, err := crypto.NewBox([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	cfgEnv := map[string]string{"FD_SECRET_KEY": strings.Repeat("k", 32), "FD_DATA_DIR": t.TempDir(), "FD_PUBLIC_URL": "https://help.example.test"}
	cfg, err := config.Load(func(k string) string { return cfgEnv[k] })
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	set := settings.New(db, box, getenv)
	m, err := notify.New(notify.Deps{DB: db, Log: log, Settings: set, Secrets: box, Cfg: cfg, Getenv: getenv})
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(server.Options{Config: cfg, Logger: log, DB: db, Spec: api.Spec})
	srv.SetAuthenticator(adminToken{id: storetest.CreateStaff(t, db, domain.RoleAdmin, 3)})
	m.Mount(srv.Groups())
	return srv.Handler()
}

// call runs one request through h and validates request and response
// against api/openapi.yaml.
func call(t *testing.T, h http.Handler, method, path string, body any) (int, []byte) {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	newReq := func() *http.Request {
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer tok")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		return req
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newReq())
	resp := rec.Result()
	vreq := newReq()
	apispec.ValidateRequest(t, vreq)
	apispec.ValidateResponse(t, vreq, resp)
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// passwordKeys returns the JSON paths of every key named "password" in doc.
func passwordKeys(v any, path string) []string {
	var out []string
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if k == "password" {
				out = append(out, path+"."+k)
			}
			out = append(out, passwordKeys(child, path+"."+k)...)
		}
	case []any:
		for _, child := range x {
			out = append(out, passwordKeys(child, path+"[]")...)
		}
	}
	return out
}

func assertNoPassword(t *testing.T, what string, b []byte, secrets ...string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("%s: %v: %s", what, err, b)
	}
	if keys := passwordKeys(doc, "$"); len(keys) > 0 {
		t.Errorf("%s serialises a password key at %v: %s", what, keys, b)
	}
	for _, s := range secrets {
		if strings.Contains(string(b), s) {
			t.Errorf("%s leaks the password value: %s", what, b)
		}
	}
	return doc
}

// TestEmailSettingsNeverSerialisesPassword (CON-014): the SMTP password is
// write-only (EmailSettings.smtp.password is writeOnly in the spec). Neither
// GET nor the PUT response of /admin/email ever carries a `password` key or
// the value — whether the password was stored through the API or comes from
// FD_SMTP_PASSWORD (env-locked) — only `password_set`.
func TestEmailSettingsNeverSerialisesPassword(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		const secret = "Sup3rS3cret!pw"
		h := emailServer(t, db, nil)
		code, b := call(t, h, "GET", "/api/v1/admin/email", nil)
		if code != 200 {
			t.Fatalf("GET %d %s", code, b)
		}
		assertNoPassword(t, "GET (no password yet)", b)
		put := map[string]any{
			"smtp": map[string]any{"host": "smtp.acme.test", "port": 587, "security": "starttls", "username": "mailer",
				"password": secret, "auth": "plain", "timeout_seconds": 10},
			"senders": map[string]any{"customer": map[string]any{"from_name": "Acme", "from_address": "support@acme.test"}},
		}
		code, b = call(t, h, "PUT", "/api/v1/admin/email", put)
		if code != 200 {
			t.Fatalf("PUT %d %s", code, b)
		}
		assertNoPassword(t, "PUT response", b, secret)
		code, b = call(t, h, "GET", "/api/v1/admin/email", nil)
		if code != 200 {
			t.Fatalf("GET %d %s", code, b)
		}
		doc := assertNoPassword(t, "GET (stored password)", b, secret)
		if smtp, _ := doc["smtp"].(map[string]any); smtp["password_set"] != true {
			t.Errorf("password_set is not true: %s", b)
		}

		const envSecret = "EnvOnlyS3cret!"
		h = emailServer(t, db, map[string]string{"FD_SMTP_PASSWORD": envSecret, "FD_SMTP_HOST": "env-smtp.acme.test"})
		code, b = call(t, h, "GET", "/api/v1/admin/email", nil)
		if code != 200 {
			t.Fatalf("GET (env) %d %s", code, b)
		}
		doc = assertNoPassword(t, "GET (env-locked password)", b, envSecret, secret)
		if smtp, _ := doc["smtp"].(map[string]any); smtp["password_set"] != true || !strings.Contains(string(b), "smtp.password") {
			t.Errorf("env password not reported as set and locked: %s", b)
		}
	})
}
