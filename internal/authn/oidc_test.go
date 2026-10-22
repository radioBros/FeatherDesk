// SPDX-License-Identifier: AGPL-3.0-only

package authn_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/radioBros/FeatherDesk/internal/app"
	"github.com/radioBros/FeatherDesk/internal/authn"
	"github.com/radioBros/FeatherDesk/internal/authn/authntest"
	"github.com/radioBros/FeatherDesk/internal/directory"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
	"github.com/radioBros/FeatherDesk/internal/tickets"
)

// fakeOP is an in-process OpenID provider: discovery, JWKS, token endpoint
// with PKCE S256 verification, userinfo. Tokens are RS256-signed with a key
// generated per test (stdlib crypto only).
type fakeOP struct {
	t   *testing.T
	srv *httptest.Server
	// path is the issuer path ("" = the server root). Authentik issuers look
	// like /application/o/<slug>/ with a trailing slash.
	path string
	key  *rsa.PrivateKey
	mu   sync.Mutex
	// code → grant
	codes map[string]grant
}

type grant struct {
	claims    map[string]any
	challenge string
	clientID  string
}

var (
	opKeyOnce sync.Once
	opKey     *rsa.PrivateKey
)

func newFakeOP(t *testing.T) *fakeOP { return newFakeOPAt(t, "") }

// issuer is the OP's issuer URL, exactly as it appears in discovery and tokens.
func (op *fakeOP) issuer() string { return op.srv.URL + op.path }

// newFakeOPAt serves the OP under an issuer path (e.g. "/application/o/x/").
func newFakeOPAt(t *testing.T, path string) *fakeOP {
	opKeyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		opKey = k
	})
	op := &fakeOP{t: t, key: opKey, codes: map[string]grant{}, path: path}
	mux := http.NewServeMux()
	mux.HandleFunc(strings.TrimSuffix(path, "/")+"/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		u := op.srv.URL
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": op.issuer(), "authorization_endpoint": u + "/authorize", "token_endpoint": u + "/token", "jwks_uri": u + "/jwks",
			"userinfo_endpoint": u + "/userinfo", "end_session_endpoint": u + "/logout", "id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		pub := op.key.PublicKey
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
			"n": b64(pub.N.Bytes()), "e": b64(big.NewInt(int64(pub.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		op.mu.Lock()
		g, ok := op.codes[r.PostForm.Get("code")]
		delete(op.codes, r.PostForm.Get("code"))
		op.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || r.PostForm.Get("code_verifier") == "" || b64(sum[:]) != g.challenge {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-" + r.PostForm.Get("code"), "token_type": "Bearer",
			"expires_in": 300, "id_token": op.sign(g.claims)})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"sub": "nobody"})
	})
	op.srv = httptest.NewServer(mux)
	t.Cleanup(op.srv.Close)
	return op
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (op *fakeOP) sign(claims map[string]any) string {
	h, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	p, _ := json.Marshal(claims)
	in := b64(h) + "." + b64(p)
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, op.key, crypto.SHA256, sum[:])
	if err != nil {
		panic(err)
	}
	return in + "." + b64(sig)
}

