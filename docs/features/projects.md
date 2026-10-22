# Projects

> Part of the [FeatherDesk manual](../DOCUMENTATION.md). The overview is in [Admin guide → Projects](../DOCUMENTATION.md#721-projects-administration--projects-supervisor) and [Security model](../DOCUMENTATION.md#14-security-model) rule 25. This page has every detail of the API and of the admin rules. Design: decision D-20 (`development/decisions/projects_decision.md`).

A **project** groups tickets that belong together: a product, a client, an internal service. Each project has:

- a **key** (`WEB`, `MOB`, `IT`) and its own ticket sequence, so tickets get **references** like `WEB-12`;
- its own **categories**, the **teams** that serve it, optional **SLA overrides** and **inbound mailboxes**;
- **members**: agents only see tickets of the projects they belong to;
- optional **portal branding** (title, introduction, logo, primary colour) for its own entry at `/p/{key}`.

Projects are optional in practice. Every installation has at least one, the **Default project**, and a single-project installation looks and behaves exactly as before.

## 1. Upgrading and the Default project

Migrations `0140`–`0142` (both engines) run on start-up:

- They create the **Default project** with the fixed id `00000000-0000-7000-8000-000000000001`, key `HD`, named after *Branding → Product name*.
- Every existing ticket, category and inbound mailbox moves into it. KB categories stay **global**.
- Every staff user becomes a member, and every team serves it.
- Existing tickets keep their number as their sequence: ticket `#1042` becomes `HD-1042`. The Default project's sequence continues from the global ticket counter, so while it is the only project, refs and numbers stay equal.
- The SLA policies become the **global defaults** (`project_id` null).

You can rename and re-key the Default project (the key only while it has no tickets), deactivate it once another project is active, or delete it when nothing references it.

## 2. Ticket references

| Field | Meaning |
|---|---|
| `number` | The global, instance-wide number. Unique and never reused. Kept for compatibility: `/by-number/{n}` and `[#n]` in email subjects still work |
| `project_id` | The ticket's project |
| `ref` | `KEY-SEQ`, for example `MOB-7`. `SEQ` comes from the project's own counter, allocated in the creating transaction (no gaps on rollback, no duplicates under concurrency) |
| `project` | `{id, key, name, color}` for the chip next to the ticket |

- **Lookups:** `GET /agent/tickets/by-ref/{ref}` and `GET /portal/tickets/by-ref/{ref}`. The key is matched case-insensitively. Unknown, malformed and invisible refs are all **404**.
- **Search:** a `q` that contains a ref (`q=MOB-7`) matches that ticket. A `q` made only of refs matches nothing else; mixed text searches the text and the refs.
- **Email:** subjects carry `[KEY-123]`. Inbound threading accepts `[KEY-123]` (any letter case), the legacy `[#1042]`, `In-Reply-To`/`References` and plus addresses.
- **Templates:** `{{.Ticket.Ref}}` in email templates; `{{ticket.ref}}` in canned responses and macros (next to `{{ticket.number}}`).
- **MCP:** every tool that takes `ticket` accepts a ref, a number or an id. Resources are `ticket://KEY-123` (`ticket://<number>` still resolves).
- **Moving a ticket** (`PATCH /agent/tickets/{id}` with `project_id`) gives it the next ref of the target project. The old ref no longer resolves; `number` is unchanged.

## 3. Choosing a ticket's project

On create (`POST /portal/tickets`, `POST /agent/tickets`, MCP `create_ticket`), in this order:

1. a **follow-up** (reply to a closed ticket) stays in the original's project; a **split** child stays in the parent's;
2. `project_id` (an id or a key);
3. the **mailbox's** project for email;
4. the **category's** project;
5. customers only: the one project open to them, else the default project if it is open to them, else **422** `project_id` `required`;
6. the **default project**: the Default project while active, else the first active project by `sort`, then name.

Checks (all **422**, on the named field):

| Rule | Field / code |
|---|---|
| Unknown project | `project_id` / `not_found` |
| Inactive project (except inbound mail) | `project_id` / `inactive` |
| Agent not a member (supervisors and admins work everywhere) | `project_id` / `not_allowed` |
| Customer: project not portal-visible, or outside the organization's list | `project_id` / `not_allowed` |
| Category of another project | `category_id` / `wrong_project` |
| Team that does not serve the project | `team_id` / `wrong_project` |

**Routing:** a category's default team is used when it serves the project; otherwise the project's `default_team_id`; otherwise triage. A mailbox team that stopped serving its project routes to triage.

**Moving** (`PATCH` with `project_id`) needs the `transfer` action; agents must be members of the target. In the same transaction the ticket gets the next ref of the target, a category of the old project is cleared, a team that does not serve the target is replaced by the target's default team (or triage), an assignee who may not work in the target is unassigned, the SLA due dates are recomputed with the target's policies, and `ticket.project_changed` `{from, to, from_ref, to_ref}` is written (staff-only, also a webhook event type).

## 4. Visibility and permissions

### I-11: agents see only their projects

An agent sees a ticket only if it is in a project they are a member of, **and** the usual rules hold (assignee, team and tier, triage, watcher). Being the assignee or a watcher never opens another project. Supervisors and admins see every project. An agent with no membership sees no ticket.

I-11 is enforced in the SQL visibility predicate (lists, counts, search, detail, by-ref, by-number, assignable, links, reports, WebSocket subscriptions), in the in-memory event check (`CanSeeEvent`: WebSocket fan-out and resume), in email and in-app notification recipients, in mentions (`mention_not_visible`), in staff replies by email, in MCP (it calls the REST API) and in assignment: M-4 now also requires the owner to work in the ticket's project, so assignment, the assignable list and the automatic strategies only pick members, and staff who leave a project are unassigned from its tickets at once.

Events written before the upgrade have no project in their snapshot and count as the Default project.

### Roles in a project

`project_members(user_id, project_id, role)` with `role` = `member` or `project_admin`.

| Action | Agent member | Project admin of P | Project admin of another project | Supervisor | Admin |
|---|---|---|---|---|---|
| See P's tickets (I-11, with team/tier rules) | ✅ | ✅ | ❌ | ✅ | ✅ |
| P's categories | ❌ | ✅ | ❌ | ✅ | ✅ |
| Teams serving P | ❌ | ✅ | ❌ | ✅ | ✅ |
| P's SLA overrides | ❌ | ✅ | ❌ | ✅ | ✅ |
| P's KB categories | ❌ | ✅ | ❌ | ✅ | ✅ |
| P's automation rules | ❌ | ✅ | ❌ | ✅ | ✅ |
| P's shared macros | ❌ | ✅ | ❌ | ✅ | ✅ |
| P's canned responses (without team) | ❌ | ✅ | ❌ | ✅ | ✅ |
| P's portal settings and logo | ❌ | ✅ | ❌ | ✅ | ✅ |
| P's members | ❌ | read only | ❌ | ✅ | ✅ |
| Create or delete projects, change key, active, sort | ❌ | ❌ | ❌ | ✅ | ✅ |
| Anything global (global rules, macros, canned responses, KB categories, SLA defaults, rule order) | ❌ | ❌ | ❌ | ✅ | ✅ |

A project admin has the agent role: the project-admin role widens configuration rights, never ticket visibility. Configuration of a project they do not administer is **404**; a global or member-management write is **403**. Tokens need the `admin` scope for project configuration writes (KB category routes check `kb:write` too, which `admin` implies) and `admin` or `directory:read` for its reads.

`GET /me` reports `allowed_areas` with `project_admin` for agents who administer a project, and `projects: [{project_id, role}]`.

### Customers

- I-3 is unchanged: customers see their own tickets (and organization managers their organization's) in every project.
- They can **open** tickets only in active, portal-visible projects. An organization's `project_ids` limits that further; an empty list means every portal-visible project.
- Portal categories and custom fields follow the same limit.

### Staff memberships

- `POST /admin/staff` takes `projects: [{project_id, role}]` (`project_id` also accepts a key, `role` defaults to `member`). Without it the new user joins the default project, so a single-project installation needs no change.
- `PATCH /admin/staff/{id}` with `projects` replaces the memberships; projects left are re-validated (M-4) at once.
- Staff created by OIDC just-in-time provisioning and by the setup wizard join the default project.
- `PUT /admin/projects/{id}/members` replaces a project's member list.

## 5. Scoped configuration

| Object | Project field | Semantics |
|---|---|---|
| Category | `project_id` (required in responses) | Exactly one project. Omitted on create: the default project. Its default team must serve the project. A category with tickets cannot move (`immutable`) |
| Team | `project_ids` | The projects it serves. Omitted on create: the default project; omitted on PATCH: unchanged. Also editable from the project side (`PUT /admin/projects/{id}/teams`) |
| SLA policy | per project | `GET/PUT /admin/projects/{id}/sla-policies`. A ticket uses its project's override for its priority, else the global default (`/admin/sla-policies`). Applied on create, priority change, resume and move |
| Inbound mailbox | `project_id` | New tickets from the mailbox join the project. Its team must serve the project (`team_id` `wrong_project`). Omitted on create: the default project |
| KB category | `project_id` (nullable) | Project or global. Sub-categories of a project category belong to the same project (`wrong_project`). Articles follow their category |
| Automation rule | `project_id` (nullable) | A project rule only runs on that project's tickets (also time rules and dry runs). The `project` condition field (`is`/`is_not`, ids or keys) works in any rule. Set-team and set-category actions must fit the rule's project; canned replies must be global or the rule's project |
| Macro | `project_id` (nullable) | Applies only to that project's tickets (`macro_id` `wrong_project` elsewhere). Personal macros may name a project the owner works in |
| Canned response | `project_id` (nullable) | Offered only in that project; agents see responses of their projects |
| Custom field | `project_ids` (nullable) | Null: every project. Otherwise the field only applies to tickets of those projects |
| Saved view | `filter.project` | Same as the list parameter |
| Webhook | `project_ids` | Empty: every project. Otherwise only events of tickets in those projects are delivered |
| Organization | `project_ids` | Projects its customers may open tickets in (empty: every portal-visible one) |

## 6. Portal entry and branding

- `GET /portal/projects` lists the projects the signed-in customer may open tickets in. The new-ticket form shows a picker when there are several.
- `GET /auth/config?project=KEY` adds `project` to the public configuration: the `PortalProject` view (`portal_title`, `portal_intro_md`, `logo_url`, `primary_color`, name, colour, icon) of an active, portal-visible project, or `null`. The SPA uses it for the anonymous `/p/{key}` entry; null overrides fall back to the global branding.
- `POST /admin/projects/{id}/logo` (multipart `file`, PNG/JPEG/SVG ≤ 1 MB, processed like `logo_light`) and `DELETE` to remove it. The logo is served from `/branding/<sha>.<ext>` and is deleted with the project.

## 7. Agent panel support

- `GET /agent/projects`: the projects the caller works in (all for supervisors and admins), with `my_role` and `can_admin`. Inactive projects are included so chips of old tickets render. The SPA hides the switcher while only one active project exists.
- The switcher's choice persists per user: `PATCH /me {"current_project_id": "<id>" | null}`; `GET /me` returns it. 422 for a project the caller does not work in.
- The `project` filter (id or key; unknown → 422) is accepted by `GET /agent/tickets`, `/agent/tickets/counts`, `/agent/reports/summary`, `/portal/tickets`, `/agent/categories`, `/portal/categories`, `/admin/categories`, `/agent/teams`, `/agent/staff` (members plus supervisors and admins: the mention and assignee pickers), `/agent/custom-fields`, `/portal/custom-fields`, `/agent/canned-responses` (project responses plus unscoped), `/agent/macros`, `/admin/macros`, `/admin/automation/rules`, `/kb/categories`, `/kb/articles`, `/agent/kb/articles`, `/admin/kb/categories` (project plus global for the KB).
- `GET /agent/tickets/counts` returns the usual totals plus `by_project: {<project_id>: {mine, unassigned, triage, breached, tier}}` for every project the caller works in (zeros included). The WebSocket `counts` frame has the same shape.
- Reports: `project_id` (the filter applied) and `by_project: [{project_id, key, name, created, resolved}]`.

## 8. Realtime

- Every ticket event snapshot carries `project_id` and `ref`.
- Topic `project:<project_id>`: every ticket event of the project, for staff who work in it (supervisors and admins: any project). Others get it in `ack.denied`; a customer asking for it is closed with **4403**, like `queue:*`. Frames are still filtered per event (team, tier, I-11).
- Resume (`last_event_id`) replays `project:` topics too.

## 9. Administration API

| Method and path | Who | Notes |
|---|---|---|
| `GET /admin/projects` | project admins (their projects), supervisors, admins | `Project` = project + `key_locked`, `member_count`, `ticket_count`, `team_ids` |
| `POST /admin/projects` | supervisors, admins | `key` (2–10, `^[A-Z][A-Z0-9]*$`, upper-cased, unique → `unique`), `name`, optional `team_ids`, `default_team_id` (added to the teams), portal fields |
| `GET /admin/projects/{id}` | admins of the project | 404 otherwise |
| `PATCH /admin/projects/{id}` | admins of the project | `key`, `active`, `sort`: supervisors and admins (403 for project admins). `key` is `immutable` once the project has tickets. The last active project cannot be deactivated (`not_allowed`). `default_team_id` must serve the project |
| `DELETE /admin/projects/{id}` | supervisors, admins | 409 while tickets, categories, mailboxes or KB categories belong to it, and for the last active project. Rules, macros, canned responses, SLA overrides, memberships, mappings and the logo go with it |
| `GET /admin/projects/{id}/members` | admins of the project | `ProjectMember[]` |
| `PUT /admin/projects/{id}/members` | supervisors, admins | `{members: [{user_id, role}]}`, the full list; staff only |
| `GET`/`PUT /admin/projects/{id}/teams` | admins of the project | `{team_ids}`, the full list; the default team must stay |
| `GET`/`PUT /admin/projects/{id}/sla-policies` | admins of the project | GET: one `ProjectSlaPolicy` per priority with `inherited`. PUT `{policies: [...]}`: the overrides (`[]` clears them) |
| `POST`/`DELETE /admin/projects/{id}/logo` | admins of the project | multipart upload / remove |
| `GET /agent/projects` | staff | see §7 |
| `GET /portal/projects` | customers | see §6 |

New field error codes: `wrong_project`. New event type: `ticket.project_changed`.

## 10. Demo data

`featherdesk seed --demo` creates three projects: **Web App** `WEB` (the renamed Default project, portal-visible with its own title and intro), **Mobile** `MOB` (portal-visible, own colour, SLA override, scoped rule, macro, canned response, KB category and custom field) and **Internal IT** `IT` (staff-only). Tickets are spread over all three; Globex can only open tickets in `WEB`; `freya.fields@demo.test` is project admin of `MOB`; `mona.mobile@demo.test` works in `MOB` only. The summary prints each project with sample refs.

## 11. The user interface

Everything above has a screen. Strings live in the `projects.*` namespace of the agent pack (and the portal pack) in all five locales; every table has the column toggles and the usual compact density, and every form validates before it sends.

### Agent panel

- **Switcher.** The top bar shows a *Project* menu with *All projects* and the user's active projects (colour dot and key). It is hidden while the user works in a single active project. The choice is saved with `PATCH /me {current_project_id}` (restored on every device) and filters the ticket lists, the queue badges (from `by_project` of the counts), reports and the lookups. A choice that is no longer one of the user's projects counts as *All projects*.
- **Refs and chips.** Tickets show their ref (`WEB-123`) and a project chip (colour dot and key) in lists (the ref is a locked column; *Project* is a toggle), the detail header, search results, notifications, the link/merge/split pickers and the customer and organization ticket lists. The old number still shows as `#1042` where no ref is known.
- **Deep links.** `/agent/tickets/WEB-123` opens a ticket through `GET /agent/tickets/by-ref/{ref}`; `/agent/tickets/1042` (number) keeps working, and the global search box accepts `WEB-123` as well as `#1042`. After a move the page follows the ticket to its new ref.
- **Move to project.** The ticket detail has a *Move to project* action, shown when `allowed_actions` contains `transfer`. It lists the user's active projects, warns what changes (new ref, category cleared, team and assignee possibly replaced, SLA recomputed) and shows the server's `wrong_project`, `not_allowed` and `inactive` errors on the field.
- **Create.** The new-ticket form has a project picker when the user works in several projects, preselected with the switcher's project; categories and teams follow the chosen project.

### Administration

- **Administration → Projects** (supervisors and admins; agents who are `project_admin` see only their projects). A table with column toggles (name, key, members, tickets, active, portal, default team), a create/edit dialog and a detail page:
  - *Properties:* key (immutable once `key_locked`), name, description, colour, icon, active, sort, default team, portal visible.
  - *Portal:* title, introduction (Markdown), primary colour, logo upload and removal.
  - *Members:* the full list with `member` / `project_admin` roles (supervisors and admins edit it; project admins read it).
  - *Teams:* which teams serve the project; the default team must stay.
  - *SLA:* one row per priority, *inherited* from the global defaults until overridden; clearing removes the overrides.
- **Project selectors** on categories, inbound mailboxes, KB categories, automation rules (and the `project` condition), shared macros, canned responses (single, optional where the API allows global), and multi-selects on teams, custom fields, webhooks and organizations (*allowed projects*). Project admins only see and can pick their own projects; the other admin pages they may open (categories, KB categories, rules, macros, canned responses) show only their projects' rows.
- **Staff → edit:** a *Projects* editor (project and role per row). Without a choice, new staff join the default project.

### Portal

- **New ticket:** a project picker from `GET /portal/projects`, hidden when only one project is open to the customer.
- **`/p/{key}`:** the per-project entry. It loads `GET /auth/config?project=KEY` and applies the project's title, introduction, logo and primary colour over the global branding, preselects the project in the new-ticket form and keeps it through sign-in. An unknown, inactive or non-portal-visible key shows a friendly "not found" page.
- **Lists and details** show the ref and project chip.

### Reports

The reports page has a *Project* filter (default: the switcher's project) and a *Tickets by project* chart (created and resolved per project, a lightweight SVG like the other charts) from `by_project`.

