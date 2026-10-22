# Automation: assignment, rules and macros

> Part of the [FeatherDesk manual](../DOCUMENTATION.md). The overview is in [Admin guide → Automation and assignment](../DOCUMENTATION.md#716-automation-and-assignment-support-setup-supervisor) and [Agent guide → Macros and availability](../DOCUMENTATION.md#89-macros-and-availability); this page has every detail.

FeatherDesk can do routine ticket work for you in three ways:

- **Automatic assignment.** A team can hand its unassigned tickets to its agents in turn, or to whoever has the least work.
- **Automation rules.** *When this happens and these conditions hold, do that.* Rules run either when something happens to a
  ticket or on an hourly schedule, for example "pending for 3 days".
- **Macros.** An agent applies a bundle of reply, status, tags, assignee and priority in one click.

Everything automatic is done by the **Automation** system user. It shows in the ticket timeline as *Automation rule applied*, with
the rule's name and what each action did. Customers never see rule names, automation notes or the timeline entry.

This guide covers agents and admins. The last section has API examples for integrators.

## 1. Automatic assignment

Every team has an **Automatic assignment** setting (*Administration → People → Teams*, edit a team; supervisors and admins):

| Strategy | What it does |
|---|---|
| **Manual** (default) | Nothing. Unassigned tickets wait in the team queue for an agent to pick them up |
| **Round robin** | Takes turns over the eligible members, in a fixed order, so each gets the same number of tickets |
| **Least busy** | Picks the eligible member with the fewest open tickets, counting every ticket they own that is not resolved or closed. A tie goes to the same member each time |

A strategy only fills an empty owner: a ticket that already has one is left alone.

**Eligible** means the same thing as for manual assignment (rule M-4), plus one condition:

- the account is active;
- the agent is a member of the ticket's team;
- the agent's tier is at least the ticket's tier (supervisors and admins count as tier 3);
- the agent is **not marked away**.

The strategy runs when a ticket of the team ends up without an owner:

- a new ticket arrives in the team (portal, agent, API, email);
- a ticket is transferred to the team;
- an escalation or de-escalation removes the owner because they no longer qualify for the new tier or team;
- someone unassigns the ticket. The previous owner is skipped if anyone else is eligible;
- an agent loses the team, the tier or their account, and their tickets are unassigned.

If nobody is eligible, for example because everyone is away, the ticket stays unassigned.

### Away

Agents set their own availability from the **user menu** (top right, the **Away** switch). While away, an agent gets no
automatic assignments, but anyone can still assign them a ticket by hand, and a rule's *Assign* to a named agent still
works. The switch stays on until the agent turns it off.

## 2. Automation rules

Supervisors and admins manage rules in **Administration → Support setup → Automation rules**. A rule can be limited to one
[project](projects.md): it then runs only on that project's tickets, and that project's **project admins** manage it too (they
see and change only their projects' rules; global rules and the order stay with supervisors and admins).

A rule has:

- a **name** (up to 120 characters);
- an **event** (*When*): what makes it run;
- up to 20 **conditions**, combined with **all of them** (every condition must hold) or **any of them** (one is enough). A rule
  without conditions always applies;
- up to **10 actions**;
- an **Active** switch;
- **Stop processing later rules**: when this rule applies, later rules are skipped for that event.

Rules run in the order of the list. Use the arrows to reorder them.

### Events

| Event | Runs when |
|---|---|
| **Ticket created** | A ticket is created, through any channel |
| **Customer replied** | A customer posts a public reply. Staff replies and automated replies never count |
| **Ticket updated** | A person changes the status, priority, assignee, team, tier, category, tags, custom fields or subject. A change that touches several fields runs the rules once |
| **Time-based (hourly)** | The hourly check. Needs at least one hours condition |

### Conditions

| Condition | Operators | Notes |
|---|---|---|
| Subject, Message text | contains, does not contain, matches the pattern | Case-insensitive. The text or pattern can be up to 500 characters; a pattern uses RE2 syntax. *Message text* is the customer's message for *Customer replied*, otherwise the first message of the ticket |
| Channel, Priority, Status | is, is not | One value or a list (up to 50) |
| Support level (tier) | is, is not, at least, at most | 1–3 |
| Category, Team, Organization | is, is not | |
| Project | is, is not | Project ids or keys (the API stores ids) |
| Requester email domain | is, is not | For example `example.com` |
| Tag | is present, is missing | |
| Custom field | equals, contains | Pick the field by its label (the API uses its key). For a multi-select, both operators match one of the chosen options |
| Within business hours | is yes / no | Uses the business calendar of the ticket's team. A team without a calendar is always in business hours |
| Hours since last update | at least N | Time-based rules only. N is 1–8760 |
| Hours in current status | at least N | Time-based rules only. N is 1–8760 |

### Actions

| Action | Notes |
|---|---|
| Set priority, Set category, Move to team, Set status, Set support level | Moving a ticket to another team keeps the owner only if they still qualify, and then runs that team's assignment strategy. Status changes follow the normal status rules. *Closed* from a working status resolves the ticket first. Required custom fields still block *Resolved* |
| Add tag, Remove tag | |
| Assign | *Round robin*, *Least busy agent*, or *A specific agent* who must be allowed to own the ticket (M-4; the *Away* switch is not checked). With a strategy, a ticket that already has an owner is left *already so* |
| Add internal note | Staff only, up to 10 000 characters. Never shown to customers |
| Send canned reply | Sends a canned response as a public reply, with its placeholders filled in (`{{agent.name}}` is the ticket's assignee, empty when unassigned). An automated reply does not count as the first response for the SLA, and does not move a *New* ticket to *Open* |
| Escalate one level | Raises the tier by one, like the Escalate button, with the rule's name as the reason |
| Add watcher | A staff member |

Each action ends as *applied*, *already so* (nothing to change) or *skipped*. If an action can't be done, for example because
the named agent can't own the ticket, it is **skipped** with the reason, and the rule's other actions still run. On a **closed**
ticket, trigger rules skip every action except *Add internal note* ("the ticket is closed").

The timeline entry *Automation rule applied* is written only when at least one action of the rule was applied. It then lists
every action with its outcome, including the skipped ones. A rule whose actions were all skipped or already so leaves no
entry; use the dry run to see why.

### Safety rails

- **No loops.** Changes made by automation never trigger rules, not even other rules. A rule that sets the priority to urgent
  can't trigger itself, or a rule that reacts to urgent tickets.
- **At most 10 actions per event**, across all rules. A rule that is cut short is marked *truncated* in the timeline.
- **Rules run in the background**, a moment after the change, through the durable job queue. If the server stops, they run
  after the restart, and never twice.
- **Time rules fire once per ticket**, then wait until the ticket changes again. A change made by a time rule doesn't count. So
  "pending for 72 hours → add a reminder note" adds one note, not one every hour. Closed tickets are never checked.

### Test a rule (dry run)

In the rules list, choose the **Dry run** button (the flask icon) of a rule, enter a **Ticket number** and choose **Run test**. You
see which conditions are met and what each action would do: *applied*, *already so* or *skipped*, with the reason. Nothing is
written. The dry run tests the saved rule, and works for inactive rules too, so you can test a rule before you turn it on.

### Examples

- **VIP routing.** When *Ticket created*. Conditions: *Requester email domain is* `bigcustomer.com`. Actions: *Move to team* VIP,
  *Set priority* high, *Add tag* `vip`.
- **Outside office hours.** When *Ticket created*. Condition: *Within business hours is* No. Action: *Send canned reply* using
  "We'll answer tomorrow morning".
- **Chase silent customers.** When *Time-based (hourly)*. Conditions: *Status is* pending, *Hours in current status at least* 72.
  Action: *Add internal note* "Customer silent for 3 days, consider closing". Add a second rule at 168 hours with *Set status*
  closed.
- **Refund keyword.** When *Customer replied*. Condition: *Message text matches the pattern* `\brefund(s|ed)?\b`. Actions: *Add tag*
  `refund`, *Add watcher* (the billing lead).

## 3. Macros

A macro is a reusable bundle. It can have any of:

- a reply, either **public** or an **internal** note (up to 64 KiB). It can use the canned-response placeholders
  `{{customer.name}}` (alias `{{requester.name}}`), `{{ticket.number}}`, `{{ticket.ref}}` and `{{agent.name}}` (whoever applies the macro);
- a status;
- a priority;
- tags to add, and tags to remove (up to 20 each);
- an assignee: *me* (whoever applies it), *nobody*, or a named agent.

A macro needs a name (up to 120 characters) and at least one of these parts.

There are two kinds:

- **Personal macros** (**My macros**, in the user menu at the top right, under the *Away* switch) belong to one agent, and only
  that agent sees and applies them.
- **Shared macros** (*Administration → Support setup → Shared macros*) are offered to every agent. Only supervisors and admins can
  create, edit or delete them, and the project admins of a project for the macros limited to it.

A macro can be limited to one [project](projects.md); it is then offered and applied only on that project's tickets.

To apply a macro, open a ticket and choose the **Macros** button in the ticket header (next to the status, priority and tier
chips), then pick one. The button shows when you may reply, add a note or set the status. It applies as you, in one step: the
reply first, then priority, tags, assignee and status. Unlike an automated reply, a macro's public reply is your reply: it counts
as the first response for the SLA and moves a *New* ticket to *Open*. You need permission for every part. For example, an agent can't apply a macro
that sets *urgent* when urgent needs a supervisor. If any part isn't allowed, nothing changes and you see why.

## 4. API

Every route is in `api/openapi.yaml`. Rule and shared-macro routes need a supervisor or admin; with a personal access token they
need the `admin` scope, which only admins can hold. Personal macro routes need `tickets:read` (list) or `tickets:write` (create,
change, delete, apply). Mutations from a browser session also need `X-CSRF-Token`.

### Rules

```http
POST /api/v1/admin/automation/rules
Authorization: Bearer fd_pat_…

{
  "name": "VIP routing",
  "event": "ticket.created",
  "match": "all",
  "conditions": [
    {"field": "requester_domain", "op": "is", "value": "bigcustomer.com"},
    {"field": "subject", "op": "regex", "value": "outage|down"}
  ],
  "actions": [
    {"type": "set_team", "value": "0199…team-id"},
    {"type": "set_priority", "value": "high"},
    {"type": "assign", "strategy": "least_busy"},
    {"type": "add_tag", "value": "vip"}
  ],
  "stop_processing": true
}
```

The response is `201` with the rule (`id`, `position`, `enabled`, …). Other routes:

| Route | Purpose |
|---|---|
| `GET /admin/automation/rules` | All rules, in execution order |
| `GET`, `PATCH`, `DELETE /admin/automation/rules/{id}` | Read, change (absent fields stay as they are), delete |
| `POST /admin/automation/rules/reorder` | `{"ids": [...]}` lists every rule exactly once, in the new order |
| `POST /admin/automation/rules/{id}/dry-run` | `{"ticket_id": "…"}` returns `{rule_id, ticket_id, matched, conditions[{index, field, matched}], actions[{type, outcome, detail}]}`. `actions` is empty when the rule doesn't match; an unknown ticket is a `422` on `ticket_id` |

A time rule:

```json
{
  "name": "Close silent tickets",
  "event": "time",
  "conditions": [
    {"field": "status", "op": "is", "value": "pending"},
    {"field": "hours_since_status", "op": "gte", "value": 168}
  ],
  "actions": [{"type": "set_status", "value": "closed"}]
}
```

Validation errors are `422` with field paths such as `conditions[1].value` (`pattern` for an invalid regex) or
`actions[0].user_id`.

### Macros

```http
POST /api/v1/agent/macros
{"name": "Need logs", "reply_kind": "public", "reply_md": "Hi {{customer.name}}, could you send the logs?",
 "status": "pending", "add_tags": ["waiting-logs"], "assignee": "me"}

POST /api/v1/agent/tickets/{id}/macros/{macro_id}/apply
Idempotency-Key: 7c1e…
If-Match: W/"1761040000000000000"
```

`apply` returns `TicketUpdateResult` (`{ticket, visible}`), like `PATCH /agent/tickets/{id}`. If a part isn't allowed, it is a `403`
and nothing changes. An invalid status move is a `409` (`invalid-transition`), and a stale `If-Match` is a `412`.

| Route | Purpose |
|---|---|
| `GET /agent/macros` | Your personal macros plus every shared macro |
| `POST /agent/macros`, `PATCH`/`DELETE /agent/macros/{id}` | Your own personal macros. Another agent's macro is a `404`; a shared one is a `403` |
| `GET`/`POST /admin/macros`, `PATCH`/`DELETE /admin/macros/{id}` | Shared macros (supervisor or admin) |

A macro `PATCH` replaces the whole macro (the body is the same as for `POST`), so send every part you want to keep. A rule
`PATCH` is partial.

### Availability and team strategy

```http
GET   /api/v1/me/availability          → {"away": false, "away_since": null}
PATCH /api/v1/me/availability {"away": true}

PATCH /api/v1/admin/teams/{id} {"name": "Support", "description": "…", "notification_email": null, "notify_mode": "agents",
                                 "calendar_id": null, "assignment_strategy": "round_robin"}
```

`assignment_strategy` is `manual`, `round_robin` or `least_busy`. A team `PATCH` without the field keeps the current strategy,
but every **other** team field is replaced: read the team first (`GET /api/v1/admin/teams`) and send its `description`,
`notification_email`, `notify_mode` and `calendar_id` back, or they are cleared (`notify_mode` falls back to `agents`).

### Timeline and webhooks

An applied rule writes the `ticket.automation_applied` event, under actor id `automation`:

```json
{"rule_id": "…", "rule_name": "VIP routing", "trigger": "ticket.created", "source_event_id": "…",
 "actions": [{"type": "set_priority", "outcome": "applied"}, {"type": "assign", "outcome": "skipped", "detail": "no eligible agent"}]}
```

`truncated: true` is added when the rule was cut short by the 10-action limit. `source_event_id` is absent for time-based rules.
The outcomes are `applied`, `unchanged` (*already so* in the UI) and `skipped`.

It appears in `GET /agent/tickets/{id}/events` and on staff WebSocket topics. It is never sent to customers, and it is not a webhook
event type. The changes a rule makes (`ticket.priority_changed`, `ticket.assigned`, `message.created`, …) are ordinary events
with actor id `automation`. Webhooks receive them with `actor: null`.
