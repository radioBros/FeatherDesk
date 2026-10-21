// SPDX-License-Identifier: AGPL-3.0-only

package demo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
)

// APIError is a non-2xx answer of the in-process API.
type APIError struct {
	Method, Path string
	Status       int
	Body         string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("demo: %s %s: HTTP %d: %s", e.Method, e.Path, e.Status, strings.TrimSpace(e.Body))
}

// client drives the application's real HTTP handler in-process (no socket, no
// network): one instance per signed-in user, or one per bearer token. Going
// through the router means every rule (visibility, transitions, validation,
// events, audit) is the production one.
type client struct {
	h       http.Handler
	origin  string // scheme://host of ST_PUBLIC_URL (Origin/Host for the CSRF check)
	host    string
	ip      string // distinct remote address per user (per-IP throttles)
	cookies map[string]string
	csrf    string
	bearer  string
	userID  string
	name    string
}

func newClient(h http.Handler, origin, host, ip string) *client {
	return &client{h: h, origin: origin, host: host, ip: ip, cookies: map[string]string{}}
}

func (c *client) request(method, path string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/v1"+path, body)
	req.Host = c.host
	req.RemoteAddr = c.ip + ":40000"
	req.Header.Set("Origin", c.origin)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	for k, v := range c.cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	if c.csrf != "" && method != http.MethodGet {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.MaxAge < 0 || ck.Value == "" {
			delete(c.cookies, ck.Name)
		} else {
			c.cookies[ck.Name] = ck.Value
		}
	}
	return rec
}

func (c *client) finish(method, path string, rec *httptest.ResponseRecorder, out any) error {
	if rec.Code < 200 || rec.Code > 299 {
		return &APIError{Method: method, Path: path, Status: rec.Code, Body: rec.Body.String()}
	}
	if out != nil && rec.Code != http.StatusNoContent && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			return fmt.Errorf("demo: %s %s: decode: %w", method, path, err)
		}
	}
	return nil
}

// do sends a JSON request (body may be nil) and decodes a 2xx answer into out.
func (c *client) do(method, path string, body, out any) error {
	var r io.Reader
	ct := ""
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r, ct = bytes.NewReader(b), "application/json"
	}
	return c.finish(method, path, c.request(method, path, r, ct), out)
}

func (c *client) get(path string, out any) error { return c.do(http.MethodGet, path, nil, out) }
func (c *client) post(path string, body, out any) error {
	return c.do(http.MethodPost, path, body, out)
}
func (c *client) put(path string, body, out any) error { return c.do(http.MethodPut, path, body, out) }
func (c *client) patch(path string, body, out any) error {
	return c.do(http.MethodPatch, path, body, out)
}

// upload sends one multipart file field (attachments, branding assets).
func (c *client) upload(path, filename, contentType string, data []byte, out any) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	h.Set("Content-Type", contentType)
	part, err := mw.CreatePart(h)
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	return c.finish(http.MethodPost, path, c.request(http.MethodPost, path, &buf, mw.FormDataContentType()), out)
}

// login signs in with a local password and keeps the session cookie and CSRF
// token for later calls.
func (c *client) login(audience, email, password string) error {
	var me struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		CSRFToken string `json:"csrf_token"`
	}
	if err := c.post("/auth/login", map[string]string{"audience": audience, "email": email, "password": password}, &me); err != nil {
		return err
	}
	c.csrf, c.userID, c.name = me.CSRFToken, me.ID, me.Name
	return nil
}