func (op *fakeOP) env(extra map[string]string) map[string]string {
	env := map[string]string{
		"FD_OIDC_STAFF_ISSUER": op.issuer(), "FD_OIDC_STAFF_CLIENT_ID": "staff-client", "FD_OIDC_STAFF_CLIENT_SECRET": "staff-secret",
		"FD_OIDC_STAFF_ROLE_MAP": "fd-admins=admin,fd-supervisors=supervisor,fd-agents=agent", "FD_OIDC_STAFF_TIER_MAP": "fd-l2=2,fd-l3=3",
		"FD_OIDC_CUSTOMER_ISSUER": op.issuer(), "FD_OIDC_CUSTOMER_CLIENT_ID": "cust-client",
	}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

// claims builds standard ID token claims for client.
func (op *fakeOP) claims(client, sub, email string, groups ...string) map[string]any {
	now := time.Now()
	c := map[string]any{"iss": op.issuer(), "aud": client, "sub": sub, "email": email, "email_verified": true, "name": "Person " + sub,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix()}
	if groups != nil {
		c["groups"] = groups
	}
	return c
}

// flow runs start → (fake IdP) → callback. mutate may alter the grant.
type flow struct {
	aud      domain.Audience
	claims   map[string]any
	returnTo string
	mutate   func(g *grant, state *string)
	browser  bool // send the callback as a browser navigation (Accept: text/html)
}

func (op *fakeOP) login(t *testing.T, e *authntest.Env, c *authntest.Client, f flow) *http.Response {
	t.Helper()
	path := "/api/v1/auth/oidc/" + string(f.aud) + "/start"
	if f.returnTo != "" {
		path += "?redirect=" + url.QueryEscape(f.returnTo)
	}
	r, b := c.Do("GET", path, nil)
	if r.StatusCode != 302 {
		t.Fatalf("start: %d %s", r.StatusCode, b)
	}
	loc, _ := url.Parse(r.Header.Get("Location"))
	q := loc.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("nonce") == "" || q.Get("state") == "" {
		t.Fatalf("authorize URL lacks PKCE/nonce/state: %s", loc)
	}
	var stCookie string
	for _, ck := range r.Cookies() {
		if ck.Name == "fd_oidc" {
			stCookie = ck.Value
			if !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode {
				t.Fatalf("fd_oidc flags: %+v", ck)
			}
		}
	}
	claims := map[string]any{}
	for k, v := range f.claims {
		claims[k] = v
	}
	if _, ok := claims["nonce"]; !ok {
		claims["nonce"] = q.Get("nonce")
	}
	g := grant{claims: claims, challenge: q.Get("code_challenge"), clientID: q.Get("client_id")}
	state := q.Get("state")
	if f.mutate != nil {
		f.mutate(&g, &state)
	}
	code := "code-" + randHex()
	op.mu.Lock()
	op.codes[code] = g
	op.mu.Unlock()
	c.Header = http.Header{"Cookie": {"fd_oidc=" + stCookie}}
	if f.browser {
		c.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	}
	defer func() { c.Header = nil }()
	resp, _ := c.Do("GET", "/api/v1/auth/oidc/"+string(f.aud)+"/callback?code="+code+"&state="+url.QueryEscape(state), nil)
	return resp
}

func randHex() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func staffFlow(op *fakeOP, sub, email string, groups ...string) flow {
	return flow{aud: domain.AudienceStaff, claims: op.claims("staff-client", sub, email, groups...)}
}

func mustLogin(t *testing.T, op *fakeOP, e *authntest.Env, f flow) *authntest.Client {
	t.Helper()
	c := e.Client()
	r := op.login(t, e, c, f)
	if r.StatusCode != 302 || c.Cookie == "" {
		b := make([]byte, 512)
		n, _ := r.Body.Read(b)
		t.Fatalf("callback: %d %s", r.StatusCode, b[:n])
	}
	return c
}

func userBySub(t *testing.T, e *authntest.Env, sub string) (id, role string, tier int, status string, idp bool) {
	t.Helper()
	err := e.DB.Read().QueryRowContext(context.Background(), `SELECT u.id, u.role, COALESCE(u.tier, 0), u.status, u.idp_managed FROM users u
		JOIN user_identities i ON i.user_id = u.id WHERE i.subject = ?`, sub).Scan(&id, &role, &tier, &status, &idp)
	if err != nil {
		t.Fatalf("user for sub %s: %v", sub, err)
	}
	return
}

func TestOIDC_StaffLoginAndTierFromMap(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(nil)})
		c := e.Client()
		f := staffFlow(op, "sub-1", "agent1@corp.test", "fd-agents", "fd-l2", "unrelated")
		f.returnTo = "/agent/tickets?view=mine"
		r := op.login(t, e, c, f)
		if r.StatusCode != 302 || r.Header.Get("Location") != "/agent/tickets?view=mine" {
			t.Fatalf("callback: %d %s", r.StatusCode, r.Header.Get("Location"))
		}
		_, role, tier, status, idp := userBySub(t, e, "sub-1")
		if role != "agent" || tier != 2 || status != "active" || !idp {
			t.Fatalf("user: %s %d %s %v", role, tier, status, idp)
		}
		if r, b := c.Do("GET", "/api/v1/me", nil); r.StatusCode != 200 || authntest.JSON(t, b)["kind"] != "staff" {
			t.Fatalf("me: %d %s", r.StatusCode, b)
		}
	})
}

