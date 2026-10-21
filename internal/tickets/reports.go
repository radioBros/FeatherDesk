// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/radioBros/SimpleTickets/internal/authz"
	"github.com/radioBros/SimpleTickets/internal/directory"
	"github.com/radioBros/SimpleTickets/internal/domain"
	"github.com/radioBros/SimpleTickets/internal/problems"
)

// Reports summary (TKT-028): computed on demand from ticket rows and events in
// the range (no materialised tables). Agents get their own stats (tickets
// assigned to them); supervisors and admins everything. Durations are
// business minutes when calendar_mode=team and the ticket's team has a
// calendar (TKT-035).

type stat struct {
	Median *float64 `json:"median"`
	P90    *float64 `json:"p90"`
}

type tierRow struct {
	Tier        int `json:"tier"`
	Created     int `json:"created"`
	Resolved    int `json:"resolved"`
	EscalatedIn int `json:"escalated_in"`
}

type teamRow struct {
	TeamID   *string `json:"team_id"`
	Name     string  `json:"name"`
	Created  int     `json:"created"`
	Resolved int     `json:"resolved"`
}

// Report is the Report schema.
type Report struct {
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
	CalendarMode string    `json:"calendar_mode"`
	Totals       struct {
		Created  int `json:"created"`
		Resolved int `json:"resolved"`
		Closed   int `json:"closed"`
		Reopened int `json:"reopened"`
	} `json:"totals"`
	FirstResponseMinutes stat `json:"first_response_minutes"`
	ResolutionMinutes    stat `json:"resolution_minutes"`
	SLA                  struct {
		Met      int `json:"met"`
		Breached int `json:"breached"`
	} `json:"sla"`
	ByTier []tierRow `json:"by_tier"`
	ByTeam []teamRow `json:"by_team"`
	CSAT   struct {
		Average *float64 `json:"average"`
		Count   int      `json:"count"`
	} `json:"csat"`
}

// ReportQuery selects the report scope.
type ReportQuery struct {
	From, To     time.Time
	TeamID       string
	CalendarMode string // 24x7 (default) or team
}

// percentile is the nearest-rank percentile of sorted values (nil when empty).
func percentile(sorted []float64, p float64) *float64 {
	if len(sorted) == 0 {
		return nil
	}
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	rank = min(max(rank, 1), len(sorted))
	v := math.Round(sorted[rank-1]*100) / 100
	return &v
}

