// SPDX-License-Identifier: AGPL-3.0-only

// Package problems implements RFC 9457 problem details. Every error that
// reaches an HTTP client goes through Write; handlers never call http.Error.
package problems

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/radioBros/FeatherDesk/internal/blob"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// BaseURI prefixes every problem type slug.
const BaseURI = "https://featherdesk.dev/problems/"

// Registered slugs (CON-006). Clients switch on these; never invent ad-hoc ones.
const (
	SlugBadRequest          = "bad-request"
	SlugValidation          = "validation"
	SlugUnauthenticated     = "unauthenticated"
	SlugForbidden           = "forbidden"
	SlugNotFound            = "not-found"
	SlugConflict            = "conflict"
	SlugInvalidTransition   = "invalid-transition"
	SlugRateLimited         = "rate-limited"
	SlugCSRF                = "csrf"
	SlugIdempotencyMismatch = "idempotency-mismatch"
	SlugIdempotencyInFlight = "idempotency-in-flight"
	SlugSetupRequired       = "setup-required"
	SlugEmailDisabled       = "email-disabled"
	SlugMergeCrossRequester = "merge-cross-requester"
	SlugLinkCycle           = "link-cycle"
	SlugFieldRequired       = "field-required"
	SlugPayloadTooLarge     = "payload-too-large"
	SlugUnsupportedMedia    = "unsupported-media-type"
	SlugPreconditionFailed  = "precondition-failed"
	SlugMethodNotAllowed    = "method-not-allowed"
	SlugInternal            = "internal"
	SlugUnavailable         = "unavailable"
	SlugPortalDisabled      = "portal-disabled"
	// I-12: posting on behalf of a customer (tickets:act_as_customer).
	SlugOnBehalfNotCustomer = "on-behalf-not-customer"
	SlugOnBehalfNotVisible  = "on-behalf-not-visible"
	SlugOnBehalfDisabled    = "on-behalf-disabled"
)

// Registry maps every slug to its HTTP status and default title.
var Registry = map[string]struct {
	Status int
	Title  string
}{
	SlugBadRequest:          {400, "Bad request"},
	SlugValidation:          {422, "Validation failed"},
	SlugUnauthenticated:     {401, "Authentication required"},
	SlugForbidden:           {403, "Forbidden"},
	SlugNotFound:            {404, "Not found"},
	SlugConflict:            {409, "Conflict"},
	SlugInvalidTransition:   {409, "Invalid status transition"},
	SlugRateLimited:         {429, "Too many requests"},
	SlugCSRF:                {403, "CSRF check failed"},
	SlugIdempotencyMismatch: {422, "Idempotency key reused with a different request"},
	SlugIdempotencyInFlight: {409, "A request with this idempotency key is in progress"},
	SlugSetupRequired:       {409, "Setup required"},
	SlugEmailDisabled:       {409, "Email is not configured"},
	SlugMergeCrossRequester: {409, "Tickets have different requesters"},
	SlugLinkCycle:           {409, "Link would create a cycle"},
	SlugFieldRequired:       {422, "Required field missing"},
	SlugPayloadTooLarge:     {413, "Payload too large"},
	SlugUnsupportedMedia:    {415, "Unsupported media type"},
	SlugPreconditionFailed:  {412, "Precondition failed"},
	SlugMethodNotAllowed:    {405, "Method not allowed"},
	SlugInternal:            {500, "Internal server error"},
	SlugUnavailable:         {503, "Service unavailable"},
	SlugPortalDisabled:      {404, "Customer portal is disabled"},
	SlugOnBehalfNotCustomer: {403, "The on_behalf_of user is not a customer"},
	SlugOnBehalfNotVisible:  {403, "The customer cannot see this ticket"},
	SlugOnBehalfDisabled:    {409, "The customer account is disabled"},
}

// FieldError describes one invalid field; Code matches web/src/constants/validations.ts keys.
type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// Problem is the wire format (application/problem+json).
type Problem struct {
	Type     string         `json:"type"`
	Title    string         `json:"title"`
	Status   int            `json:"status"`
	Detail   string         `json:"detail,omitempty"`
	Instance string         `json:"instance,omitempty"`
	Errors   []FieldError   `json:"errors,omitempty"`
	Extra    map[string]any `json:"-"`
}