func TestRoleSync_TierFromMap(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(nil)})
		mustLogin(t, op, e, staffFlow(op, "s", "a@corp.test", "fd-agents", "fd-l2", "fd-l3"))
		if _, _, tier, _, _ := userBySub(t, e, "s"); tier != 3 {
			t.Fatalf("tier %d", tier)
		}
		mustLogin(t, op, e, staffFlow(op, "s", "a@corp.test", "fd-agents"))
		if _, _, tier, _, _ := userBySub(t, e, "s"); tier != 1 {
			t.Fatalf("default tier %d", tier)
		}
	})
}

func TestRoleSync_HighestWins(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(nil)})
		mustLogin(t, op, e, staffFlow(op, "s", "a@corp.test", "fd-agents", "fd-admins", "fd-supervisors"))
		if _, role, _, _, _ := userBySub(t, e, "s"); role != "admin" {
			t.Fatalf("role %s", role)
		}
	})
}

func TestRoleSync_DemoteOnGroupRemoval(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(nil)})
		mustLogin(t, op, e, staffFlow(op, "s", "a@corp.test", "fd-admins"))
		mustLogin(t, op, e, staffFlow(op, "s", "a@corp.test", "fd-agents"))
		id, role, _, _, _ := userBySub(t, e, "s")
		if role != "agent" {
			t.Fatalf("role %s", role)
		}
		if n := e.Int(`SELECT COUNT(*) FROM audit_log WHERE action = 'auth.role_synced' AND target_id = ?`, id); n != 1 {
			t.Fatalf("role_synced audits = %d", n)
		}
	})
}

func TestRoleSync_NoMappedGroupDisablesAndRefuses(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(nil)})
		first := mustLogin(t, op, e, staffFlow(op, "s", "a@corp.test", "fd-agents"))
		r := op.login(t, e, e.Client(), staffFlow(op, "s", "a@corp.test", "someone-else"))
		if r.StatusCode != 403 {
			t.Fatalf("no mapped group: %d", r.StatusCode)
		}
		if _, _, _, status, _ := userBySub(t, e, "s"); status != "disabled" {
			t.Fatalf("status %s", status)
		}
		if r, _ := first.Do("GET", "/api/v1/me", nil); r.StatusCode != 401 {
			t.Fatal("existing session survived")
		}
		// A brand-new user without a mapped group is not created.
		if r := op.login(t, e, e.Client(), staffFlow(op, "new", "new@corp.test")); r.StatusCode != 403 {
			t.Fatalf("new unmapped: %d", r.StatusCode)
		}
		if n := e.Int(`SELECT COUNT(*) FROM users WHERE email = 'new@corp.test'`); n != 0 {
			t.Fatal("unmapped user created")
		}
	})
}

// TestRoleSync_BrowserRefusalLandsOnLogin: a browser that is refused at the
// callback lands on /agent/login?error=<code> (FE-020 explains it) instead of a
// raw problem document; the user is still disabled (AUTH-006, AUTH-023 interop).
func TestRoleSync_BrowserRefusalLandsOnLogin(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(nil)})
		mustLogin(t, op, e, staffFlow(op, "s", "a@corp.test", "fd-agents"))
		f := staffFlow(op, "s", "a@corp.test", "someone-else")
		f.browser = true
		c := e.Client()
		r := op.login(t, e, c, f)
		if r.StatusCode != 302 || !strings.HasSuffix(r.Header.Get("Location"), "/agent/login?error=no_role") {
			t.Fatalf("no mapped group (browser): %d %q", r.StatusCode, r.Header.Get("Location"))
		}
		if c.Cookie != "" {
			t.Fatal("a refused login set a session cookie")
		}
		if _, _, _, status, _ := userBySub(t, e, "s"); status != "disabled" {
			t.Fatalf("status %s", status)
		}
		// Groups restored at the IdP: still refused, because the account is disabled.
		f = staffFlow(op, "s", "a@corp.test", "fd-agents")
		f.browser = true
		if r := op.login(t, e, e.Client(), f); r.StatusCode != 302 || !strings.HasSuffix(r.Header.Get("Location"), "/agent/login?error=disabled") {
			t.Fatalf("disabled (browser): %d %q", r.StatusCode, r.Header.Get("Location"))
		}
		// A broken callback (state mismatch) explains itself too.
		f.mutate = func(_ *grant, state *string) { *state = "forged" }
		if r := op.login(t, e, e.Client(), f); r.StatusCode != 302 || !strings.HasSuffix(r.Header.Get("Location"), "/agent/login?error=oidc_failed") {
			t.Fatalf("state mismatch (browser): %d %q", r.StatusCode, r.Header.Get("Location"))
		}
	})
}

