// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/server"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Mount registers the email admin API and the staff signature routes.
func (m *Module) Mount(g server.Groups) {
	g.Admin.Get("/email", m.getEmail)
	g.Admin.Put("/email", m.putEmail)
	g.Admin.Post("/email/test", m.testEmail)
	g.Admin.Get("/email/templates/{key}/preview", m.preview)
	g.Authed.Get("/me/signature", m.getSignature)
	g.Authed.Put("/me/signature", m.putSignature)
	m.mountInApp(g)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ---- DTOs (OpenAPI EmailSettings) ----

type senderDTO struct {
	FromName    string  `json:"from_name"`
	FromAddress string  `json:"from_address"`
	ReplyTo     *string `json:"reply_to"`
}

func senderOut(id settings.Identity) *senderDTO {
	if strings.TrimSpace(id.Address) == "" {
		return nil // SenderIdentity requires a valid from_address
	}
	d := &senderDTO{FromName: id.Name, FromAddress: id.Address}
	if id.ReplyTo != "" {
		rt := id.ReplyTo
		d.ReplyTo = &rt
	}
	return d
}

func (m *Module) emailDoc(ctx context.Context) (map[string]any, error) {
	sv, err := m.set.SMTP(ctx)
	if err != nil {
		return nil, err
	}
	ev, err := m.set.Email(ctx)
	if err != nil {
		return nil, err
	}
	s := sv.SMTP
	smtp := map[string]any{"host": s.Host, "port": s.Port, "security": s.Security, "username": s.Username, "password_set": sv.PasswordSet,
		"auth": s.Auth, "helo": s.HELO, "timeout_seconds": s.TimeoutSeconds, "allow_insecure_tls": s.AllowInsecureTLS}
	if s.Port < 1 || s.Port > 65535 {
		delete(smtp, "port")
	}
	if s.TimeoutSeconds < 1 || s.TimeoutSeconds > 120 {
		delete(smtp, "timeout_seconds")
	}
	senders := map[string]any{}
	if d := senderOut(ev.CustomerFrom); d != nil {
		senders["customer"] = d
	}
	if d := senderOut(ev.StaffFrom); d != nil {
		senders["staff"] = d
	}
	enabled := map[string]bool{}
	for _, k := range TemplateKeys {
		enabled[k] = Transactional(k) || !slices.Contains(ev.DisabledTemplates, k)
	}
	var triage any
	if ev.TriageMailbox != "" {
		triage = ev.TriageMailbox
	}
	locked := []string{}
	for _, f := range sv.LockedByEnv {
		locked = append(locked, "smtp."+f)
	}
	for _, f := range ev.LockedByEnv {
		locked = append(locked, "senders."+strings.TrimSuffix(f, "_from"))
	}
	blocklist := ev.InboundBlocklist
	if blocklist == nil {
		blocklist = []string{}
	}
	cfg, _ := m.smtpConfig(ctx)
	var lastErr any
	if le := m.LastError(); le != "" {
		lastErr = le
	}
	return map[string]any{
		"smtp": smtp, "senders": senders, "templates_enabled": enabled, "triage_mailbox": triage,
		"plus_addressing": ev.PlusAddressing, "inbound_blocklist": blocklist, "locked_by_env": locked,
		"status": map[string]any{"enabled": cfg.Configured(), "last_error": lastErr},
	}, nil
}

func (m *Module) getEmail(w http.ResponseWriter, r *http.Request) {
	doc, err := m.emailDoc(r.Context())
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

type smtpIn struct {
	Host             *string `json:"host"`
	Port             *int    `json:"port"`
	Security         *string `json:"security"`
	Username         *string `json:"username"`
	Password         *string `json:"password"`
	Auth             *string `json:"auth"`
	HELO             *string `json:"helo"`
	TimeoutSeconds   *int    `json:"timeout_seconds"`
	AllowInsecureTLS *bool   `json:"allow_insecure_tls"`
}

type emailIn struct {
	SMTP    *smtpIn `json:"smtp"`
	Senders *struct {
		Customer *senderDTO `json:"customer"`
		Staff    *senderDTO `json:"staff"`
	} `json:"senders"`
	TemplatesEnabled map[string]bool `json:"templates_enabled"`
	TriageMailbox    json.RawMessage `json:"triage_mailbox"`
	PlusAddressing   *bool           `json:"plus_addressing"`
	InboundBlocklist *[]string       `json:"inbound_blocklist"`
}

// maxBlocklist bounds settings.email.inbound_blocklist (NOT-028).
const maxBlocklist = 500

var blockDomainRe = regexp.MustCompile(`^@?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// normalizeBlocklist validates entries the mailin pipeline matches: a full
// address, "@domain" or "domain" (subdomains match). Lower-cased, deduplicated
// and sorted.
func normalizeBlocklist(in []string) ([]string, []problems.FieldError) {
	if len(in) > maxBlocklist {
		return nil, []problems.FieldError{problems.Field("inbound_blocklist", "max_items", "at most 500 entries")}
	}
	var fe []problems.FieldError
	out := []string{}
	for i, e := range in {
		e = strings.ToLower(strings.TrimSpace(e))
		ok := blockDomainRe.MatchString(e)
		if !ok && strings.Contains(e, "@") && !strings.HasPrefix(e, "@") {
			ok = validAddr(e)
		}
		if !ok {
			fe = append(fe, problems.Field(fmt.Sprintf("inbound_blocklist[%d]", i), "invalid", "an email address, @domain or domain"))
			continue
		}
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	slices.Sort(out)
	return out, fe
}

func validAddr(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == strings.TrimSpace(s)
}

// applySMTP overlays the submitted fields on dst and validates them. locked
// lists env-locked json names; a locked field submitted with a value that
// differs from the effective one is a 422 locked_by_env (equal echoes of the
// read-only value are ignored, as the OpenAPI description says).
func applySMTP(in *smtpIn, dst *settings.SMTP, eff settings.SMTP, locked []string) (changed []string, fe []problems.FieldError) {
	isLocked := func(f string) bool { return slices.Contains(locked, f) }
	str := func(name string, v *string, cur *string, effv string) {
		if v == nil {
			return
		}
		if isLocked(name) {
			if *v != effv {
				fe = append(fe, problems.Field("smtp."+name, "locked_by_env", "set by environment"))
			}
			return
		}
		if *cur != *v {
			changed = append(changed, "smtp."+name)
		}
		*cur = *v
	}
	str("host", in.Host, &dst.Host, eff.Host)
	str("username", in.Username, &dst.Username, eff.Username)
	str("helo", in.HELO, &dst.HELO, eff.HELO)
	if in.Security != nil && !slices.Contains([]string{"none", "starttls", "tls"}, *in.Security) {
		fe = append(fe, problems.Field("smtp.security", "enum", "none, starttls or tls"))
	} else {
		str("security", in.Security, &dst.Security, eff.Security)
	}
	if in.Auth != nil && !slices.Contains([]string{"none", "plain", "login", "cram-md5"}, *in.Auth) {
		fe = append(fe, problems.Field("smtp.auth", "enum", "none, plain, login or cram-md5"))
	} else {
		str("auth", in.Auth, &dst.Auth, eff.Auth)
	}
	num := func(name string, v *int, cur *int, effv, lo, hi int) {
		if v == nil {
			return
		}
		if isLocked(name) {
			if *v != effv {
				fe = append(fe, problems.Field("smtp."+name, "locked_by_env", "set by environment"))
			}
			return
		}
		if *v < lo || *v > hi {
			fe = append(fe, problems.Field("smtp."+name, "range", fmt.Sprintf("%d..%d", lo, hi)))
			return
		}
		if *cur != *v {
			changed = append(changed, "smtp."+name)
		}
		*cur = *v
	}
	num("port", in.Port, &dst.Port, eff.Port, 1, 65535)
	num("timeout_seconds", in.TimeoutSeconds, &dst.TimeoutSeconds, eff.TimeoutSeconds, 1, 120)
	if in.AllowInsecureTLS != nil {
		if isLocked("allow_insecure_tls") {
			if *in.AllowInsecureTLS != eff.AllowInsecureTLS {
				fe = append(fe, problems.Field("smtp.allow_insecure_tls", "locked_by_env", "set by environment"))
			}
		} else {
			if dst.AllowInsecureTLS != *in.AllowInsecureTLS {
				changed = append(changed, "smtp.allow_insecure_tls")
			}
			dst.AllowInsecureTLS = *in.AllowInsecureTLS
		}
	}
	if in.Password != nil && *in.Password != "" && isLocked("password") {
		fe = append(fe, problems.Field("smtp.password", "locked_by_env", "set by environment"))
	}
	return changed, fe
}

func identityIn(field string, d *senderDTO, dst *settings.Identity) []problems.FieldError {
	var fe []problems.FieldError
	if !validAddr(d.FromAddress) {
		fe = append(fe, problems.Field(field+".from_address", "email", "a valid email address"))
	}
	rt := ""
	if d.ReplyTo != nil {
		rt = strings.TrimSpace(*d.ReplyTo)
	}
	if rt != "" && !validAddr(rt) {
		fe = append(fe, problems.Field(field+".reply_to", "email", "a valid email address"))
	}
	*dst = settings.Identity{Name: strings.TrimSpace(d.FromName), Address: strings.TrimSpace(d.FromAddress), ReplyTo: rt}
	return fe
}

func (m *Module) putEmail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in emailIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		problems.Write(w, r, problems.BadRequest("invalid JSON body"))
		return
	}
	sv, err := m.set.SMTP(ctx)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	ev, err := m.set.Email(ctx)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	stored := settings.Email{DisabledTemplates: []string{}, InboundBlocklist: []string{}}
	if err := m.set.Get(ctx, settings.KeyEmail, &stored); err != nil {
		problems.Write(w, r, err)
		return
	}
	var fe []problems.FieldError
	var changed []string
	nextSMTP := sv.SMTP
	var newPW *string
	if in.SMTP != nil {
		c, f := applySMTP(in.SMTP, &nextSMTP, sv.SMTP, sv.LockedByEnv)
		changed, fe = append(changed, c...), append(fe, f...)
		if in.SMTP.Password != nil && *in.SMTP.Password != "" && !slices.Contains(sv.LockedByEnv, "password") {
			newPW = in.SMTP.Password
			changed = append(changed, "smtp.password")
		}
	}
	next := stored
	if in.Senders != nil {
		for _, s := range []struct {
			field, lock string
			in          *senderDTO
			dst         *settings.Identity
			eff         settings.Identity
		}{{"senders.customer", "customer_from", in.Senders.Customer, &next.CustomerFrom, ev.CustomerFrom},
			{"senders.staff", "staff_from", in.Senders.Staff, &next.StaffFrom, ev.StaffFrom}} {
			if s.in == nil {
				continue
			}
			if slices.Contains(ev.LockedByEnv, s.lock) {
				if !strings.EqualFold(strings.TrimSpace(s.in.FromAddress), s.eff.Address) {
					fe = append(fe, problems.Field(s.field, "locked_by_env", "set by environment"))
				}
				continue
			}
			before := *s.dst
			fe = append(fe, identityIn(s.field, s.in, s.dst)...)
			if before != *s.dst {
				changed = append(changed, s.field)
			}
		}
	}
	if in.TemplatesEnabled != nil {
		disabled := slices.Clone(next.DisabledTemplates)
		for k, on := range in.TemplatesEnabled {
			switch {
			case !slices.Contains(TemplateKeys, k):
				fe = append(fe, problems.Field("templates_enabled."+k, "invalid", "unknown template key"))
			case Transactional(k) && !on:
				fe = append(fe, problems.Field("templates_enabled."+k, "not_optional", "auth emails cannot be disabled"))
			case on:
				disabled = slices.DeleteFunc(disabled, func(s string) bool { return s == k })
			case !slices.Contains(disabled, k):
				disabled = append(disabled, k)
			}
		}
		slices.Sort(disabled)
		if !slices.Equal(disabled, next.DisabledTemplates) {
			changed = append(changed, "templates_enabled")
		}
		next.DisabledTemplates = disabled
	}
	if len(in.TriageMailbox) > 0 {
		var tm *string
		if err := json.Unmarshal(in.TriageMailbox, &tm); err != nil {
			fe = append(fe, problems.Field("triage_mailbox", "email", "a valid email address"))
		} else {
			v := ""
			if tm != nil {
				v = strings.TrimSpace(*tm)
			}
			if v != "" && !validAddr(v) {
				fe = append(fe, problems.Field("triage_mailbox", "email", "a valid email address"))
			} else if v != next.TriageMailbox {
				next.TriageMailbox = v
				changed = append(changed, "triage_mailbox")
			}
		}
	}
	if in.PlusAddressing != nil && *in.PlusAddressing != next.PlusAddressing {
		next.PlusAddressing = *in.PlusAddressing
		changed = append(changed, "plus_addressing")
	}
	if in.InboundBlocklist != nil {
		list, f := normalizeBlocklist(*in.InboundBlocklist)
		fe = append(fe, f...)
		if len(f) == 0 && !slices.Equal(list, next.InboundBlocklist) {
			next.InboundBlocklist = list
			changed = append(changed, "inbound_blocklist")
		}
	}
	if len(fe) > 0 {
		problems.Write(w, r, problems.Validation(fe...))
		return
	}
	actor := ""
	if p := server.PrincipalFrom(ctx); p != nil {
		actor = p.UserID
	}
	if in.SMTP != nil {
		if err := m.set.PutSMTP(ctx, nextSMTP, newPW, actor); err != nil {
			problems.Write(w, r, err)
			return
		}
	}
	if err := m.set.PutEmail(ctx, next, actor); err != nil {
		problems.Write(w, r, err)
		return
	}
	if len(changed) > 0 {
		if err := m.audit(ctx, actor, server.ClientIP(r), "email.settings_updated", "settings", "email", map[string]any{"fields": changed}); err != nil {
			problems.Write(w, r, err)
			return
		}
	}
	m.getEmail(w, r)
}

// audit writes one audit_log row (never values of secrets).
func (m *Module) audit(ctx context.Context, actor, ip, action, ttype, tid string, data any) error {
	b, _ := json.Marshal(data)
	return m.db.Tx(ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log (id, actor_id, action, target_type, target_id, data, ip, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			store.NewID(), store.Nullable(actor), action, ttype, tid, string(b), ip, store.Now())
		return err
	})
}

// ---- test email ----

// testEmail sends a test mail with the submitted, unsaved config (merged with
// the saved password when none is submitted, env locks applied), capped at
// 15 s, and reports a readable error code. Saved settings never change.
func (m *Module) testEmail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var in struct {
		To     string   `json:"to"`
		Config *emailIn `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		problems.Write(w, r, problems.BadRequest("invalid JSON body"))
		return
	}
	if !validAddr(in.To) {
		problems.Write(w, r, problems.Validation(problems.Field("to", "email", "a valid email address")))
		return
	}
	sv, err := m.set.SMTP(ctx)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	ev, err := m.set.Email(ctx)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	cfgDoc := sv.SMTP
	pw := ""
	if sv.PasswordSet {
		pw, _ = m.set.SMTPPassword(ctx)
	}
	ident := ev.StaffFrom
	if ident.Address == "" {
		ident = ev.CustomerFrom
	}
	if in.Config != nil {
		if in.Config.SMTP != nil {
			if _, fe := applySMTP(in.Config.SMTP, &cfgDoc, sv.SMTP, nil); len(fe) > 0 {
				problems.Write(w, r, problems.Validation(fe...))
				return
			}
			if in.Config.SMTP.Password != nil && *in.Config.SMTP.Password != "" {
				pw = *in.Config.SMTP.Password
			}
		}
		if in.Config.Senders != nil && in.Config.Senders.Staff != nil && validAddr(in.Config.Senders.Staff.FromAddress) {
			ident = settings.Identity{Name: in.Config.Senders.Staff.FromName, Address: in.Config.Senders.Staff.FromAddress}
		}
	}
	// Env still wins over the submitted values (the effective config would).
	settings.Overlay(&cfgDoc, "ST_SMTP_", m.getenv)
	if p := m.getenv("ST_SMTP_PASSWORD"); p != "" {
		pw = p
	}
	cfg := configFrom(cfgDoc, pw)
	res := map[string]any{"ok": true, "message": "Test email sent to " + in.To}
	if !cfg.Configured() {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error_code": "other", "message": "SMTP host is not set"})
		return
	}
	if cfg.Timeout > 15*time.Second {
		cfg.Timeout = 15 * time.Second
	}
	brand, _ := m.brand(ctx)
	if ident.Address == "" {
		ident.Address = "noreply@" + m.host
	}
	if ident.Name == "" {
		ident.Name = brand.ProductName
	}
	ml := &Mail{From: mail.Address{Name: ident.Name, Address: ident.Address}, To: mail.Address{Address: in.To},
		Subject: "Test email from " + brand.ProductName,
		Text:    "This is a test email from " + brand.ProductName + ". Your SMTP settings work.\n"}
	tctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := m.sender.Send(tctx, cfg, ml); err != nil {
		code := "other"
		var f *SendFailure
		if errors.As(err, &f) {
			code = f.Code
		} else if errors.Is(err, context.DeadlineExceeded) {
			code = "timeout"
		}
		res = map[string]any{"ok": false, "error_code": code, "message": testMessage(code, err)}
	}
	writeJSON(w, http.StatusOK, res)
}

func testMessage(code string, err error) string {
	plain := map[string]string{
		"auth_failed":        "The SMTP server rejected the username or password.",
		"tls_mismatch":       "TLS mismatch: check the security mode (none, STARTTLS, TLS) and the port.",
		"connection_refused": "Connection refused: check the host and port.",
		"timeout":            "The SMTP server did not answer in time.",
		"dns":                "The SMTP host name could not be resolved.",
		"rejected":           "The SMTP server rejected the sender or recipient.",
	}[code]
	if plain == "" {
		plain = "Sending failed."
	}
	detail := err.Error()
	if len(detail) > 300 {
		detail = detail[:300]
	}
	return plain + " (" + detail + ")"
}

// ---- preview ----

func (m *Module) preview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key := chi.URLParam(r, "key")
	if !m.tpl.Has(key) {
		problems.Write(w, r, problems.NotFound())
		return
	}
	brand, err := m.brand(ctx)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	sup, _ := m.support(ctx)
	locale := m.tpl.Locale(r.URL.Query().Get("locale"), sup.DefaultLocale)
	out, err := m.tpl.Render(key, locale, brand, FixtureData(key, m.portalURL, m.agentURL))
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(out.HTML))
}

