// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
	"github.com/radioBros/SimpleTickets/internal/store"
)

// ListQuery is the parsed filter set of GET /agent/tickets and /portal/tickets (TKT-004).
type ListQuery struct {
	Statuses     []domain.TicketStatus
	Priorities   []domain.Priority
	TeamID       string // id or "none"
	Assignee     string // "me", "none" or a user id
	Tier         int
	CategoryID   string
	OrgID        string
	RequesterID  string
	Tag          string
	UpdatedSince *time.Time
	Q            string
	Sort         string
	Breached     *bool
	CF           map[string]string
	MineOnly     bool
	Project      string // D-20: project id or key; resolved by filterSQL (422 when unknown)
	Limit        int
	Cursor       string
}

var agentListParams = map[string]bool{"cursor": true, "limit": true, "status": true, "priority": true, "team_id": true, "assignee": true,
	"tier": true, "category_id": true, "organization_id": true, "requester_id": true, "tag": true, "updated_since": true, "q": true,
	"sort": true, "breached": true, "project": true}

var portalListParams = map[string]bool{"cursor": true, "limit": true, "status": true, "q": true, "mine_only": true, "project": true}

func parseLimit(v string) (int, error) {
	if v == "" {
		return 50, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 200 {
		return 0, problems.Validation(problems.Field("limit", "range", "1..200"))
	}
	return n, nil
}

// ParseAgentListQuery validates the staff list parameters (unknown keys, other
// than cf.<key>, are rejected so typos never silently widen a list).
func ParseAgentListQuery(v url.Values) (ListQuery, error) {
	q := ListQuery{Sort: "attention", CF: map[string]string{}}
	var errs []problems.FieldError
	for k := range v {
		if strings.HasPrefix(k, "cf.") && len(k) > 3 {
			q.CF[k[3:]] = v.Get(k)
			continue
		}
		if !agentListParams[k] {
			errs = append(errs, problems.Field(k, "unknown", "unknown parameter"))
		}
	}
	var err error
	if q.Limit, err = parseLimit(v.Get("limit")); err != nil {
		return q, err
	}
	q.Cursor = v.Get("cursor")
	for _, s := range splitCSV(v.Get("status")) {
		st := domain.TicketStatus(s)
		if !st.Valid() {
			errs = append(errs, problems.Field("status", "enum", "unknown status "+s))
		}
		q.Statuses = append(q.Statuses, st)
	}
	for _, s := range splitCSV(v.Get("priority")) {
		pr := domain.Priority(s)
		if !pr.Valid() {
			errs = append(errs, problems.Field("priority", "enum", "unknown priority "+s))
		}
		q.Priorities = append(q.Priorities, pr)
	}
	q.TeamID, q.Assignee = v.Get("team_id"), v.Get("assignee")
	if t := v.Get("tier"); t != "" {
		n, err := strconv.Atoi(t)
		if err != nil || n < 1 || n > 3 {
			errs = append(errs, problems.Field("tier", "range", "1..3"))
		}
		q.Tier = n
	}
	q.CategoryID, q.OrgID, q.RequesterID, q.Tag = v.Get("category_id"), v.Get("organization_id"), v.Get("requester_id"), strings.ToLower(v.Get("tag"))
	if s := v.Get("updated_since"); s != "" {
		ts, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			errs = append(errs, problems.Field("updated_since", "date_time", "RFC 3339 timestamp"))
		}
		ts = ts.UTC()
		q.UpdatedSince = &ts
	}
	q.Q = v.Get("q")
	if len(q.Q) > 200 {
		errs = append(errs, problems.Field("q", "max_length", "at most 200 characters"))
	}
	q.Project = v.Get("project")
	if s := v.Get("sort"); s != "" {
		if _, ok := sorts[s]; !ok {
			errs = append(errs, problems.Field("sort", "enum", "unknown sort"))
		}
		q.Sort = s
	}
	if b := v.Get("breached"); b != "" {
		x, err := strconv.ParseBool(b)
		if err != nil {
			errs = append(errs, problems.Field("breached", "boolean", "true or false"))
		}
		q.Breached = &x
	}
	if len(errs) > 0 {
		return q, problems.Validation(errs...)
	}
	return q, nil
}