// TestOIDC_IssuerTrailingSlashKept: Authentik issuers end in "/"
// (https://auth.example.com/application/o/<slug>/). The configured issuer is
// used verbatim, so discovery's exact issuer comparison succeeds and the
// staff login completes (regression: trimming the slash made /start 503).
func TestOIDC_IssuerTrailingSlashKept(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOPAt(t, "/application/o/featherdesk-staff/")
		e := authntest.New(t, db, authntest.Options{Env: op.env(nil)})
		c := e.Client()
		r := op.login(t, e, c, staffFlow(op, "sub-slash", "slash@corp.test", "fd-agents"))
		if r.StatusCode != 302 {
			t.Fatalf("callback with a trailing-slash issuer: %d", r.StatusCode)
		}
		if r, b := c.Do("GET", "/api/v1/me", nil); r.StatusCode != 200 || authntest.JSON(t, b)["kind"] != "staff" {
			t.Fatalf("me: %d %s", r.StatusCode, b)
		}
		if n := e.Int(`SELECT COUNT(*) FROM user_identities WHERE issuer = ? AND subject = 'sub-slash'`, op.issuer()); n != 1 {
			t.Fatalf("identity not stored under the exact issuer %q", op.issuer())
		}
	})
}

func TestOIDC_StateMismatchRejected(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(nil)})
		f := staffFlow(op, "s", "a@corp.test", "fd-agents")
		f.mutate = func(_ *grant, state *string) { *state = "forged-state" }
		if r := op.login(t, e, e.Client(), f); r.StatusCode != 403 {
			t.Fatalf("state mismatch: %d", r.StatusCode)
		}
		// Callback without the fd_oidc cookie at all.
		c := e.Client()
		if r, _ := c.Do("GET", "/api/v1/auth/oidc/staff/callback?code=x&state=y", nil); r.StatusCode != 403 {
			t.Fatalf("no state cookie: %d", r.StatusCode)
		}
	})
}

func TestOIDC_NonceMismatchRejected(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(nil)})
		f := staffFlow(op, "s", "a@corp.test", "fd-agents")
		f.claims["nonce"] = "replayed-nonce"
		if r := op.login(t, e, e.Client(), f); r.StatusCode != 403 {
			t.Fatalf("nonce mismatch: %d", r.StatusCode)
		}
		if n := e.Int(`SELECT COUNT(*) FROM users`); n != 0 {
			t.Fatal("user created")
		}
	})
}

func TestOIDC_PKCERequired(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(nil)})
		f := staffFlow(op, "s", "a@corp.test", "fd-agents")
		f.mutate = func(g *grant, _ *string) { g.challenge = b64([]byte("challenge-of-another-transaction-000")) }
		if r := op.login(t, e, e.Client(), f); r.StatusCode != 403 {
			t.Fatalf("verifier mismatch: %d", r.StatusCode)
		}
	})
}

func TestOIDC_WrongAudienceRejected(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(nil)})
		f := staffFlow(op, "s", "a@corp.test", "fd-agents")
		f.claims["aud"] = "cust-client" // a customer-client token on the staff callback
		if r := op.login(t, e, e.Client(), f); r.StatusCode != 403 {
			t.Fatalf("wrong aud: %d", r.StatusCode)
		}
		f = staffFlow(op, "s", "a@corp.test", "fd-agents")
		f.claims["aud"] = []string{"staff-client", "other"}
		f.claims["azp"] = "other"
		if r := op.login(t, e, e.Client(), f); r.StatusCode != 403 {
			t.Fatalf("wrong azp: %d", r.StatusCode)
		}
	})
}

func TestOIDC_NoLinkToCustomerByEmail(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(map[string]string{"FD_OIDC_LINK_BY_EMAIL": "true"})})
		cust := e.Customer("", domain.RoleRequester)
		if r := op.login(t, e, e.Client(), staffFlow(op, "s", cust.Email, "fd-admins")); r.StatusCode != 403 {
			t.Fatalf("link staff identity to a customer: %d", r.StatusCode)
		}
		if n := e.Int(`SELECT COUNT(*) FROM user_identities`); n != 0 {
			t.Fatal("identity linked across kinds")
		}
		if k := e.Str(`SELECT kind FROM users WHERE id = ?`, cust.ID); k != "customer" {
			t.Fatal("kind changed")
		}
	})
}

