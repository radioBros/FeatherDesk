// SPDX-License-Identifier: AGPL-3.0-only

// Package mcp is the built-in Model Context Protocol server: AI clients
// (Claude Code, Claude Desktop, Cursor, …) reach SimpleTickets at POST /mcp
// over the Streamable HTTP transport with a personal access token.
//
// It is dual-era (MCP "Versioning and Compatibility"):
//
//   - 2026-07-28 (modern): stateless; every request carries its protocol
//     version and client capabilities in params._meta, mirrored in the
//     MCP-Protocol-Version / Mcp-Method / Mcp-Name headers; server/discover.
//   - 2025-11-25, 2025-06-18 and 2025-03-26 (legacy): an initialize handshake
//     creates a session (Mcp-Session-Id); 2025-03-26 also accepts batches.
//
// JSON-RPC is hand-written (no dependency). Every tool runs as a REST call
// through the full router with the caller's bearer token (api.go), so scopes,
// authorisation, the ticket lifecycle and the customer-safe views are exactly
// those of the REST API.
package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
)

// Protocol revisions this server speaks.
const (
	Version20260728 = "2026-07-28" // modern (stateless)
	Version20251125 = "2025-11-25"
	Version20250618 = "2025-06-18"
	Version20250326 = "2025-03-26"
)

// SupportedVersions lists every revision, newest first.
var SupportedVersions = []string{Version20260728, Version20251125, Version20250618, Version20250326}

var legacyVersions = []string{Version20251125, Version20250618, Version20250326}

func isLegacy(v string) bool { return slices.Contains(legacyVersions, v) }

// Reserved _meta keys (2026-07-28).
const (
	metaProtocolVersion = "io.modelcontextprotocol/protocolVersion"
	metaClientCaps      = "io.modelcontextprotocol/clientCapabilities"
	metaClientInfo      = "io.modelcontextprotocol/clientInfo"
	metaServerInfo      = "io.modelcontextprotocol/serverInfo"
)

// HTTP headers of the Streamable HTTP transport.
const (
	headerSession  = "Mcp-Session-Id"
	headerVersion  = "MCP-Protocol-Version"
	headerMethod   = "Mcp-Method"
	headerName     = "Mcp-Name"
	maxBody        = 1 << 20
	maxBatch       = 32
	serverName     = "simpletickets"
	serverTitle    = "SimpleTickets"
	defaultMaxSess = 10_000
	defaultSessTTL = 24 * time.Hour
)

// Options configure the handler.
type Options struct {
	// API is the full router; tools run through it in-process.
	API http.Handler
	// Enabled reports integrations.mcp_enabled; false answers 404.
	Enabled func(ctx context.Context) (bool, error)
	// Origins are the accepted browser origins (ST_PUBLIC_URL's, then
	// ST_ALLOWED_ORIGINS): a request with any other Origin header is refused
	// (DNS rebinding, MCP transport security).
	Origins []string
	// Limit builds the per-user rate limiter (server.(*Server).Limit); nil = none.
	Limit       func(name string, perSecond float64, burst int) func(http.Handler) http.Handler
	Log         *slog.Logger
	Version     string
	Now         func() time.Time
	MaxSessions int
	SessionTTL  time.Duration
}

// Handler serves /mcp.
type Handler struct {
	opt      Options
	sessions *sessions
	userRate func(http.Handler) http.Handler
}

