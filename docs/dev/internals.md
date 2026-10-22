# Internals: Package Map and Contracts

This is the contract that lets the backend, frontend and tooling be built in parallel. If you change a contract described here, update this file in the same change.

## Ground rules

- **Go 1.27, `CGO_ENABLED=0`.** `go.mod` is pinned through `tools/deps.go`. **Do not run `go get`, and do not add modules.** Runtime dependencies are capped at 15; the current set is listed in `tools/deps.go`.
- **Lightest possible** (see `development/CONVENTIONS.md` §10):
  - No polling loop shorter than 1 s.
  - Buffers are always bounded.
  - Do not write to the database per request when the write can be coalesced.
- **Both engines.** Every store-touching test runs on SQLite **and** Postgres through `storetest.Engines(t, func(t *testing.T, db *store.DB) {...})`. Postgres comes from `FD_TEST_POSTGRES_URL`, for example `postgres://st:st@localhost:55432/fd_test?sslmode=disable`. A missing URL skips Postgres locally and **fails in CI** (`CI=true`).
- Placeholders are always `?`. `store.Rebind` handles Postgres. Dialect-specific SQL goes in a `dialect.go` next to the query.
- Timestamps are `store.Now()` (UTC, µs) and are stored in `TIMESTAMP` (SQLite) or `TIMESTAMPTZ` (Postgres) columns.
- IDs are `store.NewID()` (UUIDv7, text).
- Errors returned to HTTP clients use `problems.*` (RFC 9457). Never call `http.Error` directly.
- Every route must exist in `api/openapi.yaml`. Paths in the spec are relative to `/api/v1`, except root routes that declare `servers: [{url: /}]`. `internal/apispec` provides `AssertRoutes(t, router)`, `ValidateRequest`/`ValidateResponse(t, req, resp)` and `Unimplemented(r, prefix)`. Ticket mutations return `TicketUpdateResult {ticket, visible}`. Customers see `CustomerStatus` (`open`, `awaiting_reply`, `resolved`, `closed`), and their schemas are strict.

## Package map

