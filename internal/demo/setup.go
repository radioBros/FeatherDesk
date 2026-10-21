// SPDX-License-Identifier: AGPL-3.0-only

package demo

import (
	"fmt"
	"time"

	"github.com/radioBros/SimpleTickets/internal/authn"
	"github.com/radioBros/SimpleTickets/internal/store"
)

type orgRef struct {
	ID, Name string
}

type teamRef struct {
	ID, Key, Name string
}

type catRef struct {
	ID, Key, Name, Team string
	Project             string // project key (D-20)
}

type fieldRef struct {
	ID, Key, Type string
}

// person is a demo account.
type person struct {
	key, email, name, role string
	tier                   int
	teams                  []string // team keys (staff)
	org                    string   // organization key (customers)
	signature              string
	idp                    bool
	projects               []string // staff: project keys (nil = every project, D-20)
	projectAdmin           []string // staff: project keys administered
	id                     string
	cl                     *client
}

func (p *person) isStaff() bool {
	return p.role == "admin" || p.role == "supervisor" || p.role == "agent"
}

type idResp struct {
	ID string `json:"id"`
}

type orgDef struct {
	key, name string
	domains   []string
	notes     string
}

var orgDefs = []orgDef{
	{"acme", "Acme Corp", []string{"acme.test", "acme-corp.test"}, "Key account since 2021. Premium support."},
	{"globex", "Globex", []string{"globex.test"}, "Mid-size customer. Billing contact: accounts@globex.test."},
	{"initech", "Initech", []string{"initech.test"}, "Evaluation customer, trial ends next quarter."},
	{"hq", "Demo HQ (internal)", []string{"demo-hq.test"}, "Our own employees: they file Internal IT tickets through the IT desk."},
}

type teamDef struct {
	key, name, desc, email, mode, cal string
}

var teamDefs = []teamDef{
	{"front", "Front desk", "First line: triage, accounts and general questions", "frontdesk@demo.test", "agents", "24x7"},
	{"tech", "Technical", "Technical incidents, integrations and the API", "technical@demo.test", "both", "rome"},
	{"billing", "Billing", "Invoices, payments, refunds and contracts", "billing@demo.test", "mailbox", "ny"},
}

func (s *seeder) setupStructure() error {
	a := s.admin
	for _, d := range orgDefs {
		var o struct {
			ID string `json:"id"`
		}
		if err := a.post("/agent/organizations", map[string]any{"name": d.name, "email_domains": d.domains, "notes": d.notes}, &o); err != nil {
			return err
		}
		s.orgs[d.key] = &orgRef{ID: o.ID, Name: d.name}
		s.advance(time.Minute)
	}
	wk := func(from, to string, days ...string) map[string]any {
		w := map[string]any{}
		for _, d := range days {
			w[d] = []map[string]string{{"start": from, "end": to}}
		}
		return w
	}
	year := s.now.Year()
	cals := []struct {
		key, name, tz string
		week          map[string]any
		holidays      []string
	}{
		{"24x7", "24×7 (always on)", "UTC", wk("00:00", "24:00", "mon", "tue", "wed", "thu", "fri", "sat", "sun"), nil},
		{"rome", "Business hours (Rome)", "Europe/Rome", func() map[string]any {
			w := wk("09:00", "13:00", "mon", "tue", "wed", "thu", "fri")
			for _, d := range []string{"mon", "tue", "wed", "thu", "fri"} {
				w[d] = []map[string]string{{"start": "09:00", "end": "13:00"}, {"start": "14:00", "end": "18:00"}}
			}
			return w
		}(), []string{
			fmt.Sprintf("%d-08-15", year), fmt.Sprintf("%d-11-01", year), fmt.Sprintf("%d-12-08", year),
			fmt.Sprintf("%d-12-25", year), fmt.Sprintf("%d-12-26", year), fmt.Sprintf("%d-01-01", year+1),
		}},
		{"ny", "Billing hours (New York)", "America/New_York", wk("08:00", "16:00", "mon", "tue", "wed", "thu", "fri"),
			[]string{fmt.Sprintf("%d-11-26", year), fmt.Sprintf("%d-12-25", year)}},
	}
	for _, c := range cals {
		var out idResp
		if err := a.post("/admin/calendars", map[string]any{"name": c.name, "timezone": c.tz, "week": c.week, "holidays": nilEmpty(c.holidays)}, &out); err != nil {
			return fmt.Errorf("calendar %s: %w", c.key, err)
		}
		s.cals[c.key] = out.ID
	}
	for _, d := range teamDefs {
		body := map[string]any{"name": d.name, "description": d.desc, "notification_email": d.email, "notify_mode": d.mode, "calendar_id": s.cals[d.cal]}
		var out idResp
		if err := a.post("/admin/teams", body, &out); err != nil {
			return fmt.Errorf("team %s: %w", d.key, err)
		}
		s.teams[d.key] = &teamRef{ID: out.ID, Key: d.key, Name: d.name}
	}
	return nil
}

func nilEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

var staffDefs = []person{
	{key: "sup", email: "supervisor@demo.test", name: "Sam Supervisor", role: "supervisor", tier: 3, teams: []string{"front", "tech", "billing"},
		signature: "Sam Supervisor\nSupport Operations\n\n_We are here Mon-Fri, 9-18 CET._"},
	{key: "fd1", email: "fiona.ferris@demo.test", name: "Fiona Ferris", role: "agent", tier: 1, teams: []string{"front"}, signature: "Fiona\nFront desk, L1"},
	{key: "fd2", email: "felix.ford@demo.test", name: "Felix Ford", role: "agent", tier: 2, teams: []string{"front"}},
	{key: "fd3", email: "freya.fields@demo.test", name: "Freya Fields", role: "agent", tier: 3, teams: []string{"front"}, signature: "Freya Fields\nSenior support engineer",
		projectAdmin: []string{"mob"}},
	{key: "te1", email: "theo.turner@demo.test", name: "Theo Turner", role: "agent", tier: 1, teams: []string{"tech"}, signature: "Theo\nTechnical support L1"},
	{key: "te2", email: "tina.torres@demo.test", name: "Tina Torres", role: "agent", tier: 2, teams: []string{"tech"}},
	{key: "te3", email: "tomas.tran@demo.test", name: "Tomas Tran", role: "agent", tier: 3, teams: []string{"tech"}, signature: "Tomas Tran\nPlatform engineer (L3)\n\nhttps://status.demo.test"},
	{key: "bi1", email: "bella.brooks@demo.test", name: "Bella Brooks", role: "agent", tier: 1, teams: []string{"billing"}},
	{key: "bi2", email: "ben.blake@demo.test", name: "Ben Blake", role: "agent", tier: 2, teams: []string{"billing"}, signature: "Ben Blake\nBilling specialist"},
	{key: "bi3", email: "bianca.bell@demo.test", name: "Bianca Bell", role: "agent", tier: 3, teams: []string{"billing"}},
	{key: "idp", email: "ivy.identity@demo.test", name: "Ivy Identity", role: "agent", tier: 2, teams: []string{"tech"}, idp: true},
	// D-20: works in the Mobile project only (I-11: sees no other project's tickets).
	{key: singleProjectKey, email: "mona.mobile@demo.test", name: "Mona Mobile", role: "agent", tier: 2, teams: []string{"tech"},
		projects: []string{"mob"}, signature: "Mona Mobile\nMobile apps support"},
}

// setupAdmin creates the break-glass admin (sixty-four days back, before the
// first ticket) and signs it in; every later step acts through its session.
func (s *seeder) setupAdmin() error {
	s.sim(s.now.AddDate(0, 0, -64))
	if _, err := authn.CreateAdmin(s.ctx, s.db, "admin@demo.test", "Ada Admin", s.opt.Password, "demo"); err != nil {
		return err
	}
	adm := &person{key: "admin", email: "admin@demo.test", name: "Ada Admin", role: "admin", tier: 3,
		signature: "Ada Admin\nSimpleTickets Demo administrator"}
	s.wall()
	cl := s.newClient()
	if err := cl.login("staff", adm.email, s.opt.Password); err != nil {
		return err
	}
	adm.cl, adm.id = cl, cl.userID
	s.staff["admin"] = adm
	s.admin = cl
	s.sim(s.now.AddDate(0, 0, -64).Add(time.Minute))
	return nil
}

func (s *seeder) setupStaff() error {
	s.sim(s.now.AddDate(0, 0, -64).Add(10 * time.Minute))
	for i := range staffDefs {
		d := staffDefs[i]
		var teams []string
		for _, k := range d.teams {
			teams = append(teams, s.teams[k].ID)
		}
		body := map[string]any{"email": d.email, "name": d.name, "role": d.role, "tier": d.tier, "team_ids": teams, "password": s.opt.Password,
			"projects": s.memberships(&d)}
		var out idResp
		if err := s.admin.post("/admin/staff", body, &out); err != nil {
			return fmt.Errorf("staff %s: %w", d.email, err)
		}
		d.id = out.ID
		s.staff[d.key] = &d
		s.advance(time.Minute)
	}
	// IdP-managed: no API creates one (they come from OIDC); mark the row.
	idp := s.staff["idp"]
	return s.exec(`UPDATE users SET idp_managed = ?, password_hash = NULL WHERE id = ?`, true, idp.id)
}