// ParsePortalListQuery accepts only the customer parameters; staff filters
// such as assignee or tier are a 422, never silently honoured (TKT-004).
func ParsePortalListQuery(v url.Values) (ListQuery, error) {
	q := ListQuery{Sort: "-updated_at"}
	var errs []problems.FieldError
	for k := range v {
		if !portalListParams[k] {
			errs = append(errs, problems.Field(k, "unknown", "unknown parameter"))
		}
	}
	var err error
	if q.Limit, err = parseLimit(v.Get("limit")); err != nil {
		return q, err
	}
	q.Cursor = v.Get("cursor")
	if s := v.Get("status"); s != "" {
		sts, ok := customerStatusFilter(s)
		if !ok {
			errs = append(errs, problems.Field("status", "enum", "open, awaiting_reply, resolved or closed"))
		}
		q.Statuses = sts
	}
	q.Q = v.Get("q")
	if len(q.Q) > 200 {
		errs = append(errs, problems.Field("q", "max_length", "at most 200 characters"))
	}
	q.Project = v.Get("project")
	if m := v.Get("mine_only"); m != "" {
		b, err := strconv.ParseBool(m)
		if err != nil {
			errs = append(errs, problems.Field("mine_only", "boolean", "true or false"))
		}
		q.MineOnly = b
	}
	if len(errs) > 0 {
		return q, problems.Validation(errs...)
	}
	return q, nil
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ---- sorting with keyset cursors ----

type keyKind byte

const (
	kInt  keyKind = 'i'
	kTime keyKind = 't'
	kStr  keyKind = 's'
)

type sortKey struct {
	expr string
	desc bool
	kind keyKind
	val  func(t *ticketRow) any
}

// farFuture stands in for "no due date" so due-date ordering is total.
var farFuture = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

const farFutureSQL = `'9999-12-31 00:00:00+00:00'`

func attentionRank(t *ticketRow) int {
	if !t.LastStaffActivityAt.Valid || t.LastCustomerActivityAt.After(t.LastStaffActivityAt.Time) {
		return 0
	}
	return 1
}

func workingRank(t *ticketRow) int {
	if t.Status == domain.StatusNew || t.Status == domain.StatusOpen {
		return 0
	}
	return 1
}

func priorityRank(p domain.Priority) int {
	switch p {
	case domain.PriorityUrgent:
		return 4
	case domain.PriorityHigh:
		return 3
	case domain.PriorityNormal:
		return 2
	}
	return 1
}

func dueOf(t *ticketRow) time.Time {
	switch {
	case t.FirstResponseDueAt.Valid:
		return t.FirstResponseDueAt.Time
	case t.ResolutionDueAt.Valid:
		return t.ResolutionDueAt.Time
	}
	return farFuture
}

var (
	keyID      = sortKey{"t.id", false, kStr, func(t *ticketRow) any { return t.ID }}
	keyIDDesc  = sortKey{"t.id", true, kStr, func(t *ticketRow) any { return t.ID }}
	keyDue     = sortKey{"COALESCE(t.first_response_due_at, t.resolution_due_at, " + farFutureSQL + ")", false, kTime, func(t *ticketRow) any { return dueOf(t) }}
	keyPrioAsc = sortKey{priorityRankSQL, false, kInt, func(t *ticketRow) any { return priorityRank(t.Priority) }}
)

// sorts: lifecycle §5 "needs attention" (working statuses first, customer
// waiting first, priority, earliest due) and the plain orders of TicketSort.
var sorts = map[string][]sortKey{
	"attention": {
		{"CASE WHEN t.status IN ('new', 'open') THEN 0 ELSE 1 END", false, kInt, func(t *ticketRow) any { return workingRank(t) }},
		{"CASE WHEN t.last_staff_activity_at IS NULL OR t.last_customer_activity_at > t.last_staff_activity_at THEN 0 ELSE 1 END", false, kInt, func(t *ticketRow) any { return attentionRank(t) }},
		{priorityRankSQL, true, kInt, func(t *ticketRow) any { return priorityRank(t.Priority) }},
		keyDue, keyID,
	},
	"updated_at":  {{"t.updated_at", false, kTime, func(t *ticketRow) any { return t.UpdatedAt }}, keyID},
	"-updated_at": {{"t.updated_at", true, kTime, func(t *ticketRow) any { return t.UpdatedAt }}, keyIDDesc},
	"created_at":  {{"t.created_at", false, kTime, func(t *ticketRow) any { return t.CreatedAt }}, keyID},
	"-created_at": {{"t.created_at", true, kTime, func(t *ticketRow) any { return t.CreatedAt }}, keyIDDesc},
	"priority":    {keyPrioAsc, keyID},
	"-priority":   {{priorityRankSQL, true, kInt, func(t *ticketRow) any { return priorityRank(t.Priority) }}, keyIDDesc},
	"due":         {keyDue, keyID},
	"number":      {{"t.number", false, kInt, func(t *ticketRow) any { return t.Number }}, keyID},
	"-number":     {{"t.number", true, kInt, func(t *ticketRow) any { return t.Number }}, keyIDDesc},
}

func encodeCursor(keys []sortKey, t *ticketRow) string {
	vals := make([]any, len(keys))
	for i, k := range keys {
		v := k.val(t)
		if tm, ok := v.(time.Time); ok {
			v = tm.UTC().Format(time.RFC3339Nano)
		}
		vals[i] = v
	}
	b, _ := json.Marshal(vals)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(keys []sortKey, c string) ([]any, error) {
	bad := problems.Validation(problems.Field("cursor", "invalid", "invalid cursor"))
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return nil, bad
	}
	var raw []any
	if json.Unmarshal(b, &raw) != nil || len(raw) != len(keys) {
		return nil, bad
	}
	out := make([]any, len(keys))
	for i, k := range keys {
		switch k.kind {
		case kInt:
			f, ok := raw[i].(float64)
			if !ok {
				return nil, bad
			}
			out[i] = int64(f)
		case kTime:
			s, ok := raw[i].(string)
			tm, err := time.Parse(time.RFC3339Nano, s)
			if !ok || err != nil {
				return nil, bad
			}
			out[i] = tm.UTC()
		case kStr:
			s, ok := raw[i].(string)
			if !ok {
				return nil, bad
			}
			out[i] = s
		}
	}
	return out, nil
}

// keysetWhere builds "(k1 > v1) OR (k1 = v1 AND k2 > v2) …" honouring directions.
func keysetWhere(keys []sortKey, vals []any) (string, []any) {
	var ors []string
	var args []any
	for i := range keys {
		var ands []string
		for j := 0; j < i; j++ {
			ands = append(ands, keys[j].expr+" = ?")
			args = append(args, vals[j])
		}
		op := " > ?"
		if keys[i].desc {
			op = " < ?"
		}
		ands = append(ands, keys[i].expr+op)
		args = append(args, vals[i])
		ors = append(ors, "("+strings.Join(ands, " AND ")+")")
	}
	return "(" + strings.Join(ors, " OR ") + ")", args
}

func orderBy(keys []sortKey) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k.expr
		if k.desc {
			parts[i] += " DESC"
		}
	}
	return strings.Join(parts, ", ")
}