func TestOIDC_NoLinkToBreakGlass(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(map[string]string{"FD_OIDC_LINK_BY_EMAIL": "true"})})
		bg := e.StaffUser(domain.RoleAdmin, 3, true)
		if r := op.login(t, e, e.Client(), staffFlow(op, "s", bg.Email, "fd-admins")); r.StatusCode != 403 {
			t.Fatalf("link to break-glass: %d", r.StatusCode)
		}
		if n := e.Int(`SELECT COUNT(*) FROM user_identities WHERE user_id = ?`, bg.ID); n != 0 {
			t.Fatal("break-glass linked")
		}
	})
}

func TestOIDC_LinkByEmailOnlyWhenEnabledAndVerified(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		off := authntest.New(t, db, authntest.Options{Env: op.env(nil)})
		agent := off.StaffUser(domain.RoleAgent, 1, false)
		if r := op.login(t, off, off.Client(), staffFlow(op, "s", agent.Email, "fd-agents")); r.StatusCode != 403 {
			t.Fatalf("link with flag off: %d", r.StatusCode)
		}
		on := authntest.New(t, db, authntest.Options{Env: op.env(map[string]string{"FD_OIDC_LINK_BY_EMAIL": "true"})})
		f := staffFlow(op, "s", agent.Email, "fd-agents")
		f.claims["email_verified"] = false
		if r := op.login(t, on, on.Client(), f); r.StatusCode != 403 {
			t.Fatalf("link unverified email: %d", r.StatusCode)
		}
		mustLogin(t, op, on, staffFlow(op, "s", agent.Email, "fd-agents"))
		if id, _, _, _, idp := userBySub(t, on, "s"); id != agent.ID || !idp {
			t.Fatal("not linked to the existing staff user")
		}
	})
}

func TestOIDC_IdPDownBootStillReady(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		dead := httptest.NewServer(http.NotFoundHandler())
		url := dead.URL
		dead.Close()
		e := authntest.New(t, db, authntest.Options{Env: map[string]string{
			"FD_OIDC_STAFF_ISSUER": url, "FD_OIDC_STAFF_CLIENT_ID": "x", "FD_OIDC_STAFF_ROLE_MAP": "g=agent",
			"FD_BOOTSTRAP_ADMIN_EMAIL": "boot@example.test", "FD_BOOTSTRAP_ADMIN_PASSWORD": "bootstrap-pass-99"}})
		rec := httptest.NewRecorder()
		e.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
		if rec.Code != 200 {
			t.Fatalf("readyz with IdP down: %d %s", rec.Code, rec.Body)
		}
		c := e.Client()
		if r, _ := c.Do("GET", "/api/v1/auth/oidc/staff/start", nil); r.StatusCode != 503 {
			t.Fatalf("start with IdP down: %d", r.StatusCode)
		}
		if r, _ := c.Do("GET", "/api/v1/auth/oidc/customer/start", nil); r.StatusCode != 404 {
			t.Fatalf("unconfigured audience: %d", r.StatusCode)
		}
		admin := e.Client()
		if r, b := login(admin, domain.AudienceStaff, "boot@example.test", "bootstrap-pass-99"); r.StatusCode != 200 {
			t.Fatalf("break-glass login during IdP outage: %d %s", r.StatusCode, b)
		}
		_, b := admin.Do("GET", "/api/v1/admin/system", nil)
		if !strings.Contains(string(b), `"oidc"`) || !strings.Contains(string(b), `"error"`) {
			t.Fatalf("/admin/system lacks the OIDC status: %s", b)
		}
	})
}

func TestOIDCCustomer_NeverCreatesStaff(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(nil)})
		org := storetest.CreateOrg(t, e.DB, "")
		e.Exec(`UPDATE organizations SET email_domains = ? WHERE id = ?`, `["cust.test"]`, org)
		f := flow{aud: domain.AudienceCustomer, claims: op.claims("cust-client", "c1", "jo@cust.test", "fd-admins")}
		c := mustLogin(t, op, e, f)
		_ = c
		var kind, role, orgID string
		_ = e.DB.Read().QueryRowContext(context.Background(), `SELECT kind, role, COALESCE(organization_id, '') FROM users WHERE email = 'jo@cust.test'`).Scan(&kind, &role, &orgID)
		if kind != "customer" || role != "requester" || orgID != org {
			t.Fatalf("customer OIDC created %s/%s org=%q", kind, role, orgID)
		}
	})
}

