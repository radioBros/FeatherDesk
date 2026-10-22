// SPDX-License-Identifier: AGPL-3.0-only

package directory

import (
	"context"
	"errors"
	"net/mail"
	"strings"

	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// webmailDomains are public mail providers: never used for organization
// auto-join (TKT-016).
var webmailDomains = map[string]bool{
	"gmail.com": true, "googlemail.com": true, "outlook.com": true, "hotmail.com": true, "live.com": true,
	"msn.com": true, "yahoo.com": true, "ymail.com": true, "icloud.com": true, "me.com": true, "mac.com": true,
	"aol.com": true, "gmx.com": true, "gmx.de": true, "gmx.net": true, "web.de": true, "proton.me": true,
	"protonmail.com": true, "mail.com": true, "zoho.com": true, "yandex.com": true, "yandex.ru": true,
	"libero.it": true, "virgilio.it": true, "tiscali.it": true, "alice.it": true, "hotmail.it": true,
	"yahoo.it": true, "orange.fr": true, "free.fr": true, "laposte.net": true, "wanadoo.fr": true,
	"t-online.de": true, "freenet.de": true, "fastmail.com": true, "hey.com": true, "qq.com": true, "163.com": true,
}

// IsWebmailDomain reports whether d is a public mail provider.
func IsWebmailDomain(d string) bool { return webmailDomains[strings.ToLower(strings.TrimSpace(d))] }

// ParseCustomerEmail lower-cases and validates an address ("" + false when invalid).
func ParseCustomerEmail(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 254 {
		return "", false
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s || !strings.Contains(s, "@") {
		return "", false
	}
	at := strings.LastIndexByte(s, '@')
	if at < 1 || at == len(s)-1 || !strings.Contains(s[at+1:], ".") {
		return "", false
	}
	return strings.ToLower(s), true
}

// EmailDomain returns the lower-cased domain of an address.
func EmailDomainOf(email string) string {
	at := strings.LastIndexByte(email, '@')
	if at < 0 {
		return ""
	}
	return strings.ToLower(email[at+1:])
}

// OrgForDomain returns the active organization claiming the domain ("" when
// none, or for webmail domains).
func OrgForDomain(ctx context.Context, q store.Querier, domainName string) (string, error) {
	domainName = strings.ToLower(domainName)
	if domainName == "" || IsWebmailDomain(domainName) {
		return "", nil
	}
	rows, err := q.QueryContext(ctx, `SELECT id, email_domains FROM organizations WHERE status = 'active' ORDER BY created_at, id`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var doms store.JSON[[]string]
		if err := rows.Scan(&id, &doms); err != nil {
			return "", err
		}
		for _, d := range doms.V {
			if strings.EqualFold(d, domainName) {
				return id, nil
			}
		}
	}
	return "", rows.Err()
}

// CustomerRef is the minimal customer identity.
type CustomerRef struct {
	ID             string
	Email          string
	Name           string
	OrganizationID string
	Role           domain.Role
	Status         domain.UserStatus
	Created        bool
}

// FindOrCreateCustomer resolves a customer by e-mail inside tx, creating an
// active requester without a password when none exists (TKT-002/TKT-015). The
// organization is chosen by e-mail domain (never webmail). A staff account
// with that address is a 409: kinds never mix (D-04).
func FindOrCreateCustomer(ctx context.Context, tx *store.Tx, email, name string, status domain.UserStatus) (CustomerRef, error) {
	addr, ok := ParseCustomerEmail(email)
	if !ok {
		return CustomerRef{}, problems.Validation(problems.Field("requester_email", "email", "a valid e-mail address"))
	}
	var c CustomerRef
	var kind string
	var org store.NullString
	err := tx.QueryRowContext(ctx, `SELECT id, kind, email, name, organization_id, role, status FROM users WHERE email = ?`, addr).
		Scan(&c.ID, &kind, &c.Email, &c.Name, &org, &c.Role, &c.Status)
	switch {
	case err == nil:
		if kind != string(domain.KindCustomer) {
			return CustomerRef{}, problems.Conflict("the e-mail address belongs to a staff account")
		}
		c.OrganizationID = org.String
		return c, nil
	case !errors.Is(store.NotFound(err), store.ErrNotFound):
		return CustomerRef{}, err
	}
	orgID, err := OrgForDomain(ctx, tx, EmailDomainOf(addr))
	if err != nil {
		return CustomerRef{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = addr[:strings.IndexByte(addr, '@')]
	}
	if len([]rune(name)) > 120 {
		name = string([]rune(name)[:120])
	}
	if status == "" {
		status = domain.UserActive
	}
	c = CustomerRef{ID: store.NewID(), Email: addr, Name: name, OrganizationID: orgID, Role: domain.RoleRequester, Status: status, Created: true}
	now := store.Now()
	_, err = tx.ExecContext(ctx, `INSERT INTO users (id, kind, email, name, role, organization_id, status, created_at, updated_at)
		VALUES (?, 'customer', ?, ?, 'requester', ?, ?, ?, ?)`, c.ID, c.Email, c.Name, store.Nullable(orgID), string(status), now, now)
	if store.IsUniqueViolation(err) {
		return CustomerRef{}, problems.Conflict("the e-mail address is already registered")
	}
	return c, err
}

// LoadCustomer reads a customer by id inside q (store.ErrNotFound for staff).
func LoadCustomer(ctx context.Context, q store.Querier, id string) (CustomerRef, error) {
	var c CustomerRef
	var org store.NullString
	err := q.QueryRowContext(ctx, `SELECT id, email, name, organization_id, role, status FROM users WHERE id = ? AND kind = 'customer'`, id).
		Scan(&c.ID, &c.Email, &c.Name, &org, &c.Role, &c.Status)
	c.OrganizationID = org.String
	return c, store.NotFound(err)
}
