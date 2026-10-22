// SPDX-License-Identifier: AGPL-3.0-only

package kb

import (
	"context"
	"testing"

	"github.com/radioBros/FeatherDesk/internal/authz"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/store"
	"github.com/radioBros/FeatherDesk/internal/store/storetest"
)

// agreementPrincipals covers every I-10 audience, including PATs with and
// without kb:read (tickets:read implies it).
func agreementPrincipals() map[string]*domain.Principal {
	tok := func(p domain.Principal, scopes ...string) *domain.Principal {
		p.TokenID, p.Scopes = "t", scopes
		return &p
	}
	cust := domain.Principal{UserID: "c", Kind: domain.KindCustomer, Role: domain.RoleRequester, OrganizationID: "o"}
	mgr := domain.Principal{UserID: "m", Kind: domain.KindCustomer, Role: domain.RoleOrgManager, OrganizationID: "o"}
	agent := domain.Principal{UserID: "a", Kind: domain.KindStaff, Role: domain.RoleAgent, Tier: 1}
	admin := domain.Principal{UserID: "x", Kind: domain.KindStaff, Role: domain.RoleAdmin, Tier: 3}
	return map[string]*domain.Principal{
		"anonymous": nil, "requester": &cust, "org_manager": &mgr, "agent": &agent, "admin": &admin,
		"customer-pat-tickets-read": tok(cust, domain.ScopeTicketsRead), "customer-pat-tickets-write": tok(cust, domain.ScopeTicketsWrite),
		"staff-pat-kb-read": tok(agent, domain.ScopeKBRead), "staff-pat-directory": tok(agent, domain.ScopeDirectoryRead),
	}
}

// TestCanSeeArticle_AudienceAgreesWithKB: the module's reader class equals
// authz.ArticleAudienceOf for every kind of principal.
func TestCanSeeArticle_AudienceAgreesWithKB(t *testing.T) {
	want := map[audience]authz.ArticleAudience{audAnonymous: authz.ArticleAnonymous, audCustomer: authz.ArticleCustomer, audStaff: authz.ArticleStaff}
	for name, p := range agreementPrincipals() {
		if got := authz.ArticleAudienceOf(p); got != want[audienceOf(p)] {
			t.Errorf("%s: kb audience %s, authz audience %d", name, audienceOf(p), got)
		}
	}
}

// TestCanSeeArticle_AgreesWithKBPredicate (AUTH-011, I-10): the pure
// authz.CanSeeArticle and the SQL predicate the KB module composes into every
// read, listing and search agree for every principal × category visibility ×
// article visibility × status that the schema accepts (M-10 rejects an
// article wider than its category), on both engines. Non-staff lookups
// include `archived` only to answer 410 without content, so the readable set
// compared here is draft+published for them and all statuses for staff.
func TestCanSeeArticle_AgreesWithKBPredicate(t *testing.T) {
	storetest.Engines(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		vis := []domain.KBVisibility{domain.KBPublic, domain.KBCustomers, domain.KBInternal}
		statuses := []domain.KBStatus{domain.KBDraft, domain.KBPublished, domain.KBArchived}
		views := map[string]authz.ArticleView{}
		for _, cv := range vis {
			cat := store.NewID()
			if err := db.Tx(ctx, func(tx *store.Tx) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO kb_categories (id, slug, visibility) VALUES (?, ?, ?)`, cat, "c-"+cat, string(cv))
				return err
			}); err != nil {
				t.Fatal(err)
			}
			for _, av := range vis {
				for _, st := range statuses {
					id := store.NewID()
					err := db.Tx(ctx, func(tx *store.Tx) error {
						_, err := tx.ExecContext(ctx, `INSERT INTO kb_articles (id, category_id, slug, status, visibility) VALUES (?, ?, ?, ?, ?)`,
							id, cat, "a-"+id, string(st), string(av))
						return err
					})
					if err != nil {
						if domain.KBVisibilityRank(av) >= domain.KBVisibilityRank(cv) {
							t.Fatalf("schema rejected a valid article (%s in %s): %v", av, cv, err)
						}
						continue // M-10: an article cannot be wider than its category
					}
					views[id] = authz.ArticleView{Visibility: av, CategoryVisibility: cv, Status: st}
				}
			}
		}
		if len(views) < 3*3*2 {
			t.Fatalf("only %d article fixtures", len(views))
		}
		checked := 0
		for name, p := range agreementPrincipals() {
			aud := audienceOf(p)
			sts := []string{"draft", "published"}
			if aud == audStaff {
				sts = append(sts, "archived")
			}
			pred := visPredicate(aud, sts)
			rows, err := db.Read().QueryContext(ctx, `SELECT a.id FROM kb_articles a JOIN kb_categories c ON c.id = a.category_id WHERE `+pred)
			if err != nil {
				t.Fatal(err)
			}
			inSQL := map[string]bool{}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				inSQL[id] = true
			}
			rows.Close()
			for id, v := range views {
				if mem := authz.CanSeeArticle(p, v); mem != inSQL[id] {
					t.Errorf("%s on %+v: CanSeeArticle=%v SQL=%v", name, v, mem, inSQL[id])
				}
				checked++
			}
		}
		t.Logf("%d (principal, article) pairs agree", checked)
	})
}