func TestOIDCCustomer_NoLinkToStaffByEmail(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(map[string]string{"FD_OIDC_LINK_BY_EMAIL": "true"})})
		agent := e.StaffUser(domain.RoleAgent, 1, false)
		f := flow{aud: domain.AudienceCustomer, claims: op.claims("cust-client", "c1", agent.Email)}
		if r := op.login(t, e, e.Client(), f); r.StatusCode != 403 {
			t.Fatalf("customer identity linked to staff: %d", r.StatusCode)
		}
	})
}

func TestTeamSync_AddRemove(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		billing := storetest.CreateTeam(t, db, "Billing")
		tech := storetest.CreateTeam(t, db, "Technical")
		e := authntest.New(t, db, authntest.Options{Env: op.env(map[string]string{"FD_OIDC_STAFF_TEAM_MAP": "fd-billing=billing,fd-tech=" + tech})})
		mustLogin(t, op, e, staffFlow(op, "s", "a@corp.test", "fd-agents", "fd-billing"))
		id, _, _, _, _ := userBySub(t, e, "s")
		if n := e.Int(`SELECT COUNT(*) FROM team_members WHERE user_id = ? AND team_id = ? AND source = 'idp'`, id, billing); n != 1 {
			t.Fatal("billing membership not added")
		}
		c := mustLogin(t, op, e, staffFlow(op, "s", "a@corp.test", "fd-agents", "fd-tech"))
		if n := e.Int(`SELECT COUNT(*) FROM team_members WHERE user_id = ?`, id); n != 1 ||
			e.Int(`SELECT COUNT(*) FROM team_members WHERE user_id = ? AND team_id = ?`, id, tech) != 1 {
			t.Fatal("memberships not mirrored")
		}
		_, b := c.Do("GET", "/api/v1/me", nil)
		if ids := authntest.JSON(t, b)["team_ids"].([]any); len(ids) != 1 || ids[0] != tech {
			t.Fatalf("principal teams: %v", ids)
		}
	})
}

// TestTeamSync_RemovalUnassignsInvalidTickets: losing an IdP team at login
// re-validates M-4 through directory.MembershipChanged (wired by
// app/modules.go): tickets of that team assigned to the user are unassigned
// with a ticket.assigned event (AUTH-031 → TKT-017).
func TestTeamSync_RemovalUnassignsInvalidTickets(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		authn.SetMembershipChanged(directory.MembershipChanged)
		t.Cleanup(func() { authn.SetMembershipChanged(nil) })
		op := newFakeOP(t)
		billing := storetest.CreateTeam(t, db, "Billing")
		tech := storetest.CreateTeam(t, db, "Technical")
		ticketsModule := func(d *app.Deps) (app.Module, error) {
			return tickets.New(tickets.Deps{DB: d.DB, Log: d.Log, Queue: d.Queue, Hub: d.Hub, Blob: d.Blob, Settings: d.Settings, Secrets: d.Secrets, Server: d.Server})
		}
		e := authntest.New(t, db, authntest.Options{Env: op.env(map[string]string{"FD_OIDC_STAFF_TEAM_MAP": "fd-billing=billing,fd-tech=" + tech}),
			Extra: []app.Factory{ticketsModule}})
		mustLogin(t, op, e, staffFlow(op, "s", "a@corp.test", "fd-agents", "fd-billing", "fd-tech"))
		id, _, _, _, _ := userBySub(t, e, "s")
		cust := storetest.CreateCustomer(t, db, "")
		inBilling := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, TeamID: billing, Tier: 1, AssigneeID: id, Status: domain.StatusOpen})
		inTech := storetest.CreateTicket(t, db, storetest.Ticket{Requester: cust, TeamID: tech, Tier: 1, AssigneeID: id, Status: domain.StatusOpen})
		mustLogin(t, op, e, staffFlow(op, "s", "a@corp.test", "fd-agents", "fd-tech"))
		if n := e.Int(`SELECT COUNT(*) FROM tickets WHERE id = ? AND assignee_id IS NULL`, inBilling); n != 1 {
			t.Fatal("ticket of the lost team still assigned")
		}
		if n := e.Int(`SELECT COUNT(*) FROM ticket_events WHERE ticket_id = ? AND type = 'ticket.assigned'`, inBilling); n != 1 {
			t.Fatalf("unassign events: %d", n)
		}
		if n := e.Int(`SELECT COUNT(*) FROM tickets WHERE id = ? AND assignee_id = ?`, inTech, id); n != 1 {
			t.Fatal("ticket of a kept team was unassigned")
		}
	})
}

