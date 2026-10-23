// SPDX-License-Identifier: AGPL-3.0-only

package slack

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/radioBros/FeatherDesk/internal/events"
)

// Block is one Block Kit block (a JSON object).
type Block = map[string]any

// Triggers a route may select (OpenAPI SlackTrigger). message.created is
// split by author so a channel can follow customer replies only.
const (
	TrigCreated   = "ticket.created"
	TrigStatus    = "ticket.status_changed"
	TrigAssigned  = "ticket.assigned"
	TrigPriority  = "ticket.priority_changed"
	TrigEscalated = "ticket.escalated" // escalated and de-escalated
	TrigCustomer  = "message.customer"
	TrigStaff     = "message.staff"
	TrigNote      = "note.created" // needs include_internal
	TrigSLAWarn   = "sla.warning"
	TrigSLABreach = "sla.breached"
	TrigCSAT      = "ticket.csat"
)

// Triggers lists every trigger in display order.
var Triggers = []string{TrigCreated, TrigStatus, TrigAssigned, TrigPriority, TrigEscalated, TrigCustomer, TrigStaff, TrigNote,
	TrigSLAWarn, TrigSLABreach, TrigCSAT}

// eventTypes are the stored event types the consumer listens to.
var eventTypes = map[string]bool{
	events.TicketCreated: true, events.TicketStatusChanged: true, events.TicketAssigned: true, events.TicketPriorityChanged: true,
	events.TicketEscalated: true, events.TicketDeescalated: true, events.MessageCreated: true, events.NoteCreated: true,
	events.SLAWarning: true, events.SLABreached: true, events.TicketCSAT: true,
}

// envelope is the webhook envelope (events_spec §3) as Slack reads it.
type envelope struct {
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	Data      struct {
		Ticket struct {
			ID       string `json:"id"`
			Number   int64  `json:"number"`
			Ref      string `json:"ref"`
			Subject  string `json:"subject"`
			Status   string `json:"status"`
			Priority string `json:"priority"`
			Channel  string `json:"channel"`
			Project  struct {
				Key  string `json:"key"`
				Name string `json:"name"`
			} `json:"project"`
			Requester *person `json:"requester"`
			Assignee  *person `json:"assignee"`
		} `json:"ticket"`
		Actor   *person         `json:"actor"`
		Changes json.RawMessage `json:"changes"`
		Message *envMessage     `json:"message"`
	} `json:"data"`
}

type envMessage struct {
	Kind           string  `json:"kind"`
	BodyMD         string  `json:"body_md"`
	Author         *person `json:"author"`
	ViaIntegration bool    `json:"via_integration"`
	Attachments    []any   `json:"attachments"`
}

type person struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

func (p *person) name(fallback string) string {
	if p == nil || p.Name == "" {
		return fallback
	}
	return p.Name
}

// trigger maps an event to the route trigger it fires ("" = none).
func (env *envelope) trigger() string {
	switch env.Type {
	case events.TicketDeescalated:
		return TrigEscalated
	case events.MessageCreated:
		if env.Data.Message == nil {
			return ""
		}
		if env.Data.Message.ViaIntegration || (env.Data.Message.Author != nil && env.Data.Message.Author.Kind == "customer") {
			return TrigCustomer
		}
		return TrigStaff
	}
	return env.Type
}

// Labels shown in posts. Posts are in English: a channel is shared by people
// with different UI languages (D-21 §11).
var (
	statusLabel   = map[string]string{"new": "New", "open": "Open", "pending": "Pending", "on_hold": "On hold", "resolved": "Resolved", "closed": "Closed"}
	priorityLabel = map[string]string{"low": "Low", "normal": "Normal", "high": "High", "urgent": "Urgent"}
	priorityIcon  = map[string]string{"low": ":white_circle:", "normal": ":large_blue_circle:", "high": ":large_orange_circle:", "urgent": ":red_circle:"}
)

func label(m map[string]string, k string) string {
	if v, ok := m[k]; ok {
		return v
	}
	if k == "" {
		return "—"
	}
	return k
}

// esc escapes text for mrkdwn (&, <, >), see Slack "Formatting text".
func esc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// clip shortens s to n runes on a word boundary, with an ellipsis.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)[:n]
	cut := string(r)
	if i := strings.LastIndexAny(cut, " \n"); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " \n.,;:") + "…"
}

// quote renders an excerpt of a message body as a mrkdwn block quote.
func quote(body string) string {
	lines := strings.Split(esc(clip(body, 600)), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, "> "+l)
	}
	return strings.Join(out, "\n")
}

