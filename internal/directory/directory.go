// SPDX-License-Identifier: AGPL-3.0-only

// Package directory owns the support directory: customers, organizations,
// teams and membership, categories, business-hours calendars, custom-field
// definitions and the support settings document. Staff administration
// (directory/staff*.go) belongs to the auth module.
//
// Read-mostly data (teams, categories, active custom-field definitions) is
// served from in-memory snapshots that are rebuilt after every write, so the
// request path does no DB reads for pickers and validation (TKT-037, TKT-039).
package directory

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/radioBros/SimpleTickets/internal/blob"
	"github.com/radioBros/SimpleTickets/internal/realtime"
	"github.com/radioBros/SimpleTickets/internal/settings"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// DirDeps are the platform services the directory needs.
type DirDeps struct {
	DB       *store.DB
	Log      *slog.Logger
	Settings *settings.Service
	Hub      *realtime.Hub // optional: close sockets of disabled users
	Blob     blob.Store
}

// Directory is the directory service. Construct with NewDirectory.
type Directory struct {
	db  *store.DB
	log *slog.Logger
	set *settings.Service
	hub *realtime.Hub

	snap      atomic.Pointer[dirSnapshot]
	snapMu    sync.Mutex
	snapReads atomic.Int64 // DB loads of the snapshot (tests assert 0 on the hot path)
	snapGen   atomic.Int64 // bumped by Invalidate: a load that started before is not stored

	hookMu sync.RWMutex
	// membershipHooks run inside the write tx when staff lose team membership or
	// tier, so the ticket service can re-validate M-4 and unassign (TKT-017).
	membershipHooks []MembershipHook
	// teamsChanged run after commit with the affected user IDs (AUTH-002 team cache).
	teamsChanged []func(userIDs []string)
	// disabler disables a user (AUTH-017 authn.DisableUser when wired).
	disabler Disabler
	// calendarChanged run after commit when a business calendar changed or a
	// team's calendar_id changed (SLA due-date recompute, TKT-035).
	calendarChanged []func(ctx context.Context, calendarID string)
}

// MembershipHook is called in the write transaction after the membership or
// tier of users changed; teamID is "" for a tier change across all teams.
type MembershipHook func(ctx context.Context, tx *store.Tx, actorID, teamID string, userIDs []string) error

// Disabler disables a user inside tx: status, sessions, tokens (I-5). Its
// signature matches authn.DisableUser so modules.go can plug it in directly.
type Disabler func(ctx context.Context, tx *store.Tx, userID, actorID, reason, ip string) error

// NewDirectory builds the directory service.
func NewDirectory(d DirDeps) *Directory {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	// app/modules.go wires OnTeamsChanged → authn.InvalidateTeams (AUTH-002
	// team cache) and SetDisabler → authn.DisableUser (AUTH-017).
	return &Directory{db: d.DB, log: d.Log, set: d.Settings, hub: d.Hub}
}

// OnMembershipChange registers a hook run inside membership/tier write txs.
func (d *Directory) OnMembershipChange(h MembershipHook) {
	d.hookMu.Lock()
	d.membershipHooks = append(d.membershipHooks, h)
	d.hookMu.Unlock()
}

// OnTeamsChanged registers fn, called after commit with the users whose team
// list changed (authn drops its ≤ 30 s team cache for them).
func (d *Directory) OnTeamsChanged(fn func(userIDs []string)) {
	d.hookMu.Lock()
	d.teamsChanged = append(d.teamsChanged, fn)
	d.hookMu.Unlock()
}

// SetDisabler plugs in authn.DisableUser (AUTH-017). Without it the directory
// performs the same steps itself (status, sessions, PATs) and closes sockets.
func (d *Directory) SetDisabler(fn Disabler) {
	d.hookMu.Lock()
	d.disabler = fn
	d.hookMu.Unlock()
}

// OnCalendarChanged registers fn, run after commit when calendar calendarID
// changed ("" = a team's calendar assignment changed).
func (d *Directory) OnCalendarChanged(fn func(ctx context.Context, calendarID string)) {
	d.hookMu.Lock()
	d.calendarChanged = append(d.calendarChanged, fn)
	d.hookMu.Unlock()
}

func (d *Directory) fireCalendarChanged(ctx context.Context, calendarID string) {
	d.hookMu.RLock()
	fns := d.calendarChanged
	d.hookMu.RUnlock()
	for _, f := range fns {
		f(ctx, calendarID)
	}
}