func TestTeamSync_LocalMembershipsUntouched(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		billing := storetest.CreateTeam(t, db, "Billing")
		local := storetest.CreateTeam(t, db, "Local Team")
		e := authntest.New(t, db, authntest.Options{Env: op.env(map[string]string{"FD_OIDC_STAFF_TEAM_MAP": "fd-billing=Billing"})})
		mustLogin(t, op, e, staffFlow(op, "s", "a@corp.test", "fd-agents", "fd-billing"))
		id, _, _, _, _ := userBySub(t, e, "s")
		e.Exec(`INSERT INTO team_members (team_id, user_id, source, created_at) VALUES (?, ?, 'local', ?)`, local, id, store.Now())
		mustLogin(t, op, e, staffFlow(op, "s", "a@corp.test", "fd-agents"))
		if e.Int(`SELECT COUNT(*) FROM team_members WHERE user_id = ? AND team_id = ?`, id, local) != 1 {
			t.Fatal("local membership removed")
		}
		if e.Int(`SELECT COUNT(*) FROM team_members WHERE user_id = ? AND team_id = ?`, id, billing) != 0 {
			t.Fatal("idp membership kept after group removal")
		}
	})
}

func TestTeamSync_UnknownSlugBootError(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		tb := &fatalCatcher{TB: t}
		func() {
			defer func() { _ = recover() }()
			authntest.New(tb, db, authntest.Options{Env: op.env(map[string]string{"FD_OIDC_STAFF_TEAM_MAP": "fd-x=no-such-team"})})
		}()
		if !strings.Contains(tb.msg, "FD_OIDC_STAFF_TEAM_MAP") || !strings.Contains(tb.msg, "no-such-team") {
			t.Fatalf("expected boot error, got %q", tb.msg)
		}
	})
}

func TestOIDC_RoleMapValidatedAtBoot(t *testing.T) {
	db := storetest.Open(t, store.SQLite)
	op := newFakeOP(t)
	tb := &fatalCatcher{TB: t}
	func() {
		defer func() { _ = recover() }()
		authntest.New(tb, db, authntest.Options{Env: op.env(map[string]string{"FD_OIDC_STAFF_ROLE_MAP": "g=superuser"})})
	}()
	if !strings.Contains(tb.msg, "ROLE_MAP") {
		t.Fatalf("expected role map error, got %q", tb.msg)
	}
}

func TestLogout_OIDCReturnsEndSessionURL(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(nil)})
		c := mustLogin(t, op, e, staffFlow(op, "s", "a@corp.test", "fd-agents"))
		_, b := c.Do("GET", "/api/v1/me", nil) // picks up the CSRF token
		_ = b
		r, b := c.Do("POST", "/api/v1/auth/logout", nil)
		if r.StatusCode != 200 {
			t.Fatalf("logout: %d %s", r.StatusCode, b)
		}
		u, _ := url.Parse(authntest.JSON(t, b)["redirect_url"].(string))
		if u.Scheme+"://"+u.Host+u.Path != op.srv.URL+"/logout" || u.Query().Get("id_token_hint") == "" ||
			u.Query().Get("post_logout_redirect_uri") != authntest.PublicURL+"/agent/login?logged_out=1" {
			t.Fatalf("end-session URL: %s", u)
		}
	})
}

// ---- back-channel logout (AUTH-030) ----