| Package | Owns |
|---|---|
| `internal/domain` | Enums, `Principal`, scopes |
| `internal/store` | `Open` (engine from URL), `Tx`, `Read()`, `Rebind`, migrator, **all migrations** (`migrations/{sqlite,postgres}`), `storetest` |
| `internal/store/storetest` | `Engines`, `Open(t, dialect)`, fixtures (users, teams, tickets) |
| `internal/blob` | `Store` interface: `Put(ctx, key, r, size, contentType) (sha256 string, err)`, `Open(ctx, key) (io.ReadSeekCloser, Info, error)`, `Delete`, `Stat`. Local filesystem and S3 (hand-written SigV4) |
| `internal/config` | `config.Load()` reads the core `FD_*` variables; `config.Env` helpers (`String`, `Int`, `Bool`, `Duration`, `Required`) that modules use to read their own variables |
| `internal/problems` | Problem types and `problems.Write(w, r, err)`; typed errors `NotFound`, `Forbidden`, `Validation{Fields}`, `Conflict`, `InvalidTransition{Allowed}`, … |
| `internal/server` | chi router, middleware stack, route groups, SPA embed (index.html gets the branded title and link-preview tags from `SetPageMeta`, wired in `internal/app/pagemeta.go`), `/healthz` and `/readyz`, `/branding/*` |
| `internal/app` | `Deps`, the `Module` interface, `PrincipalFrom(ctx)`, and wiring of every module (`app.Build`) |
| `internal/queue` | In-process queue (D-11): dispatcher over `ticket_events`, consumers, `deferred_jobs`, scheduler (`Every`), write-behind (`Touch`) |
| `internal/events` | Event type constants, payload structs, `events.Write(ctx, tx, Event)` (inserts the `ticket_events` row with a snapshot) |
| `internal/realtime` | WebSocket hub (`/api/v1/ws`): topics, per-subscriber filter callbacks, resume, limits |
| `internal/crypto` | `secretbox`: AES-256-GCM using `FD_SECRET_KEY`, for SMTP, IMAP, webhook and TOTP secrets (per-purpose keys) |
| `internal/settings` | Typed settings service over the `settings` table, cached in memory; env-override and lock merge (PLT-027) |
| `internal/authn` | Sessions, local login, OIDC (staff and customer), PATs, ws-tickets, setup wizard, CSRF tokens; two-step verification (`mfa.go`, `mfa_http.go`, `mfa_cli.go`, `totp.go`) and the registration consent hook (`consent_hook.go`) of D-19 §B/§C |
| `internal/authz` | `Can(p, action, view)`, visibility predicates, `CanSeeEvent`, `CanSeeField`, `CanSeeArticle` |
| `internal/directory` | Users, organizations, teams, categories, staff admin; projects (D-20, `projects.go`: snapshot, CRUD, members, team mapping, `/agent/projects`, `/portal/projects`) |
| `internal/tickets` | Ticket service, transitions, escalation, messages, attachments, links, merge/split, custom fields, calendars and SLA, search, reports; D-19 §A assignment strategies (`assign_strategy.go`), automation execution (`automation_*.go`) and macros (`macros.go`) |
| `internal/notify` | SMTP sender, templates (5 locales), recipient resolution, email settings; the in-app notification centre (`inapp.go`, `inapp_http.go`) of D-19 §D |
| `internal/webhooks` | Webhook CRUD, signing, delivery |
| `internal/mailin` | Inbound email: IMAP IDLE, MIME webhook, parser, threading |
| `internal/kb` | Knowledge base |
| `internal/automation` | Rule model of D-19 §A (pure): `Rule`, `Condition`, `Action`, `Validate`, `Evaluate(Facts)`. The ticket service loads facts and executes actions |
| `internal/privacy` | GDPR tooling of D-19 §C: export job (`privacy.export`, ZIP streamed to blob `privacy/`), erasure (`Erase`, two modes), retention (`RunRetention`/`PreviewRetention` from one rule builder, daily `privacy.retention`), consent (`RecordConsent`). Settings keys `retention` and `privacy` |
| `internal/mcp` | Built-in MCP server (`POST/GET/DELETE /mcp` on `Root`, MCP-001…): hand-written JSON-RPC 2.0, dual-era (2026-07-28 stateless; 2025-11-25/06-18/03-26 with in-memory sessions bound to the token). Tools and resources run as in-process REST calls through `Server.Handler()` with the caller's bearer token (`api.go`), so x-scopes, authz and customer views are the REST ones; tools never touch the store. Settings key `integrations` (`{mcp_enabled}`, default off; `/admin/integrations`), helper info `GET /integrations/mcp` |
| `internal/apispec` | OpenAPI test helpers (test-only imports) |
| `internal/demo` | `seed --demo`: builds the full app (`app.Build`, queue never started) and fills it through its HTTP handler in-process, signed in as the demo users, on a simulated clock (`store.SetClock`). Direct store writes only where no service exists (webhook delivery log, inbound log, KB counters, customer passwords, `idp_managed`, consent records via `privacy.RecordConsent`). In-app notifications are replayed per seeded event through `(*notify.Module).DeliverInApp` (the consumer's in-app path, no email); the MFA user is enrolled through `/me/mfa` with codes from `authn.TOTPCode`. `--force` wipes with `store.WipeData`. |
| `cmd/featherdesk` | CLI: `serve`, `migrate`, `healthcheck`, `version`, `backup`, `restore`, `seed` (`--demo`: `internal/demo`), `migrate-engine`, `blob-migrate`, `config`; modules register `create-admin`, `reset-password` (authn), `reindex` (tickets) and `seed-kb` (kb) |

## Wiring contract (`internal/app`)

```go
type Deps struct {
    Cfg      *config.Config
    DB       *store.DB
    Log      *slog.Logger
    Queue    *queue.Queue
    Hub      *realtime.Hub
    Blob     blob.Store
    Settings *settings.Service
    Secrets  *crypto.Box
}

// Module is implemented by every feature package; app.Build mounts them.
type Module interface {
    Name() string
    Mount(g server.Groups) // register routes on the groups below
}
```

`server.Groups` exposes chi routers that already carry the right middleware:

| Group | Path prefix | Guard |
|---|---|---|
| `Public` | `/api/v1` | none (rate-limited) |
| `Authed` | `/api/v1` | any principal |
| `Portal` | `/api/v1/portal` | `Kind == customer`, else **404** (I-1); a customer also gets **404 `portal-disabled`** while `support.portal_enabled` is false (`Server.SetPortalGate`, set by authn) |
| `Agent` | `/api/v1/agent` | `Kind == staff`, else **404** |
| `Admin` | `/api/v1/admin` | admin |
| `Support` | `/api/v1/admin` | supervisor or admin (teams, calendars, global SLA, project create/delete/members) |
| `ProjectAdmin` | `/api/v1/admin` | supervisor, admin, or `project_admin` of at least one project (D-20): `/admin/projects*` reads and project configuration, categories, KB categories, automation rules, shared macros, canned responses. Handlers decide per project with `Principal.AdminsProject` / `authz.CanProjectConfig` (foreign project → 404, global → 403) |
| `Root` | `/` | raw (for `/kb/sitemap.xml`, `/branding/*`, health, `/mcp`). The inbound MIME webhook lives at `/api/v1/inbound/mime/{token}` on `Public`, authenticated by its token |

OIDC callbacks: a refused **browser** navigation in the staff audience (`Accept: text/html`) is redirected to `/agent/login?error=no_role|disabled|oidc_failed|idp_unavailable`; API clients and the customer audience get the problem document (403/503).

Authentication is pluggable. `server` defines `type Authenticator interface { Authenticate(r *http.Request) (*domain.Principal, error) }`, and `authn` provides it. CSRF checking is done in `server` middleware against `Principal.SessionID`, through `authn`'s `CSRFValidator`.

## Events and the queue (D-11)

- Every ticket mutation calls `events.Write(ctx, tx, events.Event{TicketID, ActorID, Type, Data, Snapshot})` **inside its transaction**, and nothing else. `Snapshot` carries `team_id, tier, assignee_id, requester_id, organization_id, status, number, subject` and (D-20) `project_id, ref`; `Snapshot.Project()` maps the empty project of pre-0141 events to `domain.DefaultProjectID`. The `ticket_events` table stores it as JSON, so fan-out needs no extra DB reads.
- Consumers register with `queue.Subscribe(name, filter func(events.Event) bool, handler func(ctx, events.Event) error, opts)`. Durable consumers (`notify`, `webhook`) spill to `deferred_jobs` when they are full or a first attempt fails. Non-durable consumers (`realtime`, `index`) never block the dispatcher.
- Jobs that are not events (auth mails) use `queue.Enqueue(ctx, kind, payload)` after commit, and register a handler with `queue.Handle(kind, fn)`.
- Periodic work uses `queue.Every(name, interval, fn)`, with a minimum interval of 1 s.
- Chatty writes such as `users.last_seen_at` go through `queue.Touch(table, id, column, time)`, which is flushed every 30 s.
- **Exception: session `last_seen_at`.** `sessions` is keyed by `id_hash`, not `id`, and `queue.Touch` flushes with `UPDATE … WHERE id = ?`. authn therefore keeps its own bounded coalescing buffer for sessions (flushed by `queue.Every` every 30 s and on shutdown, TD-AUTH-04). Do not route session touches through `queue.Touch`. KB view and vote counters also use their own coalescer, because `Touch` keeps a max timestamp, not a counter.

## Migration number ranges (D-18)

| Range | Area |
|---|---|
| 0001–0019 | Identity and auth: users, identities, organizations, sessions, PATs, auth tokens, audit |
| 0020–0039 | Tickets core: tickets, messages, attachments, events, counters, FTS, links, custom fields, field values |
| 0040–0059 | Support config: teams, members, categories, settings, calendars, SLA, canned responses, tags, watchers, views |
| 0060–0079 | Platform: `deferred_jobs`, branding assets, idempotency keys, webhooks, deliveries |
| 0080–0089 | Knowledge base |
| 0090–0099 | Inbound email |
| 0100–0109 | Automation and assignment (D-19 §A): `teams.assignment_strategy`/`rr_last_user_id`, `users.away_since`, `automation_rules`, `automation_runs`, `automation_time_runs`, `macros` |
| 0110–0119 | Two-factor authentication (D-19 §B): `user_mfa`, `mfa_recovery_codes`, `mfa_challenges`, `settings.security` |
| 0120–0129 | Privacy / GDPR (D-19 §C): `privacy_requests`, `user_consents`, `tickets.anonymized_at` |
| 0130–0139 | Notification centre (D-19 §D): `notifications` |
| 0140–0149 | Projects (D-20): `projects`, `project_members`, `project_teams`, `organization_projects`, ticket sequence and refs, project scoping of configuration |
| 0150–0159 | Integrations (I-12): `ticket_messages.via_token_id` |

The D-19 files, and one later identity migration:

| File | Contents |
|---|---|
| `0002_users_email_ci` | `users_email_lower_uq`: a unique index on `lower(email)`, the database-level guard behind the application's lower-casing (DB-005, DB-010) |
| `0100_automation` | `teams.assignment_strategy` (`manual`/`round_robin`/`least_busy`, CHECK) and `teams.rr_last_user_id` (round-robin pointer, written back lazily); `users.away_since`; `automation_rules` (ordered by `position`); `automation_runs` (one row per source event acted on: replay guard, pruned after 30 days); `automation_time_runs` (a time rule fires once per ticket until it changes); `macros` (`owner_id` NULL = shared) |
| `0110_mfa` | `user_mfa` (sealed TOTP secret, last accepted time step, pending secret, email flag); `mfa_recovery_codes` (sha256 of user id + code); `mfa_challenges` (purpose `login` or `enroll`, hashed token, login method, attempts, email-code HMAC and send count, pending TOTP secret of a forced enrolment); the `security` settings row `{"mfa_required":"none"}` |
| `0120_privacy` | `privacy_requests` (export: pending → processing → ready → expired/failed, `file_key` under `privacy/`; erasure: pending → completed/rejected; no PII beyond the user id); `user_consents` (kept after an erasure, IP cleared); `tickets.anonymized_at` (retention skips anonymized tickets) |
| `0130_notifications` | `notifications` (`event_id` + user + type unique, so a replayed event inserts nothing; `actor_id` without FK; `ON DELETE CASCADE` from tickets and users) |
| `0140_projects` | `projects` (key unique, portal overrides, `logo_asset_id` → `branding_assets`), `project_members` (role `member`/`project_admin`, staff-only trigger), `project_teams`, `organization_projects`, `users.current_project_id` (the switcher); the **Default project** (fixed id `domain.DefaultProjectID`, key `HD`, named after the branding product name), every staff user a member, every team mapped |
| `0141_project_tickets` | `tickets.project_id` (NOT NULL, default the Default project) and `project_seq` (backfilled `= number`; unique per project); counter `ticket_seq:<project_id>` (the Default project's starts at `ticket_number`); a trigger fills `project_seq` when an insert leaves it 0 (raw fixtures, seeds). SQLite cannot `ADD COLUMN` a NOT NULL REFERENCES column with a default while foreign keys are on, so the project FKs of `tickets` (and `categories`, `inbound_mailboxes` in 0142) are triggers there, real FKs on Postgres; deleting a project with tickets fails on both |
| `0142_project_scoping` | `categories.project_id` and `inbound_mailboxes.project_id` (NOT NULL), `kb_categories.project_id`, `automation_rules.project_id`, `macros.project_id`, `canned_responses.project_id` (nullable), `custom_field_defs.project_ids`, `webhooks.project_ids`; `sla_policies` gets a surrogate `id` and `project_id` (global rows `project_id IS NULL`, two partial unique indexes); SQLite rebuilds the table with the same column order as Postgres' `ALTER`s |
| `0150_integrations` | `ticket_messages.via_token_id` (FK `api_tokens`, `ON DELETE SET NULL`, partial index): the `tickets:act_as_customer` token that posted a customer message on the customer's behalf (I-12). Staff `Message` and webhooks expose it as `via_integration`; customer queries never read it |
| `0131_ticket_search_narrow` | Postgres only (DB-023; the SQLite file is comments): rebuilds `ticket_search` as `public_tsv` plus one stored `staff_tsv` (public ∪ internal ∪ staff fields, written by `store.IndexTicket`), each with a plain GIN index, and `toast_tuple_target = 128` so the tsvectors live out of line and the heap stays small (~11 MB at 100k tickets). It sits in the notifications range only because it had to run after 0130 |

Ticket refs (D-20): `store.TicketMatch` turns `KEY-123` tokens of the query into a `UNION` inside the subquery (`project_id = (SELECT id FROM projects WHERE key = ?) AND project_seq = ?`), so the text match keeps its plan; a query made only of refs skips the text search. The staff visibility predicate writes the I-11 term as `(t.project_id || '') IN (…)`: as a plain column the SQLite planner gave up the `tickets_updated_idx` walk of the default list sort for a full scan and sort (0.7 ms → 47 ms at 50k tickets, `TestPerf_List50k`). `ticketCols` never reads the project key (a sorted list would evaluate it for every candidate row): `Service.fillKey` sets `ticketRow.ProjectKey` from the directory snapshot before `Ref()` is used.

Postgres ticket search (`store.TicketMatch`) compares against `(SELECT to_tsquery('simple', ?))`: the scalar subquery hides the term frequency from the planner, so every execution (custom or pgx-cached generic plan) takes the GIN bitmap plan instead of a per-row `@@` walk of `tickets_updated_idx`. Keep that shape when you touch the search SQL; `TestPerf100k` (`FD_PERF=1`) is the check.

Ranges are filled out of order: a migration added later in a lower range (such as `0002_users_email_ci`) is pending on a database that already applied 0130. `db.PendingMigrations` (`internal/store/boot.go`) therefore compares the applied **set** with the embedded files, never `MAX(version)`.

Each migration exists for **both** engines with the same number and name. `TestSchemaParity` compares the resulting tables, columns, CHECK enums and indexes.

## Testing conventions

- `go test ./...` must pass with no network access, apart from the local Postgres.
- Every handler test goes through `httptest` and the real router. **Every** response is validated against the OpenAPI spec with `apispec.ValidateResponse`, including 3xx, 4xx and 5xx answers and the route-group guards. There are no per-call or per-status exclusions: when a handler legitimately answers a code, document it on the operation. The guards are documented globally: every authenticated operation lists 401 and 403 (role guard, PAT scope, CSRF), every `/portal`, `/agent` and `/admin` operation lists 404, and every mutation lists 403 (CSRF). The only unvalidated calls are deliberate fuzz or timing loops (`TestSetup_DriveByTakeover`, `TestLogin_UnknownUserTimingSimilar`) and test-only probe routes.
- Invariant tests keep the names from the plans, for example `TestVisibility_L1CannotSeeL2Ticket` and `TestWS_CustomerNeverReceivesInternal`.
- Frontend: Vitest for units, Playwright for E2E (`web/e2e`).

## Available APIs (wave 1 delivered — use these, do not re-implement)

### store (`internal/store`, `internal/store/storetest`)
- `storetest.Engines(t, func(t, db *store.DB))`; fixtures `CreateOrg`, `CreateTeam` (serves the Default project), `CreateStaff(role, tier, teams...)` (member of the Default project), `CreateProject(key)`, `AddProjectMember`, `RemoveProjectMember`, `MapProjectTeam`, `CreateCustomer(org)`, `CreateCustomerRole`, `CreateCategory`, `CreateTicket(storetest.Ticket{…})`. Postgres via `FD_TEST_POSTGRES_URL` (isolated schema per test).
- Scanning on both engines: `store.JSON[T]` / `RawJSON` for JSON columns, `store.NullTime`, `store.NullString`, `store.Nullable(s)` (empty → NULL). Bind real `bool`; write `WHERE active` or `TRUE`/`FALSE`, never `= 1`.
- `store.NextTicketNumber(ctx, tx)`, `store.NextProjectSeq(ctx, tx, projectID)` / `store.ProjectSeqCounter(id)` (D-20), `store.Cursor`/`SetCursor`, `db.OnCommit`, `tx.AfterCommit`, `db.LockInstance` (taken by `serve` via `app.BuildOptions.LockInstance`; offline commands use `store.LockSQLiteFile(path)` so they never create the file), `db.WriteStats()` (writer transactions/statements through `Tx`, for write-count tests), `db.PendingMigrations` (compare the applied set, never `MAX(version)`: ranges are filled out of order). Postgres sessions run with `jit=off` unless the URL sets it.
- Search: `store.IndexTicket` / `UnindexTicket`, `store.TicketMatch(dialect, q, staff)` (returns a subquery for `t.id IN (…)`; customers get public text only); `IndexKB` / `UnindexKB` / `KBMatch`.
- Hard delete of a ticket: insert a `purge_allow('ticket_events', <ticket_id>)` row first, because append-only triggers block deletes.
- Prune functions: `PruneSessions`, `PruneAuthTokens`, … `PruneOrphanAttachments` and `PruneInboundMessages` return blob keys, which the caller deletes after commit.
- `Backup`, `Restore`, `IntegrityCheck`, `BootCheck` (run by `app.Build` before migrating; result in `/admin/system` `db.integrity`), `PreMigrateBackup` (`serve`, SQLite, newest 3 in `$FD_DATA_DIR/backups`), `SeedDemo`, `MigrateEngine`, `LintSchema`. `settings.RotateSecrets` re-seals every stored secret (`featherdesk rotate-secrets`).
- `store.WipeData(ctx, db)` (`internal/store/wipe.go`, used by `seed --demo --force`): one write transaction that deletes every row of every data table and the search indexes, resets the ticket-number and dispatcher counters, re-creates the Default project and its sequence (D-20), and keeps the schema, the migration history and the SLA policy rows. It lifts the append-only triggers through `purge_allow` for the call and returns the blob keys the deleted rows referenced (attachments, branding and KB assets, raw inbound mail, privacy export ZIPs); the caller deletes them after commit.
- `blob.NewFromURL`, `blob.NewKey(prefix, now)`, `blob.Migrate`.
- DB triggers enforce: requester is a customer, assignee is staff, `users.kind` immutable, `team_members` staff only, KB visibility (M-10), append-only `ticket_events` and `audit_log`.

### server / app
- `server.Authenticator{ Authenticate(r) (*domain.Principal, error) }`: return `(nil, nil)` for anonymous and `problems.Unauthenticated()` for bad credentials. `server.CSRFValidator{ ValidCSRF(p, token) bool }`. Cookie auth means `p.SessionID != "" && p.TokenID == ""`; those requests get CSRF + Origin checks, bearer requests are exempt.
- Accepted origins: `config.Config.Origins()` returns the origin of `FD_PUBLIC_URL` followed by each `FD_ALLOWED_ORIGINS` entry reduced to `scheme://host[:port]` (`Config.finish` refuses an entry without an http/https scheme and host, or with a path). The one list feeds `Server.sameOrigin` (CSRF; any `Sec-Fetch-Site` other than `same-origin`/`none` is refused first, so it never acts as CORS), `realtime.Options.AllowedOrigins` (cookie WebSocket upgrades) and `mcp.Options.Origins`. Links, OIDC redirect URIs and the `Secure` cookie decision use `FD_PUBLIC_URL` only.
- `server.Groups{Public, Authed, Portal, Agent, Admin, Support, Root chi.Router}`; `(*Server).Limit(name, perSec, burst)`, `Idempotent()`, `server.MaxBody(n)`, `Metrics().Gauge` / `Metrics().Counter` (names ending in `_total` must be counters: `promtool check metrics`), `server.PrincipalFrom(ctx)`, `ClientIP`, `IsHTTPS`, `Logger(ctx)`.
- Modules: `type Factory func(d *app.Deps) (app.Module, error)`; append to `app.Registry` in `internal/app/modules.go` (authn first). `Deps{Cfg, DB, Log, Queue, Hub, Blob, Settings, Secrets, Server, Getenv, Version, Commit}`.
- Optional module interfaces:
  - `AuthProvider{Authenticator(); CSRFValidator()}`; if the authenticator implements `realtime.Rechecker` it is wired to the hub.
  - `RealtimeProvider{RegisterRealtime(*realtime.Hub)}`
  - `QueueProvider{RegisterQueue(*queue.Queue)}`, called before Start
  - `Starter{Start(ctx) error}`
  - `SystemReporter{ReportSystem(ctx, map[string]any)}`
- Already served by the platform, do not mount again: `/api/v1/ws`, `/admin/queue`, `/admin/jobs`, `/admin/jobs/{id}/retry`, `/admin/system`, `/admin/audit-log`, `/admin/storage`, `/healthz`, `/readyz`, `/metrics`, `/branding/*`, `/api/v1/openapi.yaml`, `/api/docs`. The last four non-health routes live in `internal/app/core.go`; `/api/docs` is a static reference rendered once from the embedded spec (no script, no CDN).
- **Route ⇔ spec equality.** `TestAllSpecOperationsImplemented` (`internal/app`) builds the full app like `serve` and fails on any spec operation that is not served **and** on any route that is not in the spec. Add the spec operation and the handler in the same change.

### queue / events / realtime / settings / crypto / cli / config
- `q.Subscribe(name, queue.SubscribeOptions{Durable, Workers, Capacity, Timeout, MaxAttempts, OnOverflow}, filter func(events.Event) bool, h func(ctx, events.Event) error)`; `q.Handle(kind, h func(ctx, json.RawMessage) error, opts...)`; `q.Enqueue(ctx, kind, payload)` (after commit); `q.Every(name, interval, fn)`; `q.Touch(table, column, id, t)`; `q.Retry`, `q.Status`.
- `events.Write(ctx, tx, *events.Event, data any)` (inside the ticket tx); `events.ReadAfter`, `events.ForTicket`; `events.Snapshot{Number, Subject, Status, Priority, Tier, TeamID, CategoryID, AssigneeID, RequesterID, OrganizationID, Watchers, Channel}`; `events.CustomerVisible(type)`; type constants and payload structs.
- realtime:
  - `hub.Authorize(prefix, TopicAuthorizer{AuthorizeTopic(ctx, p, topic) bool})`; a prefix ending in `:` matches as a prefix, and unknown topics are denied.
  - `hub.OnEvent`, `PublishFunc(topic, func(p) (Frame, bool))`, `Publish(topic, frame, allow)`, `SendUser`, `Broadcast`.
  - `Revoke(topic, lost)` sends `ticket.access_lost`; `CloseUser`/`CloseSession(…, code, reason)`; `SetReplayer(Replayer{Replay(ctx, p, topics, lastEventID) ([]Frame, bool)})`; `realtime.EventFrame(topic, e, data)`; close codes `CloseUnauthenticated` (4401) and others.
- settings:
  - `Get/Put(ctx, key, v, actorID)`, `OnChange(func(key))`, `Support`, `Branding`.
  - `SMTP(ctx) (SMTPView{SMTP, PasswordSet, LockedByEnv})`, `SMTPPassword`, `PutSMTP(ctx, in, newPassword *string, actor)`, `Email`, `PutEmail`, `Setup`.
  - `settings.Overlay(dst, prefix, getenv) []string` (env lock); purposes `PurposeSMTPPassword`, `PurposeIMAPPassword`, `PurposeIMAPRefresh`, `PurposeWebhook`, `PurposeMFA` (`mfa-totp`).
  - D-19 settings keys: `security` (owned by authn, `{mfa_required}`), `retention` and `privacy` (owned by `internal/privacy`); `integrations` (owned by `internal/mcp`, `{mcp_enabled}`). No D-19 feature reads an `FD_*` variable; team strategies, availability and notification preferences are columns, not settings keys.
- crypto: `Box.SealString(purpose, s)`, `OpenString`, `MAC`, `VerifyMAC`.
- cli: `cli.Register(cli.Command{Name, Summary, Run func(ctx, args) error})`. authn registers `create-admin`, `reset-password` and `reset-mfa` in its `init`.
- Customer portal switch (`support.portal_enabled`, default true; `internal/authn/portal.go`): authn caches it in an atomic bool, refreshed by `settings.OnChange("support")`. While false: customer login, register, verify-email, magic link, customer OIDC and (customer) invite accept answer 404 `portal-disabled` (the invite token is not consumed); `Authenticate` rejects customer PATs (404 `portal-disabled` under `/portal/`, the documented 401 elsewhere) and treats a customer cookie as anonymous (both except on `/api/v1/ws`); `realtime.Admitter` (implemented by authn) makes the hub accept and close a customer socket with 4403; the flip to false deletes every customer session and `mfa_challenges` row, audits `auth.portal_disabled` and closes customer sockets (`Hub.CloseWhere`). Inbound mail and outgoing notifications never use a customer principal, so they are unaffected. No `FD_*` variable: the support document has no env overlay.
- MFA (D-19 §B, `internal/authn/mfa*.go`, details in [Two-step verification wiring](#two-step-verification-wiring-d-19-b)): every non-SSO login that creates a session goes through `mfaGate` (password, magic link, verify-email, invite accept), which answers `MfaChallenge` instead of `Me` for users with a second factor or covered by `settings.security.mfa_required`. `startSession` itself has no MFA logic (OIDC and the setup wizard, which only creates a new account, use it directly). SSO-only accounts (AUTH-044, `ssoonly.go`: an OIDC identity for the audience or `idp_managed`, break-glass staff admins excepted) are refused before the gate on every local path; `markSSOLinked` clears the password, local sessions, open challenges and unused reset/magic links when an account becomes SSO-only. TOTP secrets are sealed with `settings.PurposeMFA`; `authn.ResetMFA(ctx, tx, userID, actorID, ip, via)` removes every factor (admin reset, CLI, GDPR erasure).
- config: `config.NewEnv(getenv)` with `String`, `Int`, `Bool`, `Duration`, `List`, `Map`, `Required`, `Fail`, `Err`. The listen variable is `FD_LISTEN_ADDR`.
- Problem slugs include `unavailable` (503), `idempotency-in-flight` (409) and `portal-disabled` (404), and the I-12 slugs `on-behalf-not-customer` (403), `on-behalf-not-visible` (403), `on-behalf-disabled` (409).
- Integrations (I-12, `internal/tickets/onbehalf.go`): `tickets:act_as_customer` is held only when a token lists it explicitly (`Principal.ActsAsCustomer`; `HasScope` never derives it from `admin` or a nil session scope list) and is grantable to admins only. `POST /agent/tickets/{id}/customer-messages` resolves the target customer into a synthetic customer `Principal` and calls `AddCustomerReply`, the portal reply path, with a server-side `onBehalf{OwnerID, TokenID, IP}` on `MessageInput`/`CreateInput`: it sets `via_token_id`, makes the owner the draft uploader and writes the `ticket.message.on_behalf` audit row in the same transaction (follow-ups included). `agentCreate` under the scope authors the first message as the requester. A token holding only this scope is refused by `enforceTokenScope` on every operation that does not list it (except `GET /me`). Per-token limit through `Server.AllowKey("on_behalf", "t:<token id>", 1/s, 20)` in a route middleware mounted before `Idempotent()`, so a 429 is never stored under the key. `ProblemSlug` in the spec equals `problems.Registry` (`TestProblemSlugsMatchRegistry`).
- Field error codes are the `FieldErrorCode` enum. A new `problems.Field(…, "<code>", …)` needs the code in the enum; `TestFieldErrorCodesDocumented` scans the sources (codes passed as variables are listed in `internal/apispec/codes_test.go`).

### frontend (`web/`)
- Forms:
  - `useForm()` returns `{formRef, submit(fn), fieldErrors, formError, submitting}`.
  - Rules come from `validations.*` (`@/constants/validations`), including `forField(def)` and `fromServer(code)`.
  - **Register every form in `src/forms/registry.ts`.** Lint rejects inline rules and any submit that does not call `validate()`.
- API:
  - `api` (typed openapi-fetch), `unwrap(api.GET(...))` returns data or throws `ApiProblem`.
  - `apiFetch`, `applyServerErrors`, `newIdempotencyKey()`.
  - CSRF and 401 redirects are automatic.
- Stores:
  - `useConfigStore` (`config`, `branding`, `tierLabel(t)`; `/auth/config` sends the API's nested `BrandingSettings`, which `brandingFromApi` flattens once into the SPA's `Branding` shape), `useSessionStore` (`me`, `isStaff`, `isCustomer`, `isAdmin`, `isSupervisor`, `can(action, ticket)`; reads `allowed_actions` only).
  - `useUiStore().notify`, `useCountsStore` (agent).
  - `useRealtime(topics, {onEvent, onResync, onCounts, essential})`.
- Components:
  - `StatusChip`, `PriorityChip`, `TierChip`, `UserAvatar`, `EmptyState`, `PageHeader`, `TicketNumber`, `RelativeTime`.
  - `ConfirmDialog` + `useConfirm()`.
  - `MarkdownEditor` / `MarkdownView`.
  - `MessageThread(messages, area)`: the portal drops non-public messages.
  - `AttachmentUploader` / `AttachmentList`, `BrandLogo`, `ThemeToggle`, `LocaleSwitcher`, `UserMenu`, `PortalShell`.
  - **Every select dropdown is `AutocompleteSearch`** (`@/components/AutocompleteSearch.vue`); a raw `<v-select>` or `<v-autocomplete>` is forbidden outside that file (`src/components/selectGuard.spec.ts` fails the build). It wraps `v-select` with a search box in the menu, a client-side filter over `title` and `searchFields`, and passes `rules`, `v-model`, `item-title`/`item-value` and the `item`/`selection`/`chip` slots through. Props: `compact-multiple` (filter bars: up to 2 chips, then one "N selected", selected options first on open), `hide-search` (2-4 fixed options), `fetch-items` (async mode for server-side lookups: debounced 250 ms, loading and "No results" states, results replace the items, no client filter) with `initial-items` so a selected title renders when it is not in the results. `reset()` is exposed. `v-combobox` (free text) is the only other select-like field allowed. Placeholder and counters are `common.search`, `common.nSelected`, `common.noResults`, `common.searching`.
  - **Every password input is `PasswordField`** (`@/components/PasswordField.vue`): a `v-text-field` with an eye button (`data-testid="password-toggle"`, `aria-pressed`, labels `common.showPassword` / `common.hidePassword`) that switches `type` between `password` and `text` and resets to hidden on blur. Props, `:rules`, `v-model` and slots pass through. A raw `type="password"` anywhere else fails `PasswordField.spec.ts`.
  - **Column toggles:** `useTableColumns(tableId, columns)` (`@/composables/useTableColumns`) plus `ColumnsMenu` on every table. A column is `locked` (always shown: the ref/ID and main column), `lowPriority` (hidden by default below 960 px) or `defaultHidden` (e.g. custom-field columns). Only the user's overrides are stored, in `localStorage` under `fd.v1.cols.<user id>.<table id>`, every access guarded; there is no preferences endpoint. The ticket list migrates the old `fd.v1.agent.columns` key once.
  - **Two-column pages:** `TwoColumnLayout` (`@/components/TwoColumnLayout.vue`, slots `main` and `aside`; props `aside-start`, `main-fr`/`aside-fr`, `breakpoint` `md`|`lg`) is the one layout for a long main column plus a short aside (properties, preview, info). From the breakpoint the aside is `position: sticky` at `top: var(--fd-sticky-top)` with `max-height: calc(100vh - var(--fd-sticky-top) - 16px)` and `overflow-y: auto`, so a tall aside scrolls inside itself and focus inside it scrolls into view; below it both stack, nothing sticks. `AppBar.vue` (both areas) publishes `--fd-sticky-top` on `<html>` (bar bottom edge + 16 px, via `ResizeObserver`). Sticky needs no ancestor with `overflow` other than `visible`: keep `v-container`/`v-main` clean. Used by agent ticket detail (aside = properties and links), customer and organization detail (aside = info/members, first), branding (aside = previews, `lg`). Do not add `position: sticky` by hand. E2E: `e2e/agent/sticky.spec.ts` (screenshots in `test-results/sticky/`).
  - **Density:** one field density for the whole app, set as Vuetify defaults in `radiusDefaults` (`src/plugins/branding.ts`, FE-032): text fields, textareas, selects, autocompletes, comboboxes, file, date and number inputs are `outlined` + `compact` (40 px), chips inside them `small`; checkboxes, switches and radio groups `compact`. There is no user density setting.
  - **Palette:** `src/theme/palette.ts` gives each app section a hue (`SECTION_HUE`, light and dark variants in `HUES`), registered as Vuetify colours `nav-<section>` / `on-nav-<section>` by `buildThemes`; status, priority and the semantic success/warning/error/info colours come from it too, and `avatarColor(name)` picks a stable section colour per person. `routeSections.ts` maps pages to their section and icon (`PageHeader` badge, admin nav). `palette.spec.ts` asserts WCAG AA (4.5:1) for every hue on its 16 % tint in both themes; branding only sets `primary`/`secondary`/`accent`.
- i18n: every key in en/it/de/fr/es (`web/scripts/check-i18n.mjs` enforces this).
- E2E: `e2e/fixtures.ts` provides `loginAs`, `tokenClient` (sends CSRF + `Origin`), `watchConsole`, `a11y` (axe; frames where timers never fire, such as the `sandbox=""`/CSP-sandboxed email previews, are excluded from the main run, which would otherwise hang in `axe.runPartial`, and scanned instead as the iframe element plus a copy of the frame's document in a fresh page; their violation ids get ` (in frame <url>)`), and gives every test its own `X-Forwarded-For` client IP (the E2E server trusts loopback via `FD_TRUSTED_PROXIES`, so the per-IP login throttle applies per test). The default admin is `admin@e2e.test` / `e2e-admin-password-123` via `FD_BOOTSTRAP_ADMIN_*`. `playwright.config.ts` env: `E2E_SERVER_BIN` (built binary instead of `go run`), `E2E_DATABASE_URL` (e.g. an empty Postgres DB), `E2E_MAILPIT_URL` (SMTP → `E2E_SMTP_HOST:PORT`, default 127.0.0.1:1025), `E2E_BASE_URL` (no server started), `E2E_SETUP_BASE_URL`/`E2E_SETUP_TOKEN` (fresh instance for the wizard), `E2E_AUTHENTIK_URL` (`e2e/sso.spec.ts` against the dev stack; Authentik API and sign-in helpers in `e2e/sso-helpers.ts`). `e2e/smoke-stack.spec.ts` (CI job `smoke`, DEP-022) needs `E2E_SETUP_TOKEN` + `E2E_AUTHENTIK_URL` + `E2E_MAILPIT_URL` against a dev stack on an empty database started without a bootstrap admin (`FD_DEV_BOOTSTRAP_ADMIN_EMAIL= FD_DEV_BOOTSTRAP_ADMIN_PASSWORD=`); it is one-shot (chromium only, no retries).
- Agent panel additions (FE-019…FE-059):
  - Area message packs: `loadMessagePack(name, loader)` in `@/i18n`. The agent strings live in `src/locales/agent/<locale>.json` and load lazily in the agent routes' `beforeEnter`. `setLocale` reloads every registered pack (the last call wins: a switch still loading when a later one starts never applies), and `check-i18n.mjs` checks each `src/locales/<dir>/` as its own key set.
  - Routes: `src/router/agent-routes.ts` holds `agentRoutes`, where every page is a lazy chunk, and `adminNav`. The drawer shows an admin link only when `decide()` (the route guard) allows it, never by comparing roles.
  - Forms: agent forms are registered per group in `src/areas/agent/<group>/forms.registry.ts`, which `src/forms/registry.agent.ts` aggregates.
    - `FormEntry.emptyMessage` (a string, or `false`) is for forms whose only rules are format or select rules.
    - A form component never loads data on mount; its page passes the data in as props.
  - Validations added: `integer`, `httpsUrl`, `kbSlug`, `customFieldKey` (the API's `^[a-z][a-z0-9_]{0,62}$`), `timeOfDay`, `timeRangeOrder(() => ranges, i)`, `ianaTimeZone`, `uniqueValues(() => values)`, `dateRangeOrder(() => start)`, `noInternalLinks(() => hrefs, () => active)`.
  - `errorCodes` is exactly the `FieldErrorCode` enum of `api/openapi.yaml` (`codes.spec.ts`), each code with a message in every locale; client-only rule codes live in `ruleCodes`. A new server field code needs both the enum entry and a message.
  - Shared components:
    - `CustomFieldInput` / `CustomFieldValue`: any definition type; rules come from `forField`; `noRules` is for filters. Forms show, require and send a field only while `fieldApplies(def, categoryId, projectId)` (`src/utils/customFields.ts`, the client twin of `FieldDef.AppliesTo`: null or empty scope = all, a category-scoped field never applies without a category) holds, drop the values of fields that stop applying (`dropFieldValues`), and read server errors from `fieldErrors['cf.<key>']`. The portal lookup keeps `category_ids` for this (it still nulls `project_ids`: it is asked per project).
    - Server side (`applyFieldValues`): a field that does not apply to the ticket is never required, and an empty value for it (null, blank, `[]`, `false`) or the value it already holds is ignored (nothing written); any other value is 422 `not_applicable`. Required customer fields (M-9) bind only portal/API creation, not inbound e-mail or follow-ups.
    - `MarkdownEditor`: props `extraRules`, `maxLength`, `testid`; exposes `insert(text, replaceBefore)`, `cursor()` and `focus()`.
    - `MessageThread`: props `selectable` and `v-model:selected` (agent split), plus a `#actions="{ message }"` slot.
  - Realtime: `RealtimeClient.sendPresence(topic, state)` and `onPresence(fn)`. These are staff-only and never used by the portal.
  - Ticket mutations (`areas/agent/tickets/detail/ticketApi.ts`):
    - `If-Match` is sent only when an `ETag` came from `GET /agent/tickets/{id}`. `by-number` sends none, and the client never builds one.
    - `Idempotency-Key` is one per composer draft or create form.
    - `visible: false` leads to a toast and navigation to the list.
  - Tests: `src/areas/agent/testing.ts` provides `mountAgent(component, {path, route, me, api: {'METHOD /path': handler}})`. It uses the real Vuetify, i18n and Pinia, a mock socket (`h.socket().push(frame)`), and `h.calls('PATCH /x')` with the parsed bodies. `e2e/agent/helpers.ts` covers seeding staff and teams. Cookie mutations from `page.request` need `X-CSRF-Token` and an `Origin` header.
- D-19 screens (FE-060…FE-089):
  - Automation (`src/areas/agent/automation/`): `RulesPage` + `RuleForm` + `DryRunForm` (`/agent/admin/automation`), `MacrosPage` + `MacroForm` (shared at `/agent/admin/macros`, personal at `/agent/macros`), `MacroPicker` in the ticket header, `AvailabilityToggle` inside `UserMenu` (staff). Strings under `automation.*` in the agent pack.
  - MFA (`src/components/mfa/`): `MfaStep` (the second sign-in step and forced setup, used by both login pages and the portal's verify-email, magic-link and invitation pages), `MfaSecuritySection` (profile card in both areas; the portal page is `SecurityPage` at `/profile/security`), `MfaReauthForm`, `MfaCodeForm`, `RecoveryCodes`, `QrCode`. Admin: `SecurityPolicyCard`/`SecurityPolicyForm` on the support settings page, `UserMfaCard` in the staff dialog and on the customer page. The QR encoder is `src/utils/qr.ts`, written in-repo with no dependency and lazy-loaded with `QrCode.vue`. Strings: `mfa.*` in the lazy `account` pack (`src/locales/account/`, `loadAccountMessages` in `@/i18n/account`), loaded with the portal and agent packs; the pack also holds `mcp.*` (`McpConnect` on both tokens pages).
  - Privacy (`src/areas/agent/privacy/`, `src/components/privacy/`): `PrivacyPage` (`/agent/admin/privacy`, tabs *Requests* and *Retention and settings*), `PrivacyActions` (per-user card), `MyDataPage` (`/agent/profile/data`) and the portal `MyDataPage` (`/profile/data`), both on `MyDataPanel`. Strings: the lazy `privacy.*` pack (`src/locales/privacy/`, `loadPrivacyMessages` in `@/i18n/privacy`).
- Projects (D-20): `useProjectsStore` (`@/stores/projects`: `GET /agent/projects`, `showSwitcher`, `currentId`/`filter` for list queries, `select(id)` persists with `PATCH /me current_project_id`); `ProjectChip` (colour dot and key) and `TicketNumber :reftext` show refs; `ProjectSwitcher` sits in the agent top bar (a 40 px target like the other actions; a lazy chunk, loaded only when `showSwitcher`) and `useCountsStore.counts` follows it through `by_project`. Route meta `orArea: 'project_admin'` lets agents with that `allowed_areas` entry into the project-scoped admin pages (the API still decides). Deep links: `/agent/tickets/:ref` (by-ref) next to `/agent/tickets/:number`. The portal entry is `/p/:key` (`/auth/config?project=KEY`, unknown key: friendly 404). Strings: `projects.*` in the agent and portal packs. The palette has a `projects` hue.
- Chunking (FE-031). The initial-JS budget (200 KB gzip target per area) is kept by these measures, in `web/vite.config.ts`, `src/utils/` and the shells:
  - `build.rolldownOptions.output.codeSplitting.groups` puts every module that all four first views share (Vue, vue-router, vue-i18n/`@intlify`, Pinia, openapi-fetch, and the Vuetify composables and components of the login and layout shells, listed in `CORE_COMPOSABLES` / `CORE_COMPONENTS`) into one `core` chunk. Left to the default splitting they scatter over ~20 small shared chunks. Anything used only by a lazy page must stay out of that list.
  - The `mdiInline` plugin (`apply: 'build'`) rewrites each `import { mdiX } from '@mdi/js'` into inline string constants, so an icon ships only in the chunks that use it. An unknown icon name fails the build.
  - `src/utils/markdownLoader.ts` (`loadMarkdown()`, `markdownRenderer()`) is the one shared dynamic import of markdown-it + DOMPurify (`./markdown`); `MarkdownView` renders through it, so the renderer loads once, on the first render, and stays out of every page chunk. Use it instead of importing `@/utils/markdown` statically.
  - Feature flags (`define` and `vue({ features })`): the build drops the vue-i18n legacy API, its global `<i18n-t>`/`v-t` install and devtools (`__VUE_I18N_LEGACY_API__`, `__VUE_I18N_FULL_INSTALL__`, `__INTLIFY_PROD_DEVTOOLS__` all `false`) and Vue's Options API (`optionsAPI: false`). Write components with `<script setup>` / `setup()`; the message compiler stays, since the locale files are plain strings.
  - Shell pieces that the first paint does not show are lazy chunks: `App.vue` mounts `ConfirmDialog` and `AppSnackbar` on the first confirm or toast (and warms both once the browser is idle), `AgentLayout` loads `ProjectSwitcher` only when `showSwitcher` and `ShortcutsHelp` (`areas/agent/shared/`) on its first open. So `VDialog` and `VSnackbar` are not in `CORE_COMPONENTS`.
  - Strings that only lazy screens use live in lazy message packs (`agent`, `portal`, `privacy`, `notifications`, `account`), not in the main `src/locales/<locale>.json`.
  - Measure with `pnpm build && pnpm budget`; the per-area numbers are in `development/frontend/frontend_log.md`.
- Notification centre (D-19 §D, FE-090…FE-096), shared by both areas in `src/notifications/`:
  - `NotificationBell` is a lazy chunk in the app bar of `AgentLayout` and `PortalShell` (customers only), so neither area's initial JS grows. The dropdown (`NotificationMenu`) and the pages (`/agent/notifications`, `/notifications`, one `NotificationsPage`) are lazy too.
  - `useNotificationsStore` binds to the tab's `RealtimeClient` per signed-in user: it subscribes `me` as an **essential** topic (a hidden tab must still raise pop-ups), applies `notification.created` / `notification.read`, and refetches `GET /me/notifications/unread-count` after a reconnect, a resync and on `visibilitychange` (these frames are never replayed). `onCreated` / `onRead` hooks feed open pages.
  - Browser pop-ups (`browser.ts`, Notification API only): shown when `document.visibilityState === 'hidden'` and the frame says `browser: true`; `tag` = notification id; a click focuses the tab, opens the ticket and marks it read. Sound is a WebAudio tone toggled per browser (`localStorage st.notifications.sound`).
  - `NotificationPrefsForm` (registered in `src/notifications/forms.registry.ts`) sends the whole matrix as `notification_prefs`; `validations.browserNotifications(() => permission)` blocks a browser channel without the permission. The portal profile no longer sends `notification_opt_out` (email lives in the matrix).
  - Strings: the lazy `notifications.*` pack (`src/locales/notifications/`, `loadNotificationMessages` in `@/i18n/notifications`), loaded with the agent and portal packs. Tests: `src/notifications/testing.ts` (`MockNotification`, `setVisibility`); E2E `e2e/notifications.spec.ts`.

## Cross-module wiring (`internal/app/modules.go`)

- `ticketsModule` wires the directory built by tickets: `SetDisabler(authn.DisableUser)` (AUTH-017: status, sessions, PATs, socket close 4401, audit) and `OnTeamsChanged(authn.InvalidateTeams)` (AUTH-002 team cache). `directory.NewDirectory` no longer registers these itself.
- `PATCH /admin/staff/{id}` calls `directory.MembershipChanged(ctx, tx, actor, teamID, []string{id})` inside its transaction (TKT-017, M-4). A role, tier or status change passes `teamID ""` (every team); leaving a team passes that team. The tickets module registers the process-wide hook that unassigns tickets with a `ticket.assigned` event.
- authn and the staff admin compute "email enabled" with `notify.EnabledWith` (one rule for `/auth/config`, register, forgot and invites). `PATCH /me` runs `notify.ValidateOptOut` on `notification_opt_out`.
- `auth.reset_password` with `vars.sso="true"` (SSO users, AUTH-015) renders the "sign in with SSO" text in every locale and carries no link (`TestResetPassword_SSOVariant`).
- The core module also serves the `admin:system` WebSocket topic (admins; PATs need `admin`): `queue.changed` is published when the in-memory queue counters moved, checked every 5 s with no DB read; the admin queue page refetches `GET /admin/queue`.
- Canned-response placeholders: `{{customer.name}}` is canonical and `{{requester.name}}` an alias, on the server preview (`GET /agent/canned-responses/{id}/preview`) and in the composer.
- `settings.email.inbound_blocklist` (address, `@domain` or `domain`) is edited through `GET/PUT /admin/email` (`inbound_blocklist`) and enforced by the mailin pipeline.
- End-to-end coverage of the wiring lives in `internal/app/e2e_*_test.go` (package `app_test`): the full app from the default `Registry`, a fake SMTP sink (`notify/smtptest`), WebSocket clients, a webhook receiver and an inbound MIME post.

## Automation wiring (D-19 §A)

- Lives in `internal/tickets` (`assign_strategy.go`, `automation_*.go`, `macros.go`) because actions reuse the ticket mutation primitives; the rule model is `internal/automation`. Routes are mounted by the tickets module (`mountAutomation`), the consumer and schedules by its `RegisterQueue`.
- **Actor `events.ActorAutomation` (`"automation"`).** Every event written by a rule or an assignment strategy carries this `actor_id` (no `users` row: actor lookups find nothing and show the change as automatic; notes and replies have `author_id NULL`). `mutation.addAs(actor, …)` writes one event under another actor than the rest of the mutation.
- **Consumer `automation`** (durable, 1 worker): filter `triggerOf(e) != ""` (`ticket.created`, `message.created`, the `ticket.updated` set) and never an event of the automation actor (loop guard). One write tx per source event, only when a rule matches and an action changes something; `automation_runs(event_id)` makes replays no-ops. Several events of one change run `ticket.updated` rules once (the last event of the burst evaluates). Business failures are recorded as `skipped` outcomes, only store errors are retried.
- `ticket.automation_applied` (staff only, `events.CustomerVisible` false) carries `events.AutomationApplied`.
- notify mails a public `message.created` of the automation actor as `customer.reply` (automated replies reach the customer); webhooks show the actor as `null`.
- Schedules: `automation.time_rules` (1 h), `automation.rr_flush` (30 s, coalesced round-robin pointers), `automation.prune` (24 h, `automation_runs` > 30 days).
- Assignment strategies run inside the mutation tx (create, transfer, escalation that clears, unassign, membership loss) through `Service.autoAssign`; candidates are the `Assignable` query minus `users.away_since IS NOT NULL`. Least busy counts owned tickets not resolved or closed (ties: lowest user id); round robin walks the candidates in user-id order from the team's in-memory pointer.
- **Routes** (`automation_mount.go`): rules (`automation_rules.go`: CRUD, reorder, dry-run; an in-memory cache of all rules in position order, invalidated on every write) and shared macros on the `Support` group (PATs need `admin`); personal macros on the `Agent` group (`tickets:read` / `tickets:write`); `/me/availability` on `Authed`, staff only (customers 404). `POST /agent/tickets/{id}/macros/{macro_id}/apply` is mounted with the ticket routes behind `Idempotent()`.
- **Macros** (`macros.go`): `Service.ApplyMacro` runs as the caller in one transaction (`If-Match` checked on the locked ticket): reply or note, then priority, tags, assignee, status. Every part needs the permission of the single action; one forbidden part rejects everything (403). Unlike a rule's `send_reply`, a macro's public reply is the agent's reply (SLA first response, `new` → `open`). `PATCH` replaces the whole macro.
- **Time rules** (`automation_time.go`): the hourly job selects non-closed tickets whose conditions hold and that have no `automation_time_runs` row for the rule newer than the ticket's last change; a change made by a time rule re-stamps `fired_at`, so it never re-arms itself.
- **Rule model** (`internal/automation`): pure validation (limits: 20 conditions, 10 actions, 500-character text and RE2 patterns, 50 list values, hours 1–8760) and `Evaluate(Facts)`; it imports no store, so the dry run and the consumer share it.

## Two-step verification wiring (D-19 §B)

- **Files.** `mfa.go`: policy (`security.mfa_required`, `mfaRequiredFor`), per-user state (`user_mfa`), challenges, recovery codes, email-code HMAC, the in-memory re-auth set. `mfa_http.go`: routes (`mountMFA`) and `mfaGate`. `mfa_cli.go`: `reset-mfa`. `totp.go`: RFC 6238 on the standard library (HMAC-SHA1, 30 s, 6 digits, ±1 step), no dependency.
- **Routes.** `/auth/mfa/*` on `Public` (the `mfa_token` is the credential); `/me/mfa*` on `Authed` with `RequireSession` (tokens 403); `/admin/security` and `/admin/users/{id}/mfa*` on `Admin` with `RequireScope(admin)`.
- **Gate.** `mfaGate(w, r, u, method)` runs after the first factor in password login, verify-email, magic link and invite accept (`loginUserID`). It returns false for IdP-managed users (`mfaApplies` is `!IDPManaged || breakGlassAdmin`); those never reach it locally (AUTH-044), and OIDC sign-ins never call it. An IdP-managed break-glass admin's password login is challenged like any local one. The login method travels in the challenge, so `auth.break_glass_login` and the throttle success are recorded only after the second step.
- **Throttle.** Per-IP buckets `mfa`, `mfa_email` and `reauth` (10/min); wrong codes and wrong re-auth passwords count toward the account soft-lock. Throttle and re-auth set are in memory (single node).
- **Mail.** `auth.mfa_code` through `notify.JobAuthMail` (transactional, 5 locales). Without SMTP the email method is hidden and `/auth/mfa/email/send` answers 409 `email-disabled`.
- **Schedule.** `authn.mfa_prune` (1 h) deletes expired and used challenges (`PruneMFAChallenges`).
- **Reset.** `authn.ResetMFA(ctx, tx, userID, actorID, ip, via)` deletes `user_mfa`, recovery codes and challenges and audits `auth.mfa_reset`; the admin route, `reset-mfa` and privacy erasure all call it. `settings.RotateSecrets` re-seals `user_mfa` secrets (not a forced enrolment's `mfa_challenges.totp_pending_enc`).
- **Consent hook** (`consent_hook.go`): `authn.SetRegisterConsent(fn)` lets the privacy module record the consent inside the registration transaction without authn importing privacy.

## Privacy wiring (D-19 §C)

- `privacyModule` (`internal/app/privacy_module.go`) comes after `ticketsModule` in `Registry`. It reads the tickets module from `builtModules` **without deleting the entry** (mailin consumes it last) and passes `(*tickets.Service).IndexOne` as `privacy.Deps.Reindex`, so a redaction refreshes the search index synchronously after commit.
- authn cannot import privacy: `authn.SetRegisterConsent(privacy.RecordConsent)` plugs the consent recorder into `POST /auth/register`. With `branding.legal.privacy_url` set, registration needs `accept_privacy: true` (422 `required`) and the hook inserts the `user_consents` row inside the registration transaction.
- The ready mail is `privacy.export_ready` through `notify.JobAuthMail`. `notify.Transactional(key)` (`auth.*`, `privacy.*`) marks templates admins can't disable and users can't opt out of.
- Erasure clears per-user rows of `user_mfa`, `mfa_recovery_codes`, `mfa_challenges` and `notifications` when those tables exist (`optionalUserTables`). A new per-user table with PII belongs in that list or in `Erase`.
- Queue: job kind `privacy.export` (`q.Handle`, 10 min timeout, 1 attempt; `Start` re-enqueues pending and processing exports after a restart) and the schedule `privacy.retention` (`q.Every`, 24 h): expire exports, auto-approve erasures, then closed tickets, inbound mail and audit rows. `RunRetention` and `PreviewRetention` share one rule builder, so the preview counts exactly what the next run processes.
- Routes: `/me/privacy*` on `Authed` (`POST /me/privacy/export` rate-limited, 1/min burst 3), `/admin/privacy/*` on `Admin`; PATs need `admin` on both.
- New blob prefix `privacy/` is **not** in `blob.Prefixes`: export files are ephemeral and are left out of backups on purpose. `store.WipeData` returns their keys.

## MCP wiring (`internal/mcp`)

- `mcpModule` (`internal/app/modules.go`) needs no other module: tools call the REST router. `Authenticate` ignores non-`/api/` paths, so `/mcp` resolves the caller itself with an in-process `GET /api/v1/me` carrying the bearer token (kind, role, effective scopes); every tool call is another in-process request with the same token, so a revoked token, a role change or a switched-off portal (customer tokens → 401) apply at once.
- **Inner requests must start clean:** `apiClient.do` clears the principal (`server.WithPrincipal(ctx, nil)`, or authn would skip the PAT scope check) and chi's route context (`chi.RouteCtxKey`, or the router would resume routing `/mcp`). It copies `RemoteAddr`, `X-Forwarded-*` and the request id, so rate limits and access-log lines line up with the outer call.
- Tool definitions (`tooldefs.go`) declare `Scopes` (the `x-scopes` of their first REST operation, checked by `TestToolScopesMatchSpec`) only to filter `tools/list`; enforcement stays in the REST layer. Staff and customer tool sets are separate lists; a staff-only tool has no customer definition.
- Tests: protocol in `internal/mcp/mcp_test.go` (fake REST handler); tools against the real app on both engines in `internal/app/e2e_mcp_test.go`.

## Notifications wiring

`internal/notify`, `internal/webhooks` and `internal/mailin` don't import `app`. `internal/app/modules.go` adapts `app.Deps` into each package's own `Deps` (`notifyModule`, `webhooksModule`, `mailinModule`).

- **TicketSink (inbound email → tickets).** The wiring goes through an `app.Deps`-keyed registry.
  - `modules.go` wraps every `Registry` entry with `recordBuilt`, then appends `mailinModule` **last**.
  - `mailinModule` uses the first earlier module that implements `mailin.TicketSink` (`CreateFromEmail`, `ReplyFromEmail`) or exposes `EmailSink() mailin.TicketSink`.
  - Tickets: add `tickets` to `Registry` and implement one of those two on the tickets module. Nothing else is needed. `(*mailin.Module).SetTicketSink` also exists for tests.
  - With no sink, inbound mail is recorded as `failed`/`no_sink`, and an admin can retry it from `/admin/inbound/messages/{id}/retry`.
  - `ReplyFromEmail` must apply the follow-up rule for closed tickets and return the resulting ticket id.
- **Staff replies by email.** `mailinModule` plugs in `authn.ResolveEmailSender` through `SetStaffAuthCheck`, called once per trusted authserv-id.
  - The trusted ids come from `FD_INBOUND_TRUSTED_AUTHSERV_IDS` and the per-mailbox `imap.trusted_authserv_id`.
  - Sender lookup and creation (unknown sender + self-registration → `pending_verification` customer with org auto-join) is mailin's internal `SenderResolver`, which can be replaced with `SetSenderResolver`.
- **Job kinds** (`internal/notify/contract.go`):
  - `mail.auth` takes `notify.AuthMail{template, user_id, to, locale, vars{url,…}}`. It is the same shape as `authn.MailPayload`.
  - `mail.admins` takes `notify.AdminMail{template, vars}`. webhooks uses it for `staff.webhook_disabled`.
  - Internal retry kinds: `notify.send`, `notify.coalesced`, `webhook.deliver`, `mailin.process`.
  - A failed first attempt inserts **one** `deferred_jobs` row (`attempts=1`, about 30 s later). A permanent SMTP 5xx inserts it directly as `dead`.
- **Email enabled.**
  - `notify.EnabledWith(ctx, *settings.Service)` is stateless and is true when the effective SMTP host is set.
  - `notify.Enabled(ctx)` uses the last-built notify module.
  - notify adds `smtp {configured, ok, last_error}` to `/admin/system`.
- **Opt-out.**
  - Opt-outs are stored in `users.notify_prefs` as `{"opt_out": ["staff.ticket_new", …]}`.
  - `notify.ValidateOptOut(keys)` returns field errors: `not_optional` for `auth.*` and `customer.reply`, and `invalid` for an unknown key. `PATCH /me` calls it.
- **Event data notify reads.** `note.created` data `mentioned` (or `mentions`) holds the staff user ids that get `staff.mentioned`. `message.created` data holds `message_id`, and the body is read from `ticket_messages`. Recipients come from `events.Snapshot` plus direct queries.
  - Visibility is a local predicate (team plus tier ≥ ticket tier, assignee, supervisors/admins, triage rule). It sits behind `notify.Visibility`, and authz can be swapped in with `SetVisibility`.
- **Threading ids** (`internal/notify/thread`):
  - `MessageID` = `<{event_id}.{sha256(addr)[:12]}@host>`.
  - `TicketThreadID` = `<ticket-{id}@host>`.
  - Plus-address token (`PlusToken` / `VerifyPlusToken`, HMAC via `crypto.Box.MAC`).
  - Localized `ReplyMarkers`.
  - `host` is the host of `FD_PUBLIC_URL`. Plus addressing is the global `settings.email.plus_addressing`.
- **Env:**
  - `FD_NOTIFY_COALESCE_SECONDS` (default 120, 0 = off; only `staff.customer_replied`).
  - `FD_WEBHOOK_ALLOW_PRIVATE`, `FD_WEBHOOK_ALLOW_INSECURE`.
  - `FD_MAX_ATTACHMENT_MB` (default `FD_MAX_UPLOAD_MB`; the inbound cap is 10×).
  - `FD_INBOUND_RATE_PER_HOUR` (default 20).
  - `FD_IMAP_HOST|PORT|SECURITY|USERNAME|PASSWORD|FOLDER|TEAM|ADDRESS` define one env mailbox, locking those fields.
  - `FD_TEST_IMAP_URL` enables the Dovecot integration test.
- **Schema workaround.** `inbound_messages.status` has no pending value. A recorded-but-unprocessed message is stored as `failed` with reason `pending`, and `mailin.Start` re-enqueues those rows.
- **Notification centre (D-19 §D, `internal/notify/inapp.go`, `inapp_http.go`).**
  - Table `notifications` (0130): `id, user_id, type, ticket_id, actor_id (no FK: system actors), event_id, params JSON, read_at, created_at`, unique `(event_id, user_id, type)`.
  - The consumer calls `resolve()` once per event. Email takes `emailTargets` (opt-outs applied, address dedupe); in-app takes the user targets (mailboxes excluded, `in_app_off` applied). Admin `DisabledTemplates` is email-only, and in-app does not depend on SMTP.
  - In-app writes are **one transaction per event**: a multi-row `INSERT … ON CONFLICT DO NOTHING RETURNING id` (a replay inserts nothing and sends no frame) plus one grouped unread-count query. Names and subjects are never copied into `params`: the list joins `tickets`, `projects` and `users` at read time (one query per page). Customer rows have empty `params` and an actor only on `customer.reply` (I-7, I-9).
  - D-20: `Notification` carries `ticket_ref` and `project` (`ProjectRef`; notify has its own `ProjectRefDTO` because `directory` imports `notify`). The list builds the current ref from `t.project_seq` and the joined key (`domain.TicketRef`). The frame takes `Snapshot.Ref` (null for pre-0141 events) and reads the chip of `Snapshot.Project()` with one primary-key query per event, after the write transaction and only when a row was inserted. The SPA (`src/notifications/projects.ts`) shows the chip under the ticket-list rule: agents `useProjectsStore().showSwitcher`, customers more than one `GET /portal/projects`, or rows from several projects.
  - Preferences: `users.notify_prefs` = `{"opt_out": […], "in_app_off": […], "browser_on": […]}` (email is the old opt-out list; in-app defaults on; browser is opt-in and needs in-app). `PATCH /me` `notification_prefs` goes through `notify.ValidatePrefs` and `notify.ApplyPrefs`; `GET /me` renders `notify.PrefsView`. Types: `notify.InAppTypes` = the `NotificationType` enum (not `TemplateKeys`).
  - Realtime: the module implements `RegisterRealtime` and uses `hub.SendUser` on `me`, with `notification.created {notification (with ticket_ref and project), unread_count, browser}` after commit and `notification.read {ids | all, unread_count}` after read, read-all or delete. These frames carry no `event_id` and are not replayed.
  - Retention: `queue.Every("notify.prune_notifications", 1h)` → `notify.PruneNotifications`, covering 90 days (batches of 1 000) and the newest 500 per user. `notifications` is in `store.copyOrder`.

## Realtime frames and hub behaviour

The user-facing protocol is [DOCUMENTATION.md §11](../DOCUMENTATION.md#11-websocket-protocol); `WsServerFrame` in `api/openapi.yaml` is the schema. Who publishes what:

| Topic | Frames | Published by |
|---|---|---|
| `project:<project_id>` (D-20) | `event` frames of the project's tickets, filtered per subscriber like `tickets` (team, tier, I-11); members, supervisors and admins may subscribe; customers 4403 | `internal/tickets/realtime.go` |
| `tickets`, `ticket:<id>`, `queue:<team_id>` | `event` frames for ticket events, including the staff-only `ticket.automation_applied`; `ticket.access_lost` through `hub.Revoke` | `internal/tickets/realtime.go` (`realtimeRouter`) |
| `counts` | `counts`, at most once per second | `realtimeRouter.flushCounts` |
| `me` | `assigned_to_you`, `mentioned` (staff, with `event_id`) | `realtimeRouter` through `hub.SendUser` |
| `me` | `notification.created`, `notification.read` (no `event_id`, never replayed) | `internal/notify/inapp.go` through `hub.SendUser`, after commit |
| `me` | `settings.branding_changed` (every connection) | `internal/app/app.go` through `hub.Broadcast` |
| `admin:system` | `queue.changed` | `internal/app/admin_topic.go` |
| `ticket:<id>` | `presence` | the hub (`setPresence` / `broadcastPresence`) |

Hub rules added with D-19 (`internal/realtime/hub.go`, `conn.go`):

- **`at` is always set.** `Publish`, `PublishFunc`, `SendUser`, `Broadcast` and `Revoke` stamp `at` on an `event` frame that has none, because `WsServerFrame` requires it.
- **Staff-only topic classes.** A customer subscribing to `counts`, `kb`, `queue:*`, `project:*` or `admin:*` is a protocol violation and is closed with `4403` (`staffOnlyTopic`), before any authorizer runs. Other topics a principal may not see are still answered in `ack.denied`.
- **Replay with backpressure.** A resume may replay up to 1 000 events (up to 3 000 frames across `tickets`, `ticket:<id>` and `queue:<team>`), far more than the per-connection send buffer. `conn.enqueueReplay` fills the buffer only to three quarters (a quarter stays free for live frames), waits on the writer's `sent` signal, and closes the client as slow (`4008`) when it reads nothing for `replayWait` (10 s). The old 48-frame replay cap in `internal/tickets/realtime.go` is gone (TKT-032).
- **Presence frames** always carry `users` (`omitzero`: an empty list stays on the wire).
- **Encoding failure.** A frame that fails to encode is replaced by `{"type":"resync","reason":"gap_too_large"}`, so the client refetches.

Client side (`web/src/realtime/client.ts`, `web/src/stores/realtime.ts`): `subscribe(topic, {essential})` and `useRealtime(topics, {essential})`. While the tab is hidden, `setHidden` unsubscribes every non-essential topic and re-subscribes them, with a `tab_visible` resync, when it is visible again. Essential topics (the notification store's `me`) stay subscribed, including across reconnects.

## Projects wiring (D-20)

- **Principal.** authn loads `Principal.Projects` (memberships, any role) and `Principal.AdminProjects` (`project_admin`) with the teams, in the same ≤ 30 s cache; `InvalidateTeams` drops both. Directory membership writes call `fireTeamsChanged` (wired to `authn.InvalidateTeams`); open sockets pick the change up at the 60 s recheck, and every frame is filtered with the current principal anyway. `Principal.InProject`, `AdminsProject`, `IsProjectAdmin`; supervisors and admins pass all of them.
- **I-11** lives in `authz.TicketPredicate` and `authz.CanSeeTicket` (kept in agreement by `TestCanRead_AgreesWithVisibilityPredicate*`), `notify` recipient visibility (`localVisibility` checks `project_members`), M-4 (`satisfiesM4` and `projectMemberSQL` in assignable and the assignment strategies), mentions and staff email replies (`staffInfo.principal()` carries the projects), reports (agents' own stats are limited to their projects), and the realtime topic authorizer. `authz.CanProjectConfig(p, res, projectID)` is the project-admin rule (tested by the permission matrix).
- **Directory.** The snapshot holds projects (`Project`, `TeamIDs`, `LogoURL`), team → project mapping (`Team.ProjectIDs`) and `Category.ProjectID`; lookups `Project`, `ResolveProject` (id or key), `DefaultProject` (Default while active, else first active), `TeamServesProject`, `StaffProjects`, `CustomerProjects` (portal-visible, active, organization limits), `PublicProject` (the `/auth/config?project=` view, plugged into authn with `authn.SetPublicProject` in `app/modules.go`). `directory.DefaultProjectSQL` mirrors `DefaultProject` for SQL writers (staff without `projects`, authn's setup/OIDC, mailin's new mailboxes).
- **Tickets.** `createTx` resolves the project (`ticketProject`: follow-up, explicit, mailbox, category, customer's only project, default), checks category/team/membership, allocates `NextProjectSeq` in the same transaction. `moveProject` handles `PATCH project_id`. SLA: `slaPolicyFor(project, priority)` (project override, else global) from one cache. Automation: `Rule.ProjectID` scopes `Evaluate` (`Rule.InScope`), `Facts.ProjectID` feeds the `project` condition. Webhooks filter with `hook.inScope` on the snapshot project.
- **Email.** `notify` views carry `Ref` (`viewRef`: the snapshot ref, `#n` for old events) and the subject tag is `[REF]`; templates use `{{.Ticket.Ref}}`. mailin threads `[KEY-123]` (case-insensitive) before `[#n]`, and passes the mailbox's `ProjectID` to the sink.