// FixtureData returns deterministic sample data for key (preview, golden tests).
func FixtureData(key, portalURL, agentURL string) branded {
	const id = "0192f000-0000-7000-8000-000000000001"
	switch {
	case strings.HasPrefix(key, "customer."):
		d := &CustomerData{RecipientName: "Ada Lovelace", Ticket: CustomerTicketView{Number: 1042, Subject: "Printer on floor 3 is offline", URL: portalURL + "/tickets/" + id}}
		if key == TplCustomerReply {
			d.Message = CustomerMessageView{AuthorName: "Grace Hopper", Body: "We restarted the print server. Could you try again?\n\n- step one\n- step two"}
			d.Signature = "Grace Hopper  \nSupport Team"
		}
		if key == TplCustomerResolved {
			d.CSATURL = d.Ticket.URL + "?rate=1"
		}
		return d
	case strings.HasPrefix(key, "staff.") && key != TplStaffWebhookDisabled:
		return &StaffData{RecipientName: "Grace Hopper", ActorName: "Alan Turing",
			Ticket:   StaffTicketView{ID: id, Number: 1042, Subject: "Printer on floor 3 is offline", Priority: "high", Status: "open", TierLabel: "L2", TeamName: "IT", RequesterName: "Ada Lovelace", URL: agentURL + "/tickets/" + id},
			Messages: []StaffMessageView{{AuthorName: "Ada Lovelace", Body: "The printer shows **error 42** since this morning."}},
			Reason:   "Needs network access", FromTier: "L1", ToTier: "L2", SLAWhich: "first_response", SLADue: "2026-10-20 10:00", SLABreached: key == TplStaffSLA}
	default:
		return &GenericData{RecipientName: "Ada Lovelace", Vars: map[string]string{
			"url": portalURL + "/auth/token/abc123", "expires_minutes": "60", "inviter": "Grace Hopper", "name": "Ada Lovelace",
			"reason": "too many failures", "failures": "20", "code": "482913", "expires_days": "7"}}
	}
}