// MarshalJSON flattens Extra members next to the standard ones (RFC 9457 §3.2).
func (p Problem) MarshalJSON() ([]byte, error) {
	m := map[string]any{"type": p.Type, "title": p.Title, "status": p.Status}
	if p.Detail != "" {
		m["detail"] = p.Detail
	}
	if p.Instance != "" {
		m["instance"] = p.Instance
	}
	if len(p.Errors) > 0 {
		m["errors"] = p.Errors
	}
	for k, v := range p.Extra {
		if _, taken := m[k]; !taken {
			m[k] = v
		}
	}
	return json.Marshal(m)
}

// Error is the typed error handlers return; Write turns it into a Problem.
type Error struct {
	Slug       string
	Detail     string
	Fields     []FieldError
	Extra      map[string]any
	RetryAfter time.Duration
	Cause      error
}

func (e *Error) Error() string {
	if e.Detail != "" {
		return e.Slug + ": " + e.Detail
	}
	return e.Slug
}

func (e *Error) Unwrap() error { return e.Cause }

// Status returns the HTTP status for the slug (500 for unknown slugs).
func (e *Error) Status() int {
	if r, ok := Registry[e.Slug]; ok {
		return r.Status
	}
	return http.StatusInternalServerError
}

func New(slug, detail string) *Error { return &Error{Slug: slug, Detail: detail} }

func BadRequest(detail string) *Error   { return New(SlugBadRequest, detail) }
func NotFound() *Error                  { return New(SlugNotFound, "") }
func Forbidden(detail string) *Error    { return New(SlugForbidden, detail) }
func Unauthenticated() *Error           { return New(SlugUnauthenticated, "") }
func Conflict(detail string) *Error     { return New(SlugConflict, detail) }
func CSRF() *Error                      { return New(SlugCSRF, "") }
func PayloadTooLarge() *Error           { return New(SlugPayloadTooLarge, "") }
func PreconditionFailed() *Error        { return New(SlugPreconditionFailed, "") }
func Internal(cause error) *Error       { return &Error{Slug: SlugInternal, Cause: cause} }
func UnsupportedMedia(ct string) *Error { return New(SlugUnsupportedMedia, ct) }

// Validation builds a 422 with per-field errors.
func Validation(fields ...FieldError) *Error {
	return &Error{Slug: SlugValidation, Fields: fields}
}

// Field is a shorthand for one FieldError.
func Field(field, code, message string) FieldError {
	return FieldError{Field: field, Code: code, Message: message}
}

// InvalidTransition lists the statuses that are reachable from the current one.
func InvalidTransition(from string, allowed []string) *Error {
	return &Error{Slug: SlugInvalidTransition, Detail: fmt.Sprintf("cannot leave %q this way", from), Extra: map[string]any{"allowed": allowed}}
}

func RateLimited(retryAfter time.Duration) *Error {
	return &Error{Slug: SlugRateLimited, RetryAfter: retryAfter}
}

// From converts any error into a *Error: typed errors pass through,
// not-found sentinels map to 404, everything else becomes 500.
func From(err error) *Error {
	var pe *Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &pe):
		return pe
	case errors.Is(err, store.ErrNotFound), errors.Is(err, blob.ErrNotFound):
		return NotFound()
	}
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return PayloadTooLarge()
	}
	return Internal(err)
}

// Write sends err as application/problem+json. Internal errors are logged with
// the request's logger and never leak their message to the client.
func Write(w http.ResponseWriter, r *http.Request, err error) {
	pe := From(err)
	status := pe.Status()
	title := "Error"
	if reg, ok := Registry[pe.Slug]; ok {
		title = reg.Title
	}
	p := Problem{Type: BaseURI + pe.Slug, Title: title, Status: status, Detail: pe.Detail, Errors: pe.Fields, Extra: pe.Extra}
	if r != nil {
		p.Instance = r.URL.Path
	}
	if status >= 500 {
		p.Detail = ""
		logger := slog.Default()
		if r != nil {
			if l, ok := r.Context().Value(loggerKey{}).(*slog.Logger); ok && l != nil {
				logger = l
			}
		}
		logger.Error("request failed", "err", err)
	}
	if pe.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int((pe.RetryAfter+time.Second-1)/time.Second)))
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

type loggerKey struct{}

// LoggerKey is the context key under which the server stores the request logger.
var LoggerKey any = loggerKey{}