// render builds the post for env. ticketURL opens the ticket in the agent
// panel. reply = the post goes into the ticket's thread (shorter: no title).
func render(env *envelope, trig, ticketURL string, includeBody, reply bool) Message {
	t := env.Data.Ticket
	ref := t.Ref
	if ref == "" {
		ref = fmt.Sprintf("#%d", t.Number)
	}
	actor := env.Data.Actor.name("FeatherDesk")
	title := fmt.Sprintf("<%s|*%s* %s>", ticketURL, esc(ref), esc(clip(t.Subject, 150)))
	var ch map[string]any
	_ = json.Unmarshal(env.Data.Changes, &ch)
	str := func(k string) string { s, _ := ch[k].(string); return s }

	var line, fallback, excerpt string
	switch trig {
	case TrigCreated:
		line = fmt.Sprintf(":ticket: New ticket from *%s*", esc(t.Requester.name("a customer")))
		fallback = fmt.Sprintf("New ticket %s: %s", ref, t.Subject)
		if env.Data.Message != nil {
			excerpt = env.Data.Message.BodyMD
		}
	case TrigStatus:
		line = fmt.Sprintf(":arrows_counterclockwise: Status *%s* → *%s* by %s", label(statusLabel, str("from")), label(statusLabel, str("to")), esc(actor))
		fallback = fmt.Sprintf("%s status: %s", ref, label(statusLabel, str("to")))
	case TrigAssigned:
		if str("to") == "" {
			line = fmt.Sprintf(":bust_in_silhouette: Unassigned by %s", esc(actor))
		} else {
			line = fmt.Sprintf(":bust_in_silhouette: Assigned to *%s* by %s", esc(t.Assignee.name("someone")), esc(actor))
		}
		fallback = fmt.Sprintf("%s assigned to %s", ref, t.Assignee.name("nobody"))
	case TrigPriority:
		line = fmt.Sprintf("%s Priority *%s* → *%s* by %s", label(priorityIcon, str("to")), label(priorityLabel, str("from")), label(priorityLabel, str("to")), esc(actor))
		fallback = fmt.Sprintf("%s priority: %s", ref, label(priorityLabel, str("to")))
	case TrigEscalated:
		from, _ := ch["from_tier"].(float64)
		to, _ := ch["to_tier"].(float64)
		verb, icon := "Escalated", ":arrow_double_up:"
		if env.Type == events.TicketDeescalated {
			verb, icon = "De-escalated", ":arrow_double_down:"
		}
		line = fmt.Sprintf("%s %s *L%d* → *L%d* by %s", icon, verb, int(from), int(to), esc(actor))
		if r := str("reason"); r != "" {
			line += "\n_" + esc(clip(r, 300)) + "_"
		}
		fallback = fmt.Sprintf("%s %s to L%d", ref, strings.ToLower(verb), int(to))
	case TrigCustomer, TrigStaff, TrigNote:
		m := env.Data.Message
		who := "Someone"
		if m != nil {
			who = m.Author.name(who)
		}
		switch trig {
		case TrigCustomer:
			line = fmt.Sprintf(":speech_balloon: *%s* replied", esc(who))
		case TrigStaff:
			line = fmt.Sprintf(":outbox_tray: *%s* replied to the customer", esc(who))
		default:
			line = fmt.Sprintf(":lock: *%s* added an internal note", esc(who))
		}
		fallback = fmt.Sprintf("%s: %s", ref, strings.TrimPrefix(strings.ReplaceAll(line, "*", ""), ":"))
		if m != nil {
			excerpt = m.BodyMD
			if excerpt == "" && len(m.Attachments) > 0 {
				excerpt = fmt.Sprintf("(%d attachment(s))", len(m.Attachments))
			}
		}
	case TrigSLAWarn, TrigSLABreach:
		which := "Resolution"
		if str("which") == "first_response" {
			which = "First response"
		}
		due := ""
		if d, err := time.Parse(time.RFC3339, str("due_at")); err == nil {
			due = fmt.Sprintf(" (due <!date^%d^{date_short_pretty} {time}|%s>)", d.Unix(), d.UTC().Format("2006-01-02 15:04 UTC"))
		}
		if trig == TrigSLAWarn {
			line = fmt.Sprintf(":hourglass_flowing_sand: %s SLA is about to breach%s", which, due)
		} else {
			line = fmt.Sprintf(":rotating_light: %s SLA *breached*%s", which, due)
		}
		fallback = fmt.Sprintf("%s: %s SLA %s", ref, strings.ToLower(which), map[bool]string{true: "warning", false: "breached"}[trig == TrigSLAWarn])
	case TrigCSAT:
		score, _ := ch["score"].(float64)
		stars := strings.Repeat(":star:", max(0, min(5, int(score))))
		line = fmt.Sprintf(":bar_chart: Satisfaction rating %s (%d/5) from *%s*", stars, int(score), esc(t.Requester.name("the customer")))
		fallback = fmt.Sprintf("%s rated %d/5", ref, int(score))
	default:
		line = esc(env.Type)
		fallback = ref + ": " + env.Type
	}

	text := line
	if !reply {
		text = title + "\n" + line
	}
	blocks := []Block{{"type": "section", "text": mrkdwn(clip(text, 2900))}}
	if includeBody && strings.TrimSpace(excerpt) != "" {
		blocks = append(blocks, Block{"type": "section", "text": mrkdwn(quote(excerpt))})
	}
	if !reply {
		ctx := []any{
			mrkdwn(fmt.Sprintf("%s *%s*", label(priorityIcon, t.Priority), label(priorityLabel, t.Priority))),
			mrkdwn("Status: *" + label(statusLabel, t.Status) + "*"),
			mrkdwn("Assignee: *" + esc(t.Assignee.name("unassigned")) + "*"),
		}
		if t.Project.Name != "" {
			ctx = append(ctx, mrkdwn("Project: *"+esc(t.Project.Name)+"*"))
		}
		blocks = append(blocks,
			Block{"type": "context", "elements": ctx},
			Block{"type": "actions", "elements": []any{map[string]any{
				"type": "button", "text": map[string]any{"type": "plain_text", "text": "Open ticket"}, "url": ticketURL, "action_id": "open_ticket",
			}}})
	}
	return Message{Text: clip(fallback, 300), Blocks: blocks}
}

func mrkdwn(s string) map[string]any { return map[string]any{"type": "mrkdwn", "text": s} }
