// SPDX-License-Identifier: AGPL-3.0-only

package directory

import (
	"context"
	"database/sql"
)

// NotifyTargets is who a team notification reaches (email_config_decision):
// the team mailbox and/or the active members whose effective tier covers the
// ticket's tier, depending on notify_mode. Exported for the notify module.
type NotifyTargets struct {
	Mode      string   // agents | mailbox | both
	Mailbox   string   // notification_email ("" when unset)
	MemberIDs []string // active staff, effective tier ≥ tier
}

// TeamNotifyTargets resolves the notification targets of teamID for a
// ticket at tier (teamID "" = triage: supervisors and admins).
func (d *Directory) TeamNotifyTargets(ctx context.Context, teamID string, tier int) (NotifyTargets, error) {
	q := d.db.Read()
	var out NotifyTargets
	var rows *sql.Rows
	var err error
	if teamID == "" {
		out.Mode = "agents"
		rows, err = q.QueryContext(ctx, `SELECT id FROM users WHERE kind = 'staff' AND status = 'active' AND role IN ('admin', 'supervisor') ORDER BY id`)
	} else {
		t, terr := d.Team(ctx, teamID)
		if terr != nil {
			return out, terr
		}
		out.Mode = t.NotifyMode
		if t.NotificationEmail != nil {
			out.Mailbox = *t.NotificationEmail
		}
		if out.Mode == "mailbox" {
			return out, nil
		}
		rows, err = q.QueryContext(ctx, `SELECT u.id FROM users u JOIN team_members m ON m.user_id = u.id AND m.team_id = ?
			WHERE u.kind = 'staff' AND u.status = 'active' AND (u.role IN ('admin', 'supervisor') OR u.tier >= ?) ORDER BY u.id`, teamID, tier)
	}
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return out, err
		}
		out.MemberIDs = append(out.MemberIDs, id)
	}
	return out, rows.Err()
}
