// SPDX-License-Identifier: AGPL-3.0-only

package authn

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// Verdicts of ResolveEmailSender (reason strings of inbound_messages, NOT-028).
const (
	SenderOK       = "ok"             // resolved; staff mail is authenticated
	SenderUnknown  = "unknown_sender" // no user with that address
	SenderSpoof    = "staff_spoof"    // staff address without trusted DKIM/DMARC pass
	SenderDisabled = "disabled_user"  // the user exists but is disabled
)

// ResolveEmailSender maps the From address of an inbound message to a user
// (AUTH-034). Customers need no authentication results (D-16 sender rules
// apply in mailin). A staff sender is accepted only when an
// Authentication-Results header added by trustedAuthServID shows dkim=pass
// aligned with the From domain, or spf=pass together with an aligned
// dmarc=pass; results from any other authserv-id are ignored. authResults
// holds the raw header values, one per line. Unknown senders return
// ("", "", SenderUnknown, nil).
func ResolveEmailSender(ctx context.Context, db *store.DB, from string, authResults string, trustedAuthServID string) (userID string, kind domain.UserKind, verdict string, err error) {
	addr, err := mail.ParseAddress(strings.TrimSpace(from))
	if err != nil {
		return "", "", SenderUnknown, nil
	}
	email := normEmail(addr.Address)
	var status domain.UserStatus
	err = db.Read().QueryRowContext(ctx, `SELECT id, kind, status FROM users WHERE email = ?`, email).Scan(&userID, &kind, &status)
	if errors.Is(store.NotFound(err), store.ErrNotFound) {
		return "", "", SenderUnknown, nil
	}
	if err != nil {
		return "", "", "", err
	}
	if status == domain.UserDisabled {
		return userID, kind, SenderDisabled, nil
	}
	if kind == domain.KindCustomer {
		return userID, kind, SenderOK, nil
	}
	if !trustedStaffMail(emailDomain(email), authResults, trustedAuthServID) {
		return userID, kind, SenderSpoof, nil
	}
	return userID, kind, SenderOK, nil
}

// trustedStaffMail evaluates RFC 8601 Authentication-Results headers.
func trustedStaffMail(fromDomain, headers, trusted string) bool {
	trusted = strings.ToLower(strings.TrimSpace(trusted))
	if trusted == "" || fromDomain == "" {
		return false
	}
	for _, h := range strings.Split(headers, "\n") {
		res := parseAuthResults(h)
		if res == nil || res.authservID != trusted {
			continue
		}
		var dkim, spf, dmarc bool
		for _, m := range res.methods {
			switch m.method {
			case "dkim":
				if m.result == "pass" && (aligned(m.props["header.d"], fromDomain) || aligned(domainOf(m.props["header.i"]), fromDomain)) {
					dkim = true
				}
			case "spf":
				if m.result == "pass" {
					spf = true
				}
			case "dmarc":
				if m.result == "pass" && aligned(m.props["header.from"], fromDomain) {
					dmarc = true
				}
			}
		}
		if dkim || (spf && dmarc) {
			return true
		}
	}
	return false
}

type authResults struct {
	authservID string
	methods    []authMethod
}

type authMethod struct {
	method, result string
	props          map[string]string
}

// parseAuthResults parses one header value ("Authentication-Results:" prefix
// optional). Comments in parentheses are removed.
func parseAuthResults(h string) *authResults {
	h = strings.TrimSpace(h)
	if i := strings.IndexByte(h, ':'); i > 0 && strings.EqualFold(strings.TrimSpace(h[:i]), "Authentication-Results") {
		h = h[i+1:]
	}
	h = stripComments(h)
	parts := strings.Split(h, ";")
	if len(parts) == 0 {
		return nil
	}
	head := strings.Fields(parts[0])
	if len(head) == 0 {
		return nil
	}
	res := &authResults{authservID: strings.ToLower(head[0])}
	for _, p := range parts[1:] {
		toks := strings.Fields(p)
		if len(toks) == 0 {
			continue
		}
		mth, result, ok := strings.Cut(toks[0], "=")
		if !ok {
			continue // "none"
		}
		am := authMethod{method: strings.ToLower(mth), result: strings.ToLower(result), props: map[string]string{}}
		for _, t := range toks[1:] {
			if k, v, ok := strings.Cut(t, "="); ok {
				am.props[strings.ToLower(k)] = strings.ToLower(strings.Trim(v, `"`))
			}
		}
		res.methods = append(res.methods, am)
	}
	return res
}

func stripComments(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '(':
			depth++
		case r == ')' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func domainOf(s string) string {
	if i := strings.LastIndexByte(s, '@'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// aligned is DMARC relaxed alignment approximated without a public-suffix
// list: equal domains, or one is a subdomain of the other.
func aligned(d, from string) bool {
	d, from = strings.TrimSuffix(strings.ToLower(d), "."), strings.TrimSuffix(strings.ToLower(from), ".")
	if !strings.Contains(d, ".") || !strings.Contains(from, ".") {
		return false
	}
	return d == from || strings.HasSuffix(from, "."+d) || strings.HasSuffix(d, "."+from)
}
