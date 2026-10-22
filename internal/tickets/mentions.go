// SPDX-License-Identifier: AGPL-3.0-only

package tickets

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/radioBros/FeatherDesk/internal/authz"
	"github.com/radioBros/FeatherDesk/internal/domain"
	"github.com/radioBros/FeatherDesk/internal/problems"
	"github.com/radioBros/FeatherDesk/internal/store"
)

// @mentions in internal notes (TKT-033): `@[Name](user-id)` tokens from the
// editor, or `@handle` where handle is the local part of a staff e-mail.

var (
	mentionTokenRe  = regexp.MustCompile(`@\[[^\]\n]{1,120}\]\(([0-9a-fA-F-]{36})\)`)
	mentionHandleRe = regexp.MustCompile(`(?:^|[\s(])@([a-zA-Z0-9._+-]{1,64})`)
)

// parseMentions returns explicit user IDs and bare handles in body.
func parseMentions(body string) (ids, handles []string) {
	for _, m := range mentionTokenRe.FindAllStringSubmatch(body, 50) {
		id := strings.ToLower(m[1])
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	stripped := mentionTokenRe.ReplaceAllString(body, " ")
	for _, m := range mentionHandleRe.FindAllStringSubmatch(stripped, 50) {
		h := strings.ToLower(strings.TrimRight(m[1], "."))
		if h != "" && !slices.Contains(handles, h) {
			handles = append(handles, h)
		}
	}
	return ids, handles
}

// resolveMentions maps mentions to staff IDs; every mentioned staff member
// must be able to see the ticket, otherwise the note is refused with 422
// listing them. Unknown handles are plain text and ignored.
func (s *Service) resolveMentions(ctx context.Context, tx *store.Tx, pol authz.TicketPolicy, t *ticketRow, body string) ([]string, error) {
	ids, handles := parseMentions(body)
	if len(ids) == 0 && len(handles) == 0 {
		return nil, nil
	}
	var found []string
	for _, id := range ids {
		if validID(id) {
			found = append(found, id)
		}
	}
	for _, h := range handles {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM users WHERE kind = 'staff' AND status = 'active' AND email LIKE ? ESCAPE '\'`, escapeLike(h)+"@%")
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err == nil && !slices.Contains(found, id) {
				found = append(found, id)
			}
		}
		rows.Close()
	}
	var out, invisible []string
	for _, id := range found {
		st, err := loadStaff(ctx, tx, id)
		if err != nil {
			continue // not staff: plain text
		}
		p := st.principal()
		if st.Status != domain.UserActive || !authz.CanSeeTicket(p, t.view(pol, s.clock())) {
			invisible = append(invisible, st.Name)
			continue
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	if len(invisible) > 0 {
		return nil, &problems.Error{Slug: problems.SlugValidation, Fields: []problems.FieldError{
			problems.Field("body_md", "mention_not_visible", "cannot see this ticket: "+strings.Join(invisible, ", "))},
			Extra: map[string]any{"invisible": invisible}}
	}
	return out, nil
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
