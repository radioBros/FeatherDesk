// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/radioBros/FeatherDesk/internal/authz"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/events"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// EscalateInput is EscalateRequest.
type EscalateInput struct {
	Reason       string `json:"reason"`
	TargetTeamID optStr `json:"target_team_id"`
}

// MaxReason is the escalation reason limit (OpenAPI EscalateRequest).
const MaxReason = 2000

// Escalate raises the tier by one (dir=+1) or lowers it (dir=-1), optionally
// moving the ticket to another team in the same step (lifecycle §3, TKT-010).
// The assignee is cleared when M-4 fails at the new tier/team (escalation
// only; de-escalation keeps the assignee unless the team changes). `new`
// becomes `open`. One ticket.escalated/deescalated event plus an internal
// system message carrying the reason.
func (s *Service) Escalate(ctx context.Context, p *domain.Principal, id string, dir int, in EscalateInput) (Outcome, error) {
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || utf8.RuneCountInString(reason) > MaxReason {
		return Outcome{}, problems.Validation(problems.Field("reason", "required", "1..2000 characters"))
	}
	pol := s.Policy(ctx)
	var out Outcome
	if err := s.warm(ctx); err != nil {
		return Outcome{}, err
	}
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		t, err := lockTicket(ctx, tx, id)
		if err != nil {
			return err
		}
		now := s.clock()
		v := t.view(pol, now)
		if !authz.CanSeeTicket(p, v) {
			return problems.NotFound()
		}
		act, typ := authz.ActEscalate, events.TicketEscalated
		if dir < 0 {
			act, typ = authz.ActDeescalate, events.TicketDeescalated
		}
		newTier := t.Tier + dir
		if newTier > domain.MaxTier || newTier < domain.MinTier {
			if t.Status == domain.StatusClosed {
				return problems.Forbidden("closed tickets are immutable")
			}
			return problems.Conflict(fmt.Sprintf("the ticket is already at tier %d", t.Tier))
		}
		if err := authz.Can(p, act, v); err != nil {
			return err
		}
		m := &mutation{t: t}
		fromTier, fromTeam := t.Tier, t.TeamID
		t.Tier = newTier
		if in.TargetTeamID.Set && in.TargetTeamID.str() != t.TeamID {
			team := in.TargetTeamID.str()
			if team != "" {
				if _, err := s.dir.Team(ctx, team); err != nil {
					return problems.Validation(problems.Field("target_team_id", "not_found", "unknown team"))
				}
			}
			t.TeamID = team
		}
		// The escalated/deescalated event carries the assignment change when
		// the assignee is cleared; it is the only event of the mutation.
		var cleared string
		if dir > 0 || t.TeamID != fromTeam {
			probe := &mutation{t: t}
			if err := s.revalidateAssignee(ctx, tx, probe); err != nil {
				return err
			}
			if len(probe.events) > 0 {
				cleared = probe.events[0].data.(events.Assignment).From
			}
		}
		if t.Status == domain.StatusNew {
			if _, err := s.applyStatus(ctx, tx, t, actorStaff, domain.StatusOpen, statusOpts{}); err != nil {
				return err
			}
		}
		verb := "Escalated"
		if dir < 0 {
			verb = "De-escalated"
		}
		body := fmt.Sprintf("%s from L%d to L%d: %s", verb, fromTier, newTier, reason)
		if err := insertMessage(ctx, tx, &msgRow{TicketID: t.ID, AuthorID: p.UserID, Kind: domain.MessageSystem, Channel: domain.ChannelAgent, Body: body, CreatedAt: now}); err != nil {
			return err
		}
		data := map[string]any{"from_tier": fromTier, "to_tier": newTier, "from_team": nilIfEmpty(fromTeam), "to_team": nilIfEmpty(t.TeamID), "reason": reason}
		if cleared != "" {
			data["unassigned"] = cleared
		}
		m.add(typ, data)
		if cleared != "" { // D-19 §A: the team strategy picks a new owner
			if err := s.autoAssign(ctx, tx, m, cleared); err != nil {
				return err
			}
		}
		if err := s.flush(ctx, tx, m, p.UserID); err != nil {
			return err
		}
		out = Outcome{TicketID: t.ID}
		return nil
	})
	return out, err
}