// filterSQL composes visibility + filters; staff selects staff search columns.
func (s *Service) filterSQL(ctx context.Context, p *domain.Principal, q ListQuery) (string, []any, error) {
	pol := s.Policy(ctx)
	pred, args := authz.TicketPredicate(p, pol, "t")
	where := []string{pred}
	add := func(cond string, a ...any) {
		where = append(where, cond)
		args = append(args, a...)
	}
	if len(q.Statuses) > 0 {
		ss := make([]string, len(q.Statuses))
		for i, st := range q.Statuses {
			ss[i] = string(st)
		}
		add("t.status IN ("+placeholders(len(ss))+")", anyStrings(ss)...)
	}
	if len(q.Priorities) > 0 {
		ps := make([]string, len(q.Priorities))
		for i, pr := range q.Priorities {
			ps[i] = string(pr)
		}
		add("t.priority IN ("+placeholders(len(ps))+")", anyStrings(ps)...)
	}
	switch q.TeamID {
	case "":
	case "none":
		add("t.team_id IS NULL")
	default:
		add("t.team_id = ?", q.TeamID)
	}
	switch q.Assignee {
	case "":
	case "me":
		add("t.assignee_id = ?", p.UserID)
	case "none":
		add("t.assignee_id IS NULL")
	default:
		add("t.assignee_id = ?", q.Assignee)
	}
	if q.Tier > 0 {
		add("t.tier = ?", q.Tier)
	}
	if q.CategoryID != "" {
		add("t.category_id = ?", q.CategoryID)
	}
	if q.OrgID != "" {
		add("t.organization_id = ?", q.OrgID)
	}
	if q.RequesterID != "" {
		add("t.requester_id = ?", q.RequesterID)
	}
	if q.MineOnly {
		add("t.requester_id = ?", p.UserID)
	}
	if q.Project != "" {
		pid, err := s.projectParam(ctx, q.Project)
		if err != nil {
			return "", nil, err
		}
		add("t.project_id = ?", pid)
	}
	if q.Tag != "" {
		add("EXISTS (SELECT 1 FROM ticket_tags tt JOIN tags g ON g.id = tt.tag_id WHERE tt.ticket_id = t.id AND lower(g.name) = ?)", q.Tag)
	}
	if q.UpdatedSince != nil {
		add("t.updated_at >= ?", *q.UpdatedSince)
	}
	if q.Breached != nil {
		now := s.clock()
		if *q.Breached {
			add(breachedSQL, now, now)
		} else {
			add("NOT "+breachedSQL, now, now)
		}
	}
	if strings.TrimSpace(q.Q) != "" {
		if sub, sargs, ok := store.TicketMatch(s.db.Dialect, q.Q, p.IsStaff()); ok {
			add("t.id IN ("+sub+")", sargs...)
		}
	}
	keys := make([]string, 0, len(q.CF))
	for k := range q.CF {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		cond, cargs, err := s.cfFilter(ctx, p, k, q.CF[k])
		if err != nil {
			return "", nil, err
		}
		add(cond, cargs...)
	}
	return strings.Join(where, " AND "), args, nil
}