func (d *Directory) runMembershipHooks(ctx context.Context, tx *store.Tx, actorID, teamID string, userIDs []string) error {
	d.hookMu.RLock()
	hooks := d.membershipHooks
	d.hookMu.RUnlock()
	for _, h := range hooks {
		if err := h(ctx, tx, actorID, teamID, userIDs); err != nil {
			return err
		}
	}
	return runGlobalMembershipHooks(ctx, tx, actorID, teamID, userIDs)
}

var (
	globalHookMu sync.RWMutex
	globalHooks  []MembershipHook
)

// RegisterMembershipHook installs a process-wide hook run by
// MembershipChanged (the ticket service registers its M-4 re-validation).
// Hooks must be idempotent: several may run for one change.
func RegisterMembershipHook(h MembershipHook) {
	globalHookMu.Lock()
	globalHooks = append(globalHooks, h)
	globalHookMu.Unlock()
}

func runGlobalMembershipHooks(ctx context.Context, tx *store.Tx, actorID, teamID string, userIDs []string) error {
	globalHookMu.RLock()
	hooks := globalHooks
	globalHookMu.RUnlock()
	for _, h := range hooks {
		if err := h(ctx, tx, actorID, teamID, userIDs); err != nil {
			return err
		}
	}
	return nil
}

// MembershipChanged must be called inside the write transaction of any
// change that removes staff from a team, lowers their tier or disables them
// (teamID "" = every team of the users): tickets they own that no longer
// satisfy M-4 are unassigned with an event (TKT-017). The staff admin API
// (directory/staff*.go, auth module) calls it for tier, team and status edits.
func MembershipChanged(ctx context.Context, tx *store.Tx, actorID, teamID string, userIDs []string) error {
	return runGlobalMembershipHooks(ctx, tx, actorID, teamID, userIDs)
}

func (d *Directory) fireTeamsChanged(userIDs []string) {
	d.hookMu.RLock()
	fns := d.teamsChanged
	d.hookMu.RUnlock()
	for _, f := range fns {
		f(userIDs)
	}
}

// ---- snapshot of read-mostly data ----