type custDef struct {
	key, email, name, role, org, status string
	locale                              string
}

var custDefs = []custDef{
	{"mia", "mia.manager@acme.test", "Mia Manager", "org_manager", "acme", "", "en"},
	{"uma", "uma.user@acme.test", "Uma Underwood", "requester", "acme", "", "en"},
	{"ben", "ben.baker@acme.test", "Ben Baker", "requester", "acme", "", "en"},
	{"chloe", "chloe.chen@acme.test", "Chloe Chen", "requester", "acme", "", "it"},
	{"gus", "gus.gallo@globex.test", "Gus Gallo", "org_manager", "globex", "", "it"},
	{"hank", "hank.hill@globex.test", "Hank Hill", "requester", "globex", "", "en"},
	{"ines", "ines.ito@globex.test", "Ines Ito", "requester", "globex", "", "de"},
	{"jack", "jack.jones@globex.test", "Jack Jones", "requester", "globex", "", "fr"},
	{"ida", "ida.initech@initech.test", "Ida Initech", "org_manager", "initech", "", "en"},
	{"ivan", "ivan.ivanov@initech.test", "Ivan Ivanov", "requester", "initech", "", "es"},
	{"pia", "pia.pending@initech.test", "Pia Pending", "requester", "initech", "pending_verification", "en"},
	{"dan", "dan.disabled@initech.test", "Dan Departed", "requester", "initech", "disabled", "en"},
	{"eva", "eva.employee@demo-hq.test", "Eva Employee", "requester", "hq", "", "en"},
	{"omar", "omar.office@demo-hq.test", "Omar Office", "org_manager", "hq", "", "it"},
}

func (s *seeder) setupCustomers() error {
	hash, err := authn.HashPassword(s.ctx, s.opt.Password)
	if err != nil {
		return err
	}
	for i := range custDefs {
		d := custDefs[i]
		body := map[string]any{"email": d.email, "name": d.name, "role": d.role, "organization_id": s.orgs[d.org].ID, "locale": d.locale, "send_invite": false}
		var out idResp
		if err := s.admin.post("/agent/customers", body, &out); err != nil {
			return fmt.Errorf("customer %s: %w", d.email, err)
		}
		// No API sets a customer's password (they use invites, magic links or
		// self-service reset): store the demo hash directly.
		if err := s.exec(`UPDATE users SET password_hash = ? WHERE id = ?`, hash, out.ID); err != nil {
			return err
		}
		s.custs[d.key] = &person{key: d.key, email: d.email, name: d.name, role: d.role, org: d.org, id: out.ID}
		s.advance(time.Minute)
	}
	return nil
}

// signIn opens a session for every active demo user (at the wall clock: the
// session lifetimes are real time) and sets the staff signatures.
func (s *seeder) signIn() error {
	keep := s.cur()
	s.wall()
	defer s.sim(keep)
	for _, d := range append([]person{{key: "admin"}}, staffDefs...) {
		p := s.staff[d.key]
		if p.idp {
			continue // IdP-managed accounts sign in through OIDC only
		}
		if p.cl == nil {
			p.cl = s.newClient()
			if err := p.cl.login("staff", p.email, s.opt.Password); err != nil {
				return fmt.Errorf("sign in %s: %w", p.email, err)
			}
		}
		if p.signature != "" {
			if err := p.cl.put("/me/signature", map[string]string{"signature_md": p.signature}, nil); err != nil {
				return fmt.Errorf("signature %s: %w", p.email, err)
			}
		}
	}
	for _, d := range custDefs {
		p := s.custs[d.key]
		if p.cl != nil || p.key == "pia" {
			continue
		}
		p.cl = s.newClient()
		if err := p.cl.login("customer", p.email, s.opt.Password); err != nil {
			continue // pending_verification / not yet usable accounts
		}
	}
	return nil
}

// staffBy returns the staff member of a team at a tier (exact), or the
// supervisor when there is none.
func (s *seeder) staffBy(team string, tier int) *person {
	prefix := map[string]string{"front": "fd", "tech": "te", "billing": "bi"}[team]
	if p, ok := s.staff[fmt.Sprintf("%s%d", prefix, tier)]; ok {
		return p
	}
	return s.staff["sup"]
}

// exec runs one statement in a write transaction (store-level fallback for
// data that has no service API).
func (s *seeder) exec(q string, args ...any) error {
	return s.db.Tx(s.ctx, func(tx *store.Tx) error {
		_, err := tx.ExecContext(s.ctx, q, args...)
		return err
	})
}