func (op *fakeOP) logoutToken(client, sub, sid string, mod func(map[string]any)) string {
	now := time.Now()
	c := map[string]any{"iss": op.issuer(), "aud": client, "iat": now.Unix(), "exp": now.Add(2 * time.Minute).Unix(), "jti": randHex(),
		"events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}}}
	if sub != "" {
		c["sub"] = sub
	}
	if sid != "" {
		c["sid"] = sid
	}
	if mod != nil {
		mod(c)
	}
	return op.sign(c)
}

func backchannel(e *authntest.Env, tok string) int {
	c := e.Client()
	c.Origin = "-"
	r, _ := c.Do("POST", "/api/v1/auth/oidc/staff/backchannel-logout", "logout_token="+url.QueryEscape(tok))
	return r.StatusCode
}

func sidFlow(op *fakeOP, sid string) flow {
	f := staffFlow(op, "s", "a@corp.test", "fd-agents")
	f.claims["sid"] = sid
	return f
}

func alive(c *authntest.Client) bool {
	r, _ := c.Do("GET", "/api/v1/me", nil)
	return r.StatusCode == 200
}

func TestBackchannel_RevokesBySid(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(nil)})
		a := mustLogin(t, op, e, sidFlow(op, "sid-a"))
		b := mustLogin(t, op, e, sidFlow(op, "sid-b"))
		if code := backchannel(e, op.logoutToken("staff-client", "s", "sid-a", nil)); code != 200 {
			t.Fatalf("backchannel: %d", code)
		}
		if alive(a) || !alive(b) {
			t.Fatalf("sid revocation: a=%v b=%v", alive(a), alive(b))
		}
	})
}

func TestBackchannel_RevokesAllBySubWhenNoSid(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(nil)})
		a := mustLogin(t, op, e, sidFlow(op, "sid-a"))
		b := mustLogin(t, op, e, sidFlow(op, "sid-b"))
		other := mustLogin(t, op, e, staffFlow(op, "other", "o@corp.test", "fd-agents"))
		if code := backchannel(e, op.logoutToken("staff-client", "s", "", nil)); code != 200 {
			t.Fatalf("backchannel: %d", code)
		}
		if alive(a) || alive(b) || !alive(other) {
			t.Fatal("sub revocation wrong")
		}
	})
}

func TestBackchannel_RejectsNonce400(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(nil)})
		a := mustLogin(t, op, e, sidFlow(op, "sid-a"))
		tok := op.logoutToken("staff-client", "s", "", func(c map[string]any) { c["nonce"] = "n" })
		if code := backchannel(e, tok); code != 400 {
			t.Fatalf("nonce: %d", code)
		}
		if code := backchannel(e, op.logoutToken("staff-client", "s", "", func(c map[string]any) { delete(c, "events") })); code != 400 {
			t.Fatalf("no event: %d", code)
		}
		if code := backchannel(e, op.logoutToken("staff-client", "s", "", func(c map[string]any) { c["iat"] = time.Now().Add(-10 * time.Minute).Unix() })); code != 400 {
			t.Fatalf("old iat: %d", code)
		}
		if !alive(a) {
			t.Fatal("session revoked by a rejected token")
		}
	})
}

func TestBackchannel_RejectsWrongAud400(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(nil)})
		a := mustLogin(t, op, e, sidFlow(op, "sid-a"))
		if code := backchannel(e, op.logoutToken("cust-client", "s", "", nil)); code != 400 {
			t.Fatalf("wrong aud: %d", code)
		}
		if !alive(a) {
			t.Fatal("revoked")
		}
	})
}

func TestBackchannel_ReplayJtiIgnored(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(nil)})
		mustLogin(t, op, e, sidFlow(op, "sid-a"))
		tok := op.logoutToken("staff-client", "s", "", nil)
		if code := backchannel(e, tok); code != 200 {
			t.Fatal("first")
		}
		again := mustLogin(t, op, e, sidFlow(op, "sid-c"))
		if code := backchannel(e, tok); code != 200 {
			t.Fatal("replay status")
		}
		if !alive(again) {
			t.Fatal("replayed logout token revoked a new session")
		}
	})
}

func TestBackchannel_ClosesSockets4401(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		op := newFakeOP(t)
		e := authntest.New(t, db, authntest.Options{Env: op.env(nil)})
		srv := httptest.NewServer(e.Handler())
		defer srv.Close()
		a := mustLogin(t, op, e, sidFlow(op, "sid-a"))
		ws, _, err := dialWS(t, srv, cookieHeader(e, a, e.Origin), "")
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			_ = ws.CloseNow() // safety net: the server usually closed it already
		}()
		backchannel(e, op.logoutToken("staff-client", "s", "sid-a", nil))
		if code := waitClose(t, ws, 15*time.Second); code != 4401 {
			t.Fatalf("close code %d", code)
		}
	})
}