// Team is a team row.
type Team struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Description       string  `json:"description"`
	NotificationEmail *string `json:"notification_email"`
	NotifyMode        string  `json:"notify_mode"`
	CalendarID        *string `json:"calendar_id"`
	// AssignmentStrategy is manual, round_robin or least_busy (D-19 §A).
	AssignmentStrategy string    `json:"assignment_strategy"`
	MemberCount        int       `json:"member_count"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// Category is a category row.
type Category struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	DefaultTeamID   *string `json:"default_team_id"`
	DefaultPriority *string `json:"default_priority"`
	Active          bool    `json:"active"`
	Sort            int     `json:"sort"`
}

type dirSnapshot struct {
	teams      []Team
	teamByID   map[string]*Team
	categories []Category
	catByID    map[string]*Category
	fields     []FieldDef // all definitions (active and archived), sorted
	fieldByKey map[string]*FieldDef
	fieldByID  map[string]*FieldDef
	calendars  map[string]*Calendar
}

// Invalidate drops the snapshot; the next read rebuilds it.
func (d *Directory) Invalidate() {
	d.snapGen.Add(1)
	d.snap.Store(nil)
}

// SnapshotLoads reports how many times the snapshot was read from the DB.
func (d *Directory) SnapshotLoads() int64 { return d.snapReads.Load() }

func (d *Directory) snapshot(ctx context.Context) (*dirSnapshot, error) {
	if s := d.snap.Load(); s != nil {
		return s, nil
	}
	d.snapMu.Lock()
	defer d.snapMu.Unlock()
	if s := d.snap.Load(); s != nil {
		return s, nil
	}
	gen := d.snapGen.Load()
	s, err := d.loadSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	d.snapReads.Add(1)
	// A write that invalidated the snapshot while this load was reading
	// (e.g. createTeam's refresh waiting on snapMu) must not see it come back
	// stale: only a load that started after the last Invalidate is cached.
	if d.snapGen.Load() == gen {
		d.snap.Store(s)
	}
	return s, nil
}

func (d *Directory) loadSnapshot(ctx context.Context) (*dirSnapshot, error) {
	q := d.db.Read()
	s := &dirSnapshot{teamByID: map[string]*Team{}, catByID: map[string]*Category{}, fieldByKey: map[string]*FieldDef{},
		fieldByID: map[string]*FieldDef{}, calendars: map[string]*Calendar{}}
	rows, err := q.QueryContext(ctx, `SELECT t.id, t.name, t.description, t.notification_email, t.notify_mode, t.calendar_id, t.assignment_strategy, t.created_at, t.updated_at,
		(SELECT COUNT(*) FROM team_members m WHERE m.team_id = t.id)
		FROM teams t ORDER BY lower(t.name), t.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t Team
		var email, cal store.NullString
		if err := rows.Scan(&t.ID, &t.Name, &t.Description, &email, &t.NotifyMode, &cal, &t.AssignmentStrategy, &t.CreatedAt, &t.UpdatedAt, &t.MemberCount); err != nil {
			rows.Close()
			return nil, err
		}
		t.NotificationEmail, t.CalendarID = dirStrPtr(email), dirStrPtr(cal)
		t.CreatedAt, t.UpdatedAt = t.CreatedAt.UTC(), t.UpdatedAt.UTC()
		s.teams = append(s.teams, t)
	}
	rows.Close()
	for i := range s.teams {
		s.teamByID[s.teams[i].ID] = &s.teams[i]
	}
	rows, err = q.QueryContext(ctx, `SELECT id, name, default_team_id, default_priority, active, sort FROM categories ORDER BY sort, lower(name), id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c Category
		var team, prio store.NullString
		if err := rows.Scan(&c.ID, &c.Name, &team, &prio, &c.Active, &c.Sort); err != nil {
			rows.Close()
			return nil, err
		}
		c.DefaultTeamID, c.DefaultPriority = dirStrPtr(team), dirStrPtr(prio)
		s.categories = append(s.categories, c)
	}
	rows.Close()
	for i := range s.categories {
		s.catByID[s.categories[i].ID] = &s.categories[i]
	}
	fields, err := loadFieldDefs(ctx, q)
	if err != nil {
		return nil, err
	}
	s.fields = fields
	for i := range s.fields {
		s.fieldByKey[s.fields[i].Key] = &s.fields[i]
		s.fieldByID[s.fields[i].ID] = &s.fields[i]
	}
	cals, err := loadCalendars(ctx, q)
	if err != nil {
		return nil, err
	}
	for i := range cals {
		s.calendars[cals[i].ID] = &cals[i]
	}
	return s, nil
}

// Teams returns all teams (snapshot).
func (d *Directory) Teams(ctx context.Context) ([]Team, error) {
	s, err := d.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return s.teams, nil
}

// Team returns one team, or store.ErrNotFound.
func (d *Directory) Team(ctx context.Context, id string) (Team, error) {
	s, err := d.snapshot(ctx)
	if err != nil {
		return Team{}, err
	}
	if t, ok := s.teamByID[id]; ok {
		return *t, nil
	}
	return Team{}, store.ErrNotFound
}

// Categories returns all categories (snapshot), sorted.
func (d *Directory) Categories(ctx context.Context) ([]Category, error) {
	s, err := d.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return s.categories, nil
}

// Category returns one category, or store.ErrNotFound.
func (d *Directory) Category(ctx context.Context, id string) (Category, error) {
	s, err := d.snapshot(ctx)
	if err != nil {
		return Category{}, err
	}
	if c, ok := s.catByID[id]; ok {
		return *c, nil
	}
	return Category{}, store.ErrNotFound
}

// TeamCalendar returns the business calendar of a team (nil = 24×7).
func (d *Directory) TeamCalendar(ctx context.Context, teamID string) (*Calendar, error) {
	if teamID == "" {
		return nil, nil
	}
	s, err := d.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	t, ok := s.teamByID[teamID]
	if !ok || t.CalendarID == nil {
		return nil, nil
	}
	return s.calendars[*t.CalendarID], nil
}

// Support returns the support settings (cached by the settings service).
func (d *Directory) Support(ctx context.Context) settings.Support {
	if d.set == nil {
		return settings.DefaultSupport()
	}
	v, err := d.set.Support(ctx)
	if err != nil {
		d.log.Warn("directory: support settings", "err", err)
		return settings.DefaultSupport()
	}
	return v
}

func dirStrPtr(n store.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}