// Summary computes the report for p.
func (s *Service) Summary(ctx context.Context, p *domain.Principal, rq ReportQuery) (*Report, error) {
	if rq.CalendarMode == "" {
		rq.CalendarMode = "24x7"
	}
	scope, args := "1=1", []any{}
	if !p.IsSupervisorOrAdmin() {
		scope, args = "t.assignee_id = ?", []any{p.UserID}
	}
	if rq.TeamID != "" {
		scope += " AND t.team_id = ?"
		args = append(args, rq.TeamID)
	}
	q := s.db.Read()
	rows, err := q.QueryContext(ctx, `SELECT `+ticketCols+` FROM tickets t WHERE `+scope+` AND (
		(t.created_at >= ? AND t.created_at < ?) OR (t.resolved_at >= ? AND t.resolved_at < ?) OR (t.closed_at >= ? AND t.closed_at < ?))`,
		append(args, rq.From, rq.To, rq.From, rq.To, rq.From, rq.To)...)
	if err != nil {
		return nil, err
	}
	var tickets []*ticketRow
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		tickets = append(tickets, t)
	}
	rows.Close()
	in := func(ts time.Time) bool { return !ts.Before(rq.From) && ts.Before(rq.To) }
	cal := func(t *ticketRow) *directory.Calendar {
		if rq.CalendarMode != "team" {
			return nil
		}
		c, _ := s.dir.TeamCalendar(ctx, t.TeamID)
		return c
	}
	rep := &Report{From: rq.From, To: rq.To, CalendarMode: rq.CalendarMode, ByTier: []tierRow{}, ByTeam: []teamRow{}}
	tiers := map[int]*tierRow{1: {Tier: 1}, 2: {Tier: 2}, 3: {Tier: 3}}
	teams := map[string]*teamRow{}
	team := func(id string) *teamRow {
		if r, ok := teams[id]; ok {
			return r
		}
		r := &teamRow{TeamID: strOrNil(id), Name: "Triage"}
		if id != "" {
			if tm, err := s.dir.Team(ctx, id); err == nil {
				r.Name = tm.Name
			}
		}
		teams[id] = r
		return r
	}
	var frt, res []float64
	var csatSum, csatN int
	for _, t := range tickets {
		if in(t.CreatedAt) {
			rep.Totals.Created++
			tiers[t.Tier].Created++
			team(t.TeamID).Created++
			if t.FirstRespondedAt.Valid {
				frt = append(frt, directory.BusinessDuration(cal(t), t.CreatedAt, t.FirstRespondedAt.Time).Minutes())
			}
		}
		if t.ResolvedAt.Valid && in(t.ResolvedAt.Time) {
			rep.Totals.Resolved++
			tiers[t.Tier].Resolved++
			team(t.TeamID).Resolved++
			d := directory.BusinessDuration(cal(t), t.CreatedAt, t.ResolvedAt.Time).Minutes() - float64(t.SLAPausedMinutes)
			res = append(res, math.Max(0, d))
			if t.ResolutionDueAt.Valid {
				if t.ResolvedAt.Time.After(t.ResolutionDueAt.Time) || t.SLAState["resolution.breached"] != "" {
					rep.SLA.Breached++
				} else {
					rep.SLA.Met++
				}
			}
			if t.CSATScore > 0 {
				csatSum += t.CSATScore
				csatN++
			}
		}
		if t.ClosedAt.Valid && in(t.ClosedAt.Time) {
			rep.Totals.Closed++
		}
	}
	sort.Float64s(frt)
	sort.Float64s(res)
	rep.FirstResponseMinutes = stat{Median: percentile(frt, 50), P90: percentile(frt, 90)}
	rep.ResolutionMinutes = stat{Median: percentile(res, 50), P90: percentile(res, 90)}
	rep.CSAT.Count = csatN
	if csatN > 0 {
		avg := math.Round(float64(csatSum)/float64(csatN)*100) / 100
		rep.CSAT.Average = &avg
	}
	// Reopens and escalations come from the event log in the range.
	erows, err := q.QueryContext(ctx, `SELECT e.type, e.data FROM ticket_events e JOIN tickets t ON t.id = e.ticket_id
		WHERE e.type IN ('ticket.status_changed', 'ticket.escalated') AND e.created_at >= ? AND e.created_at < ? AND `+scope,
		append([]any{rq.From, rq.To}, args...)...)
	if err != nil {
		return nil, err
	}
	for erows.Next() {
		var typ string
		var data sql.NullString
		if err := erows.Scan(&typ, &data); err != nil {
			erows.Close()
			return nil, err
		}
		var d map[string]any
		_ = json.Unmarshal([]byte(data.String), &d)
		switch typ {
		case "ticket.status_changed":
			if (d["from"] == "resolved" || d["from"] == "closed") && d["to"] == "open" {
				rep.Totals.Reopened++
			}
		case "ticket.escalated":
			if to, ok := d["to_tier"].(float64); ok && tiers[int(to)] != nil {
				tiers[int(to)].EscalatedIn++
			}
		}
	}
	erows.Close()
	for i := 1; i <= 3; i++ {
		rep.ByTier = append(rep.ByTier, *tiers[i])
	}
	for _, r := range teams {
		rep.ByTeam = append(rep.ByTeam, *r)
	}
	sort.Slice(rep.ByTeam, func(i, j int) bool { return rep.ByTeam[i].Name < rep.ByTeam[j].Name })
	return rep, nil
}

func (m *Module) reportSummary(w http.ResponseWriter, r *http.Request) error {
	p := principal(r)
	if authz.CanAdmin(p, authz.ResOwnStats, authz.OpRead) != nil {
		return problems.Forbidden("")
	}
	qv := r.URL.Query()
	now := m.svc.clock()
	rq := ReportQuery{From: now.Add(-30 * 24 * time.Hour), To: now, TeamID: qv.Get("team_id"), CalendarMode: qv.Get("calendar_mode")}
	var errs []problems.FieldError
	for k, dst := range map[string]*time.Time{"from": &rq.From, "to": &rq.To} {
		if v := qv.Get(k); v != "" {
			t, err := time.Parse(time.RFC3339Nano, v)
			if err != nil {
				errs = append(errs, problems.Field(k, "date_time", "RFC 3339 timestamp"))
				continue
			}
			*dst = t.UTC()
		}
	}
	if rq.CalendarMode != "" && rq.CalendarMode != "24x7" && rq.CalendarMode != "team" {
		errs = append(errs, problems.Field("calendar_mode", "enum", "24x7 or team"))
	}
	if !rq.To.After(rq.From) {
		errs = append(errs, problems.Field("to", "range", "must be after from"))
	}
	if len(errs) > 0 {
		return problems.Validation(errs...)
	}
	rep, err := m.svc.Summary(r.Context(), p, rq)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, rep)
	return nil
}