// cfFilter builds one `cf.<key>=<value>` condition (TKT-040): equality for
// text/select, membership for multiselect, gte:/lte: ranges for number/date.
func (s *Service) cfFilter(ctx context.Context, p *domain.Principal, key, raw string) (string, []any, error) {
	def, ok := s.dir.FieldByKey(ctx, key)
	if !ok || !authz.CanSeeField(p, def.Access()) {
		return "", nil, problems.Validation(problems.Field("cf."+key, "unknown", "unknown custom field"))
	}
	base := "EXISTS (SELECT 1 FROM ticket_field_values v WHERE v.ticket_id = t.id AND v.field_id = ? AND "
	op, val := "=", raw
	switch {
	case strings.HasPrefix(raw, "gte:"):
		op, val = ">=", raw[4:]
	case strings.HasPrefix(raw, "lte:"):
		op, val = "<=", raw[4:]
	}
	bad := problems.Validation(problems.Field("cf."+key, "filter", "invalid filter value"))
	switch def.Type {
	case domain.FieldNumber:
		n, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return "", nil, bad
		}
		return base + "v.value_num " + op + " ?)", []any{def.ID, n}, nil
	case domain.FieldDate:
		if _, err := time.Parse(time.DateOnly, val); err != nil {
			return "", nil, bad
		}
		return base + "v.value_date " + op + " ?)", []any{def.ID, val}, nil
	case domain.FieldMultiselect:
		if op != "=" {
			return "", nil, bad
		}
		return base + jsonArrayContains(s.db.Dialect, "v.value_json") + ")", []any{def.ID, val}, nil
	case domain.FieldCheckbox:
		b, err := strconv.ParseBool(val)
		if err != nil || op != "=" {
			return "", nil, bad
		}
		return base + jsonBoolEquals(s.db.Dialect, "v.value_json") + ")", []any{def.ID, strconv.FormatBool(b)}, nil
	default:
		if op != "=" {
			return "", nil, bad
		}
		return base + "v.value_text = ?)", []any{def.ID, val}, nil
	}
}

