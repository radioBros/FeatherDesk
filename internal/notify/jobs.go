// SPDX-License-Identifier: AGPL-3.0-only

package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/radioBros/FeatherDesk/internal/store"
)

// default link lifetimes stated in auth mails when the caller passes none.
var authExpiry = map[string]string{
	TplAuthVerifyEmail: "1440", TplAuthResetPassword: "60", TplAuthMagicLink: "15", TplAuthInvite: "10080", TplAuthAlreadyRegistered: "60",
	TplAuthMFACode: "10",
}

// handleAuthJob sends one auth.* mail (JobAuthMail). auth.* templates can't
// be disabled, aren't opt-out-able and are never coalesced. The actor rule
// does not apply. Loss on crash is accepted (queue_system_decision §1).
func (m *Module) handleAuthJob(ctx context.Context, payload json.RawMessage) error {
	var a AuthMail
	if err := json.Unmarshal(payload, &a); err != nil {
		return fmt.Errorf("mail.auth: bad payload: %w", err)
	}
	if !Transactional(a.Template) || !m.tpl.Has(a.Template) {
		m.log.Error("mail.auth: unknown template", "template", a.Template)
		return nil // not retryable
	}
	var u *userRow
	if a.UserID != "" {
		u, _ = m.user(ctx, a.UserID)
	}
	if u == nil && a.To != "" {
		us, err := m.users(ctx, `SELECT `+userCols+` FROM users WHERE lower(email) = lower(?)`, a.To)
		if err != nil {
			return err
		}
		if len(us) == 1 {
			u = us[0]
		}
	}
	to := strings.TrimSpace(a.To)
	r := Recipient{Addr: to, Audience: "customer"}
	if u != nil {
		if to == "" {
			r.Addr = u.Email
		}
		r.UserID, r.Name, r.Locale = u.ID, u.Name, u.Locale
		if u.Kind == "staff" {
			r.Audience = "staff"
		}
	}
	if a.Locale != "" {
		r.Locale = a.Locale
	}
	if r.Addr == "" {
		return errors.New("mail.auth: no recipient")
	}
	vars := map[string]string{}
	for k, v := range a.Vars {
		vars[k] = v
	}
	if vars["expires_minutes"] == "" {
		vars["expires_minutes"] = authExpiry[a.Template]
	}
	if vars["name"] == "" {
		vars["name"] = r.Name
	}
	return m.sendGeneric(ctx, Target{Key: a.Template, R: r}, vars)
}

// handleAdminJob sends one template to every active admin (JobAdminMail).
func (m *Module) handleAdminJob(ctx context.Context, payload json.RawMessage) error {
	var a AdminMail
	if err := json.Unmarshal(payload, &a); err != nil {
		return fmt.Errorf("mail.admins: bad payload: %w", err)
	}
	if !m.tpl.Has(a.Template) {
		m.log.Error("mail.admins: unknown template", "template", a.Template)
		return nil
	}
	em, err := m.emailSettings(ctx)
	if err != nil {
		return err
	}
	for _, k := range em.DisabledTemplates {
		if k == a.Template && !strings.HasPrefix(k, "auth.") {
			return nil
		}
	}
	admins, err := m.staffByRole(ctx, "admin")
	if err != nil {
		return err
	}
	var firstErr error
	for _, u := range admins {
		if isOptional(a.Template) && contains(u.OptOut, a.Template) {
			continue
		}
		if err := m.sendGeneric(ctx, Target{Key: a.Template, R: u.recipient()}, a.Vars); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// sendGeneric renders a non-ticket template and delivers it (first attempt
// inline, failures persisted as JobSend rows like ticket mail).
func (m *Module) sendGeneric(ctx context.Context, t Target, vars map[string]string) error {
	brand, err := m.brand(ctx)
	if err != nil {
		return err
	}
	sup, err := m.support(ctx)
	if err != nil {
		return err
	}
	em, err := m.emailSettings(ctx)
	if err != nil {
		return err
	}
	if vars == nil {
		vars = map[string]string{}
	}
	r, err := m.tpl.Render(t.Key, m.recipientLocale(t.R, sup), brand, &GenericData{RecipientName: t.R.Name, Vars: vars})
	if err != nil {
		return err
	}
	ml := m.envelope(t, r, store.NewID(), "", brand, em)
	return m.deliver(ctx, t.Key, ml)
}
