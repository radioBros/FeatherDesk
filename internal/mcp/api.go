// SPDX-License-Identifier: AGPL-3.0-only

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
)

// maxAPIResponse bounds one in-process REST response (a large ticket with
// every message is well under this).
const maxAPIResponse = 8 << 20

// apiClient runs REST calls in-process through the full router as the MCP
// caller: the bearer token travels with every call, so authn re-resolves the
// PAT, applies the operation's x-scopes (I-6) and the route-group guards, and
// the handlers apply the same authz, lifecycle and customer-safe views
// (I-2/I-7/I-9) as for any other API client. The MCP layer never touches the
// store and can never do more than the REST API with the same token.
type apiClient struct {
	h     http.Handler
	token string
	outer *http.Request // the /mcp request: client IP, proxy headers
	reqID string        // the /mcp request id, so access-log lines correlate
}

// apiError is a non-2xx REST answer, decoded from its problem document.
type apiError struct {
	Status int                   `json:"status"`
	Slug   string                `json:"slug"`
	Title  string                `json:"title"`
	Detail string                `json:"detail,omitempty"`
	Fields []problems.FieldError `json:"errors,omitempty"`
	Extra  map[string]any        `json:"extra,omitempty"`
	header http.Header
}

func (e *apiError) Error() string {
	msg := e.Title
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return fmt.Sprintf("%s (%s, HTTP %d)", msg, e.Slug, e.Status)
}

// recorder is a bounded in-memory http.ResponseWriter.
type recorder struct {
	hdr    http.Header
	status int
	buf    bytes.Buffer
	over   bool
}

func (r *recorder) Header() http.Header { return r.hdr }
func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}
func (r *recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.buf.Len()+len(p) > maxAPIResponse {
		r.over = true
		return 0, errors.New("mcp: response too large")
	}
	return r.buf.Write(p)
}

// forwarded headers keep the client IP, scheme and locale of the outer call.
var forwarded = []string{"X-Forwarded-For", "X-Forwarded-Proto", "Accept-Language", "User-Agent"}

// do sends one REST call (path relative to /api/v1) and decodes a 2xx JSON
// body into out (nil = ignore). Non-2xx answers return *apiError.
func (c *apiClient) do(ctx context.Context, method, path string, query url.Values, body, out any) (http.Header, error) {
	target := "/api/v1" + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	// Never hand the inner request a resolved principal (authn must
	// re-authenticate the bearer token so x-scopes are enforced) nor the
	// outer request's chi route context (the router would resume routing
	// /mcp instead of starting afresh).
	ictx := context.WithValue(server.WithPrincipal(ctx, nil), chi.RouteCtxKey, (*chi.Context)(nil))
	req, err := http.NewRequestWithContext(ictx, method, target, rd)
	if err != nil {
		return nil, err
	}
	req.RemoteAddr = c.outer.RemoteAddr
	req.Host = c.outer.Host
	for _, h := range forwarded {
		if v := c.outer.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	if c.reqID != "" {
		req.Header.Set("X-Request-ID", c.reqID)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := &recorder{hdr: http.Header{}}
	c.h.ServeHTTP(rec, req)
	if rec.over {
		return nil, &apiError{Status: http.StatusInternalServerError, Slug: problems.SlugInternal, Title: "Response too large"}
	}
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	if rec.status >= 300 {
		return rec.hdr, decodeProblem(rec)
	}
	if out != nil && rec.buf.Len() > 0 {
		if err := json.Unmarshal(rec.buf.Bytes(), out); err != nil {
			return rec.hdr, fmt.Errorf("mcp: decode %s %s: %w", method, path, err)
		}
	}
	return rec.hdr, nil
}

func decodeProblem(rec *recorder) *apiError {
	e := &apiError{Status: rec.status, header: rec.hdr}
	var p map[string]any
	if strings.Contains(rec.hdr.Get("Content-Type"), "json") && json.Unmarshal(rec.buf.Bytes(), &p) == nil {
		if t, ok := p["type"].(string); ok {
			e.Slug = strings.TrimPrefix(t, problems.BaseURI)
		}
		e.Title, _ = p["title"].(string)
		e.Detail, _ = p["detail"].(string)
		if raw, ok := p["errors"]; ok {
			b, _ := json.Marshal(raw)
			_ = json.Unmarshal(b, &e.Fields)
		}
		for k, v := range p {
			switch k {
			case "type", "title", "status", "detail", "errors", "instance":
			default:
				if e.Extra == nil {
					e.Extra = map[string]any{}
				}
				e.Extra[k] = v
			}
		}
	}
	if e.Slug == "" {
		e.Slug = slugForStatus(rec.status)
	}
	if e.Title == "" {
		e.Title = http.StatusText(rec.status)
	}
	return e
}

func slugForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return problems.SlugBadRequest
	case http.StatusUnauthorized:
		return problems.SlugUnauthenticated
	case http.StatusForbidden:
		return problems.SlugForbidden
	case http.StatusNotFound, http.StatusGone:
		return problems.SlugNotFound
	case http.StatusConflict:
		return problems.SlugConflict
	case http.StatusUnprocessableEntity:
		return problems.SlugValidation
	case http.StatusTooManyRequests:
		return problems.SlugRateLimited
	case http.StatusRequestEntityTooLarge:
		return problems.SlugPayloadTooLarge
	case http.StatusServiceUnavailable:
		return problems.SlugUnavailable
	}
	return problems.SlugInternal
}

func asAPIError(err error, target **apiError) bool { return errors.As(err, target) }