// ---- signature (NOT-035) ----

const maxSignature = 4000

func (m *Module) getSignature(w http.ResponseWriter, r *http.Request) {
	p := server.PrincipalFrom(r.Context())
	if !p.IsStaff() {
		problems.Write(w, r, problems.NotFound())
		return
	}
	u, err := m.user(r.Context(), p.UserID)
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"signature_md": u.Signature})
}

func (m *Module) putSignature(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := server.PrincipalFrom(ctx)
	if !p.IsStaff() {
		problems.Write(w, r, problems.NotFound())
		return
	}
	var in struct {
		SignatureMD *string `json:"signature_md"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		problems.Write(w, r, problems.BadRequest("invalid JSON body"))
		return
	}
	if in.SignatureMD == nil {
		problems.Write(w, r, problems.Validation(problems.Field("signature_md", "required", "")))
		return
	}
	sig := strings.TrimSpace(*in.SignatureMD)
	if utf8.RuneCountInString(sig) > maxSignature {
		problems.Write(w, r, problems.Validation(problems.Field("signature_md", "max_length", fmt.Sprintf("at most %d characters", maxSignature))))
		return
	}
	var n int64
	err := m.db.Tx(ctx, func(tx *store.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE users SET signature_md = ?, updated_at = ? WHERE id = ? AND kind = 'staff'`, sig, store.Now(), p.UserID)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	if err != nil {
		problems.Write(w, r, err)
		return
	}
	if n == 0 {
		problems.Write(w, r, problems.NotFound())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"signature_md": sig})
}