// List returns one page of tickets visible to p.
func (s *Service) list(ctx context.Context, p *domain.Principal, q ListQuery) ([]*ticketRow, *string, error) {
	keys, ok := sorts[q.Sort]
	if !ok {
		keys = sorts["attention"]
	}
	where, args, err := s.filterSQL(ctx, p, q)
	if err != nil {
		return nil, nil, err
	}
	if q.Cursor != "" {
		vals, err := decodeCursor(keys, q.Cursor)
		if err != nil {
			return nil, nil, err
		}
		kw, kargs := keysetWhere(keys, vals)
		where += " AND " + kw
		args = append(args, kargs...)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Read().QueryContext(ctx, `SELECT `+ticketCols+` FROM tickets t WHERE `+where+` ORDER BY `+orderBy(keys)+` LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var out []*ticketRow
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *string
	if len(out) > limit {
		out = out[:limit]
		c := encodeCursor(keys, out[len(out)-1])
		next = &c
	}
	return out, next, nil
}

// ListStaff returns a TicketPage.
func (s *Service) ListStaff(ctx context.Context, p *domain.Principal, q ListQuery) (*pageDTO[ticketSummaryDTO], error) {
	rows, next, err := s.list(ctx, p, q)
	if err != nil {
		return nil, err
	}
	items, err := s.staffSummaries(ctx, s.db.Read(), p, rows)
	if err != nil {
		return nil, err
	}
	return &pageDTO[ticketSummaryDTO]{Items: items, NextCursor: next}, nil
}

// staffSummaries batch-loads refs, tags and fields for summaries.
func (s *Service) staffSummaries(ctx context.Context, q store.Querier, p *domain.Principal, rows []*ticketRow) ([]ticketSummaryDTO, error) {
	items := make([]ticketSummaryDTO, 0, len(rows))
	if len(rows) == 0 {
		return items, nil
	}
	var uids, tids []string
	for _, t := range rows {
		uids = append(uids, t.RequesterID, t.AssigneeID)
		tids = append(tids, t.ID)
	}
	refs, err := loadUserRefs(ctx, q, uids)
	if err != nil {
		return nil, err
	}
	tags, err := tagsFor(ctx, q, tids)
	if err != nil {
		return nil, err
	}
	vals, err := loadFieldValues(ctx, q, tids)
	if err != nil {
		return nil, err
	}
	now := s.clock()
	for _, t := range rows {
		s.fillKey(ctx, t)
		d := t.summary(now)
		d.Project = s.dir.ProjectRefOf(ctx, t.ProjectID)
		s.fillRefs(&d, t, refs)
		if tg := tags[t.ID]; tg != nil {
			d.Tags = tg
		}
		d.CustomFields = s.presentFields(ctx, p, vals[t.ID])
		items = append(items, d)
	}
	return items, nil
}

// ListCustomer returns a CustomerTicketPage.
func (s *Service) ListCustomer(ctx context.Context, p *domain.Principal, q ListQuery) (*pageDTO[customerTicketSummaryDTO], error) {
	rows, next, err := s.list(ctx, p, q)
	if err != nil {
		return nil, err
	}
	items, err := s.customerSummaries(ctx, s.db.Read(), p, rows)
	if err != nil {
		return nil, err
	}
	return &pageDTO[customerTicketSummaryDTO]{Items: items, NextCursor: next}, nil
}

func (s *Service) customerSummaries(ctx context.Context, q store.Querier, p *domain.Principal, rows []*ticketRow) ([]customerTicketSummaryDTO, error) {
	items := make([]customerTicketSummaryDTO, 0, len(rows))
	if len(rows) == 0 {
		return items, nil
	}
	var uids, tids []string
	for _, t := range rows {
		uids = append(uids, t.RequesterID)
		tids = append(tids, t.ID)
	}
	refs, err := loadUserRefs(ctx, q, uids)
	if err != nil {
		return nil, err
	}
	vals, err := loadFieldValues(ctx, q, tids)
	if err != nil {
		return nil, err
	}
	for _, t := range rows {
		s.fillKey(ctx, t)
		d := t.customerSummary()
		d.Project = s.dir.ProjectRefOf(ctx, t.ProjectID)
		if r, ok := refs[t.RequesterID]; ok {
			d.Requester = r
		}
		d.CustomFields = s.presentFields(ctx, p, vals[t.ID])
		items = append(items, d)
	}
	return items, nil
}

func tagsFor(ctx context.Context, q store.Querier, ids []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT tt.ticket_id, g.name FROM ticket_tags tt JOIN tags g ON g.id = tt.tag_id
		WHERE tt.ticket_id IN (`+placeholders(len(ids))+`) ORDER BY g.name`, anyStrings(ids)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tid, name string
		if err := rows.Scan(&tid, &name); err != nil {
			return nil, err
		}
		out[tid] = append(out[tid], name)
	}
	return out, rows.Err()
}

// TicketCounts are the sidebar buckets (TicketCounts schema).
type TicketCounts struct {
	Mine       int            `json:"mine"`
	Unassigned int            `json:"unassigned"`
	Triage     int            `json:"triage"`
	Breached   int            `json:"breached"`
	Tier       map[string]int `json:"tier"`
}

func (c *TicketCounts) add(o TicketCounts) {
	c.Mine += o.Mine
	c.Unassigned += o.Unassigned
	c.Triage += o.Triage
	c.Breached += o.Breached
	for k, v := range o.Tier {
		c.Tier[k] += v
	}
}

func newTicketCounts() TicketCounts {
	return TicketCounts{Tier: map[string]int{"1": 0, "2": 0, "3": 0}}
}

// Counts is TicketCounts plus the per-project breakdown (D-20).
type Counts struct {
	TicketCounts
	ByProject map[string]TicketCounts `json:"by_project"`
}

// projectParam resolves a `project` query parameter (id or key).
func (s *Service) projectParam(ctx context.Context, v string) (string, error) {
	pr, err := s.dir.ResolveProject(ctx, v)
	if err != nil {
		return "", problems.Validation(problems.Field("project", "not_found", "unknown project"))
	}
	return pr.ID, nil
}

// Counts computes the sidebar buckets over open (non resolved/closed)
// tickets through the same visibility predicate as the list (TKT-004), for
// all projects.
func (s *Service) Counts(ctx context.Context, p *domain.Principal) (Counts, error) {
	return s.CountsFor(ctx, p, "")
}

// CountsFor is Counts with the totals limited to project (id; "" = all).
// by_project always lists every project the caller works in (I-11), with
// zeros for projects without open tickets, so the switcher can read any.
func (s *Service) CountsFor(ctx context.Context, p *domain.Principal, project string) (Counts, error) {
	pred, args := authz.TicketPredicate(p, s.Policy(ctx), "t")
	now := s.clock()
	q := `SELECT t.project_id,
		COALESCE(SUM(CASE WHEN t.assignee_id = ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN t.assignee_id IS NULL THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN t.team_id IS NULL THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN t.sla_paused_at IS NULL AND ((t.first_responded_at IS NULL AND t.first_response_due_at < ?) OR t.resolution_due_at < ?) THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN t.tier = 1 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN t.tier = 2 THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN t.tier = 3 THEN 1 ELSE 0 END), 0)
		FROM tickets t WHERE ` + openishSQL + ` AND ` + pred + ` GROUP BY t.project_id`
	c := Counts{TicketCounts: newTicketCounts(), ByProject: map[string]TicketCounts{}}
	if p.IsStaff() {
		prs, err := s.dir.StaffProjects(ctx, p)
		if err != nil {
			return c, err
		}
		for _, pr := range prs {
			c.ByProject[pr.ID] = newTicketCounts()
		}
	}
	all := append([]any{p.UserID, now, now}, args...)
	rows, err := s.db.Read().QueryContext(ctx, q, all...)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var pid string
		var t1, t2, t3 int
		g := newTicketCounts()
		if err := rows.Scan(&pid, &g.Mine, &g.Unassigned, &g.Triage, &g.Breached, &t1, &t2, &t3); err != nil {
			return c, err
		}
		g.Tier = map[string]int{"1": t1, "2": t2, "3": t3}
		c.ByProject[pid] = g
		if project == "" || project == pid {
			c.add(g)
		}
	}
	return c, rows.Err()
}