// New builds the handler.
func New(opt Options) *Handler {
	if opt.Log == nil {
		opt.Log = slog.Default()
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.MaxSessions <= 0 {
		opt.MaxSessions = defaultMaxSess
	}
	if opt.SessionTTL <= 0 {
		opt.SessionTTL = defaultSessTTL
	}
	if opt.Version == "" {
		opt.Version = "dev"
	}
	h := &Handler{opt: opt, sessions: newSessions(opt.MaxSessions, opt.SessionTTL, opt.Now)}
	h.userRate = func(next http.Handler) http.Handler { return next }
	if opt.Limit != nil {
		// Keyed by the token owner (the principal is set just for the limiter).
		h.userRate = opt.Limit("mcp", 10, 40)
	}
	return h
}

// caller is the authenticated MCP client: the PAT owner as GET /me reports it.
type caller struct {
	UserID   string          `json:"id"`
	Kind     domain.UserKind `json:"kind"`
	Role     domain.Role     `json:"role"`
	Scopes   []string        `json:"scopes"`
	Name     string          `json:"name"`
	tokenKey string
	api      *apiClient
}

func (c *caller) staff() bool { return c.Kind == domain.KindStaff }

// can mirrors domain.Principal.HasScope (admin implies all, tickets:read
// implies kb:read) for the token's effective scopes.
func (c *caller) can(anyOf []string) bool {
	if len(anyOf) == 0 {
		return true
	}
	p := &domain.Principal{Kind: c.Kind, Role: c.Role, Scopes: c.Scopes}
	if p.Scopes == nil {
		p.Scopes = []string{}
	}
	for _, s := range anyOf {
		if p.HasScope(s) {
			return true
		}
	}
	return false
}

// ---- HTTP entry points ----

// gate runs the checks shared by every method: the setting (404), Origin
// (403) and the bearer token (401). Cookies are never accepted.
func (h *Handler) gate(w http.ResponseWriter, r *http.Request) (*caller, bool) {
	on, err := h.opt.Enabled(r.Context())
	if err != nil {
		problems.Write(w, r, err)
		return nil, false
	}
	if !on {
		problems.Write(w, r, problems.NotFound())
		return nil, false
	}
	if !h.originAllowed(r) {
		problems.Write(w, r, problems.Forbidden("Origin not allowed"))
		return nil, false
	}
	token := bearer(r)
	if token == "" {
		unauthenticated(w, r)
		return nil, false
	}
	c := &caller{tokenKey: tokenKey(token), api: &apiClient{h: h.opt.API, token: token, outer: r, reqID: w.Header().Get("X-Request-ID")}}
	if _, err := c.api.do(r.Context(), http.MethodGet, "/me", nil, nil, c); err != nil {
		var ae *apiError
		if errors.As(err, &ae) {
			if ae.Status == http.StatusUnauthorized {
				unauthenticated(w, r)
				return nil, false
			}
			problems.Write(w, r, &problems.Error{Slug: ae.Slug, Detail: ae.Detail})
			return nil, false
		}
		problems.Write(w, r, err)
		return nil, false
	}
	if c.Scopes == nil {
		c.Scopes = []string{}
	}
	return c, true
}

// originAllowed: no Origin (desktop and CLI clients) or one of the
// configured origins. Browsers always send Origin on a cross-site POST.
func (h *Handler) originAllowed(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	for _, want := range h.opt.Origins {
		if strings.EqualFold(o, want) {
			return true
		}
	}
	return false
}

func bearer(r *http.Request) string {
	scheme, tok, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(tok)
}

func tokenKey(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func unauthenticated(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="SimpleTickets MCP", error="invalid_token"`)
	problems.Write(w, r, problems.Unauthenticated())
}

// ServeGet answers GET /mcp: this server never opens a standalone SSE
// stream (every response is a single JSON body), so 405.
func (h *Handler) ServeGet(w http.ResponseWriter, r *http.Request) {
	if on, err := h.opt.Enabled(r.Context()); err != nil || !on {
		problems.Write(w, r, problems.NotFound())
		return
	}
	w.Header().Set("Allow", "POST, DELETE")
	problems.Write(w, r, problems.New(problems.SlugMethodNotAllowed, "GET streams are not offered; POST JSON-RPC messages"))
}

// ServeDelete ends a legacy session (Mcp-Session-Id).
func (h *Handler) ServeDelete(w http.ResponseWriter, r *http.Request) {
	c, ok := h.gate(w, r)
	if !ok {
		return
	}
	id := r.Header.Get(headerSession)
	if id == "" {
		w.Header().Set("Allow", "POST")
		problems.Write(w, r, problems.New(problems.SlugMethodNotAllowed, "no session to end"))
		return
	}
	if !h.sessions.delete(id, c.tokenKey) {
		problems.Write(w, r, problems.NotFound())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ServePost handles one JSON-RPC message (or a 2025-03-26 batch).
func (h *Handler) ServePost(w http.ResponseWriter, r *http.Request) {
	c, ok := h.gate(w, r)
	if !ok {
		return
	}
	limited := server.WithPrincipal(r.Context(), &domain.Principal{UserID: c.UserID, Kind: c.Kind})
	h.userRate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h.post(w, r, c)
	})).ServeHTTP(w, r.WithContext(limited))
}

func (h *Handler) post(w http.ResponseWriter, r *http.Request, c *caller) {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		problems.Write(w, r, problems.UnsupportedMedia(r.Header.Get("Content-Type")))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	items, batch, err := splitBatch(body)
	if err != nil {
		writeRPC(w, http.StatusBadRequest, errorResponse(nil, newError(codeParseError, "Parse error: "+err.Error())))
		return
	}
	if batch {
		h.serveBatch(w, r, c, items)
		return
	}
	m, bad := decodeMessage(items[0])
	if bad != nil {
		writeRPC(w, http.StatusBadRequest, bad)
		return
	}
	switch {
	case m.method == "initialize":
		h.initialize(w, r, c, m)
	case isModern(r, m):
		h.serveModern(w, r, c, m)
	default:
		h.serveLegacy(w, r, c, m)
	}
}

// isModern: the request declares its version per request (_meta), or names
// a version in the header that no legacy revision has.
func isModern(r *http.Request, m *message) bool {
	if _, ok := metaOf(m.Params)[metaProtocolVersion]; ok {
		return true
	}
	v := r.Header.Get(headerVersion)
	return v != "" && !isLegacy(v)
}

func writeRPC(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func accepted(w http.ResponseWriter) { w.WriteHeader(http.StatusAccepted) }

// ---- legacy: initialize + sessions ----

type initializeParams struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"capabilities"`
	ClientInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"clientInfo"`
}

func (h *Handler) initialize(w http.ResponseWriter, r *http.Request, c *caller, m *message) {
	if m.isNotification() {
		writeRPC(w, http.StatusBadRequest, errorResponse(nil, newError(codeInvalidRequest, "Invalid Request: initialize must be a request")))
		return
	}
	var p initializeParams
	if e := decodeParams(m.Params, &p); e != nil {
		writeRPC(w, http.StatusOK, errorResponse(m.ID, e))
		return
	}
	if p.ProtocolVersion == "" {
		writeRPC(w, http.StatusOK, errorResponse(m.ID, newError(codeInvalidParams, "Invalid params: protocolVersion is required")))
		return
	}
	// Legacy negotiation: echo a supported revision, else offer our newest
	// legacy one and let the client decide.
	version := p.ProtocolVersion
	if !isLegacy(version) {
		version = Version20251125
	}
	ss := h.sessions.create(version, c.tokenKey, strings.TrimSpace(p.ClientInfo.Name+" "+p.ClientInfo.Version))
	server.Logger(r.Context()).Info("mcp session started", "user_id", c.UserID, "protocol", version, "client", ss.client)
	w.Header().Set(headerSession, ss.id)
	writeRPC(w, http.StatusOK, resultResponse(m.ID, map[string]any{
		"protocolVersion": version,
		"capabilities":    capabilities(),
		"serverInfo":      h.serverInfo(),
		"instructions":    instructions,
	}))
}

func (h *Handler) serverInfo() map[string]any {
	return map[string]any{"name": serverName, "title": serverTitle, "version": h.opt.Version}
}

func capabilities() map[string]any {
	return map[string]any{
		"tools":     map[string]any{"listChanged": false},
		"resources": map[string]any{"listChanged": false, "subscribe": false},
	}
}

// legacySession checks the session headers of a non-initialize request.
func (h *Handler) legacySession(r *http.Request, c *caller) (session, int, *rpcError) {
	id := r.Header.Get(headerSession)
	if id == "" {
		return session{}, http.StatusBadRequest, newError(codeInvalidRequest, "Bad Request: Mcp-Session-Id header is required (send initialize first)")
	}
	ss, ok := h.sessions.get(id, c.tokenKey)
	if !ok {
		return session{}, http.StatusNotFound, newError(codeInvalidRequest, "Session not found: initialize a new session")
	}
	if v := r.Header.Get(headerVersion); v != "" && v != ss.version {
		return session{}, http.StatusBadRequest, newError(codeInvalidRequest, "Bad Request: MCP-Protocol-Version "+v+" does not match the negotiated "+ss.version)
	}
	return ss, 0, nil
}

func (h *Handler) serveLegacy(w http.ResponseWriter, r *http.Request, c *caller, m *message) {
	ss, status, e := h.legacySession(r, c)
	if e != nil {
		writeRPC(w, status, errorResponse(m.ID, e))
		return
	}
	resp := h.legacyMessage(r, c, ss, m)
	if resp == nil {
		accepted(w)
		return
	}
	writeRPC(w, http.StatusOK, resp)
}

// legacyMessage handles one message inside a session; nil = no response
// (notification or client response).
func (h *Handler) legacyMessage(r *http.Request, c *caller, ss session, m *message) *response {
	if m.isResponse() {
		return nil // this server never sends requests; nothing to correlate
	}
	if m.isNotification() {
		if m.method == "notifications/initialized" {
			h.sessions.markInitialized(ss.id)
		}
		return nil
	}
	if m.method == "initialize" {
		return errorResponse(m.ID, newError(codeInvalidRequest, "Invalid Request: the session is already initialized"))
	}
	if m.method == "ping" {
		return resultResponse(m.ID, map[string]any{})
	}
	result, e := h.dispatch(r, c, ss.version, m)
	if e != nil {
		return errorResponse(m.ID, e)
	}
	return resultResponse(m.ID, result)
}

// serveBatch: JSON-RPC batches exist only in 2025-03-26.
func (h *Handler) serveBatch(w http.ResponseWriter, r *http.Request, c *caller, items []json.RawMessage) {
	if len(items) == 0 {
		writeRPC(w, http.StatusBadRequest, errorResponse(nil, newError(codeInvalidRequest, "Invalid Request: empty batch")))
		return
	}
	ss, status, e := h.legacySession(r, c)
	if e != nil {
		writeRPC(w, status, errorResponse(nil, e))
		return
	}
	if ss.version != Version20250326 {
		writeRPC(w, http.StatusBadRequest, errorResponse(nil, newError(codeInvalidRequest,
			"Invalid Request: JSON-RPC batches are only part of protocol 2025-03-26 (this session uses "+ss.version+")")))
		return
	}
	if len(items) > maxBatch {
		writeRPC(w, http.StatusBadRequest, errorResponse(nil, newError(codeInvalidRequest, "Invalid Request: at most 32 messages per batch")))
		return
	}
	out := []*response{}
	for _, raw := range items {
		m, bad := decodeMessage(raw)
		if bad != nil {
			out = append(out, bad)
			continue
		}
		if m.method == "initialize" {
			out = append(out, errorResponse(m.ID, newError(codeInvalidRequest, "Invalid Request: initialize must not be part of a batch")))
			continue
		}
		if resp := h.legacyMessage(r, c, ss, m); resp != nil {
			out = append(out, resp)
		}
	}
	if len(out) == 0 {
		accepted(w)
		return
	}
	writeRPC(w, http.StatusOK, out)
}

// ---- modern (2026-07-28): stateless, per-request metadata ----

func metaOf(params json.RawMessage) map[string]json.RawMessage {
	var p struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if len(params) > 0 {
		_ = json.Unmarshal(params, &p)
	}
	return p.Meta
}

func unsupportedVersion(requested string) *rpcError {
	return &rpcError{Code: codeUnsupportedVersion, Message: "Unsupported protocol version",
		Data: map[string]any{"supported": SupportedVersions, "requested": requested}}
}

func (h *Handler) serveModern(w http.ResponseWriter, r *http.Request, c *caller, m *message) {
	if m.isResponse() {
		writeRPC(w, http.StatusBadRequest, errorResponse(m.ID, newError(codeInvalidRequest, "Invalid Request: clients must not send responses")))
		return
	}
	meta := metaOf(m.Params)
	hdr := r.Header.Get(headerVersion)
	var version string
	if raw, ok := meta[metaProtocolVersion]; ok {
		_ = json.Unmarshal(raw, &version)
	}
	switch {
	case version == "" && hdr != Version20260728:
		writeRPC(w, http.StatusBadRequest, errorResponse(m.ID, unsupportedVersion(hdr)))
		return
	case version == "":
		writeRPC(w, http.StatusBadRequest, errorResponse(m.ID, newError(codeInvalidParams, "Invalid params: _meta."+metaProtocolVersion+" is required")))
		return
	case version != Version20260728:
		writeRPC(w, http.StatusBadRequest, errorResponse(m.ID, unsupportedVersion(version)))
		return
	}
	if m.isNotification() {
		accepted(w)
		return
	}
	if caps, ok := meta[metaClientCaps]; !ok || !strings.HasPrefix(strings.TrimSpace(string(caps)), "{") {
		writeRPC(w, http.StatusBadRequest, errorResponse(m.ID, newError(codeInvalidParams, "Invalid params: _meta."+metaClientCaps+" is required")))
		return
	}
	if e := checkHeaders(r, m, version); e != nil {
		writeRPC(w, http.StatusBadRequest, errorResponse(m.ID, e))
		return
	}
	var result map[string]any
	var e *rpcError
	switch m.method {
	case "server/discover":
		result = map[string]any{"supportedVersions": SupportedVersions, "capabilities": capabilities(), "instructions": instructions,
			"ttlMs": 300_000, "cacheScope": "public"}
	default:
		result, e = h.dispatch(r, c, version, m)
	}
	if e != nil {
		status := http.StatusOK
		if e.Code == codeMethodNotFound {
			status = http.StatusNotFound
		}
		writeRPC(w, status, errorResponse(m.ID, e))
		return
	}
	result["resultType"] = "complete"
	result["_meta"] = map[string]any{metaServerInfo: h.serverInfo()}
	writeRPC(w, http.StatusOK, resultResponse(m.ID, result))
}

// checkHeaders: the transport mirrors the version, method and name into
// headers; a missing or different value is a HeaderMismatch (-32020).
func checkHeaders(r *http.Request, m *message, version string) *rpcError {
	mismatch := func(msg string) *rpcError { return newError(codeHeaderMismatch, "Header mismatch: "+msg) }
	if got := r.Header.Get(headerVersion); got != version {
		return mismatch(headerVersion + " header " + quoteOrMissing(got) + " does not match _meta protocolVersion '" + version + "'")
	}
	if got := r.Header.Get(headerMethod); got != m.method {
		return mismatch(headerMethod + " header " + quoteOrMissing(got) + " does not match body method '" + m.method + "'")
	}
	var key string
	switch m.method {
	case "tools/call", "prompts/get":
		key = "name"
	case "resources/read":
		key = "uri"
	default:
		return nil
	}
	var p map[string]json.RawMessage
	_ = json.Unmarshal(m.Params, &p)
	var want string
	_ = json.Unmarshal(p[key], &want)
	raw := r.Header.Get(headerName)
	got, ok := decodeHeaderValue(raw)
	if raw == "" || !ok || got != want {
		return mismatch(headerName + " header " + quoteOrMissing(raw) + " does not match body " + key + " '" + want + "'")
	}
	return nil
}

func quoteOrMissing(v string) string {
	if v == "" {
		return "(missing)"
	}
	return "'" + v + "'"
}

// decodeHeaderValue undoes the `=?base64?…?=` sentinel encoding.
func decodeHeaderValue(v string) (string, bool) {
	if strings.HasPrefix(v, "=?base64?") && strings.HasSuffix(v, "?=") && len(v) >= len("=?base64??=") {
		b, err := base64.StdEncoding.DecodeString(v[len("=?base64?") : len(v)-2])
		if err != nil {
			return "", false
		}
		return string(b), true
	}
	for _, ch := range v {
		if ch < 0x20 && ch != '\t' || ch > 0x7e {
			return "", false
		}
	}
	return v, true
}

// ---- shared methods ----

// dispatch runs the methods both eras share. version selects era-specific
// shapes (error codes, structuredContent, cache hints).
func (h *Handler) dispatch(r *http.Request, c *caller, version string, m *message) (map[string]any, *rpcError) {
	modern := version == Version20260728
	switch m.method {
	case "tools/list":
		var p struct {
			Cursor string `json:"cursor"`
		}
		if e := decodeParams(m.Params, &p); e != nil {
			return nil, e
		}
		if p.Cursor != "" {
			return nil, newError(codeInvalidParams, "Invalid params: unknown cursor")
		}
		out := map[string]any{"tools": listTools(c)}
		if modern {
			out["ttlMs"], out["cacheScope"] = 300_000, "private"
		}
		return out, nil
	case "tools/call":
		return h.callTool(r, c, version, m)
	case "resources/list":
		out, e := h.listResources(r, c)
		if e == nil && modern {
			out["ttlMs"], out["cacheScope"] = 0, "private"
		}
		return out, e
	case "resources/templates/list":
		out := map[string]any{"resourceTemplates": resourceTemplates()}
		if modern {
			out["ttlMs"], out["cacheScope"] = 300_000, "public"
		}
		return out, nil
	case "resources/read":
		out, e := h.readResource(r, c, version, m)
		if e == nil && modern {
			out["ttlMs"], out["cacheScope"] = 0, "private"
		}
		return out, e
	}
	return nil, newError(codeMethodNotFound, "Method not found: "+m.method)
}

const instructions = `SimpleTickets is a help desk. Tickets have a number (shown as #1042) and an id; every tool that takes "ticket" accepts either. ` +
	`You act as the owner of the access token, with their permissions limited by the token's scopes: what you can see and do is exactly what the REST API allows them. ` +
	`Ticket details list "allowed_actions"; only attempt changes listed there. ` +
	`reply_to_ticket sends a public reply that the customer sees and is emailed; staff-only remarks belong in add_internal_note. ` +
	`Look up category, team and macro ids with list_categories, list_teams and the macro name before using them. Search the knowledge base (search_kb) before answering how-to questions.`
