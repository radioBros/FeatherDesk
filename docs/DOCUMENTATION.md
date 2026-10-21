# SimpleTickets Documentation

The complete manual for SimpleTickets, a self-hosted help desk that runs as one container. It is written for the people who install and run it, the support staff who work in it, and developers who integrate with it. SimpleTickets is developed by [RadioBros](https://radiobros.com/).

- Start with the [README](../README.md) for a quick overview and a three-command install.
- The reference for core environment variables is [configuration.md](configuration.md). This manual covers every module variable too.
- The API contract is [`api/openapi.yaml`](../api/openapi.yaml). A running instance serves it at `/api/v1/openapi.yaml`.
- Four features have their own deep-dive guide, with every option and API detail: [Automation and assignment](features/automation.md), [Two-step verification](features/mfa.md), [Privacy and GDPR](features/gdpr.md) and the [Notification centre](features/notifications.md). This manual says what each one does and where to find it.

Examples use `https://help.example.com` as the public address of the help desk and `https://auth.example.com` as the identity provider.

## Table of contents

1. [Concepts](#1-concepts)
   - [Staff and customers](#11-staff-and-customers)
   - [Roles, tiers and teams](#12-roles-tiers-and-teams)
   - [Who sees which ticket](#13-who-sees-which-ticket)
   - [Ticket lifecycle](#14-ticket-lifecycle)
   - [Escalation](#15-escalation)
   - [SLA and business hours](#16-sla-and-business-hours)
   - [Merge, split and links](#17-merge-split-and-links)
2. [Installation](#2-installation)
   - [Requirements](#21-requirements)
   - [Docker Compose](#22-docker-compose)
   - [PostgreSQL option](#23-postgresql-option)
   - [S3 storage option](#24-s3-storage-option)
   - [Kubernetes and Helm](#25-kubernetes-and-helm)
   - [Reverse proxies and TLS](#26-reverse-proxies-and-tls)
3. [Configuration](#3-configuration)
   - [Environment variables and the admin UI](#31-environment-variables-and-the-admin-ui)
   - [Locking rules](#32-locking-rules)
   - [Variable reference](#33-variable-reference)
4. [First-run setup](#4-first-run-setup)
5. [Single sign-on (SSO)](#5-single-sign-on-sso)
   - [How SSO works in SimpleTickets](#51-how-sso-works-in-simpletickets)
   - [Authentik step by step](#52-authentik-step-by-step)
   - [Group mapping: roles, tiers, teams](#53-group-mapping-roles-tiers-and-teams)
   - [Break-glass administrators](#54-break-glass-administrators)
   - [Logout and back-channel logout](#55-logout-and-back-channel-logout)
   - [Generic OIDC and Keycloak](#56-generic-oidc-and-keycloak)
   - [Two-step verification and SSO](#57-two-step-verification-and-sso)
6. [Email](#6-email)
   - [Outgoing email (SMTP)](#61-outgoing-email-smtp)
   - [Sender identities and mailboxes](#62-sender-identities-and-mailboxes)
   - [Notifications, templates and languages](#63-notifications-templates-and-languages)
   - [Inbound email](#64-inbound-email)
   - [Threading](#65-threading)
   - [Loop protection, quarantine and staff replies](#66-loop-protection-quarantine-and-staff-replies)
7. [Admin guide](#7-admin-guide)
   - [Staff](#71-staff-people--staff)
   - [Teams](#72-teams-people--teams-supervisor)
   - [Categories](#73-categories-support-setup--categories-supervisor)
   - [Custom fields](#74-custom-fields-support-setup--custom-fields-admin)
   - [SLA policies and business hours](#75-sla-policies-and-business-hours-supervisor)
   - [Canned responses and tags](#76-canned-responses-and-tags-supervisor)
   - [Knowledge base](#77-knowledge-base-supervisor-for-categories-and-publishing)
   - [Branding](#78-branding-system--branding-admin)
   - [Support settings](#79-support-settings-system--support-settings-admin)
   - [Email and inbound email](#710-email-and-inbound-email-channels-admin)
   - [Webhooks](#711-webhooks-channels--webhooks-admin)
   - [API tokens](#712-api-tokens-system--all-api-tokens-admin)
   - [Audit log](#713-audit-log-system--audit-log-admin)
   - [Queue and failed jobs](#714-queue-and-failed-jobs-system--queue--failed-jobs-admin)
   - [System and storage](#715-system-and-storage-system--system--storage-admin)
   - [Automation and assignment](#716-automation-and-assignment-support-setup-supervisor)
   - [Two-step verification](#717-two-step-verification-system--support-settings-admin)
   - [Privacy and GDPR](#718-privacy-and-gdpr-people--privacy-admin)
   - [Notification centre](#719-notification-centre)
   - [AI assistants (MCP)](#720-ai-assistants-mcp-system--support-settings-admin)
   - [Projects](#721-projects-administration--projects-supervisor)
8. [Agent guide](#8-agent-guide)
   - [Layout](#81-layout)
   - [Ticket list](#82-ticket-list)
   - [Working a ticket](#83-working-a-ticket)
   - [Escalating and handing back](#84-escalating-and-handing-back)
   - [Links, merge and split](#85-links-merge-and-split)
   - [Creating tickets for customers](#86-creating-tickets-for-customers)
   - [Customers, organizations, knowledge base and reports](#87-customers-organizations-knowledge-base-and-reports)
   - [Profile, tokens and shortcuts](#88-profile-tokens-and-shortcuts)
   - [Macros and availability](#89-macros-and-availability)
   - [Notification centre](#810-notification-centre)
9. [Customer portal guide](#9-customer-portal-guide)
10. [REST API](#10-rest-api)
    - [Areas](#101-areas)
    - [Authentication](#102-authentication)
    - [Pagination, filtering and sorting](#103-pagination-filtering-and-sorting)
    - [Errors](#104-errors)
    - [Idempotency](#105-idempotency)
    - [Optimistic concurrency (`If-Match`)](#106-optimistic-concurrency-if-match)
    - [Ticket updates and `allowed_actions`](#107-ticket-updates-and-allowed_actions)
    - [Examples](#108-examples)
    - [Automation, two-step verification, privacy and notifications](#109-automation-two-step-verification-privacy-and-notifications)
    - [AI assistants (MCP)](#1010-ai-assistants-mcp)
11. [WebSocket protocol](#11-websocket-protocol)
12. [Webhooks](#12-webhooks)
13. [Operations](#13-operations)
14. [Security model](#14-security-model)
15. [Development and contributing](#15-development-and-contributing)
16. [FAQ](#16-faq)

---

## 1. Concepts

### 1.1 Staff and customers

Every account is either **staff** or a **customer**. This "kind" is set when the account is created and can never change. If a person needs both, they need two accounts with two email addresses.

| | Staff | Customers |
|---|---|---|
| Where they work | Agent panel at `/agent` | Customer portal at `/` |
| Login page | `/agent/login` | `/login` |
| Usual sign-in | Single sign-on (staff OIDC client) | Password, magic link, or SSO (customer OIDC client) |
| API area | `/api/v1/agent/*`, `/api/v1/admin/*` | `/api/v1/portal/*` |
| Roles | `admin`, `supervisor`, `agent` | `requester`, `org_manager` |

**Staff-only mode.** An admin can switch the whole customer web app off with **Customer portal enabled** ([§7.9](#79-support-settings-system--support-settings-admin)). Then only admins and staff sign in, see [Switching the customer portal off](#switching-the-customer-portal-off).

The two audiences are kept apart in the code, not just by a setting:
- Customers are served by separate handlers and separate database queries.
- A customer who calls a staff URL gets **404 Not Found**, as if the URL did not exist.
- Customer queries are written so they cannot select internal notes.

**Organizations** group customers, typically by company.
- An organization can list **email domains**. A customer who verifies an address on one of those domains joins the organization automatically.
- An **organization manager** (`org_manager`) sees every ticket of their organization. They can open tickets on behalf of a colleague, invite colleagues, and disable or re-enable them.
- A **requester** sees only their own tickets, plus tickets they were added to as a watcher.

### 1.2 Roles, tiers and teams

Staff are described by three independent settings. Keeping them separate means a senior engineer can handle hard tickets without also getting the right to manage users.

| Axis | Values | Meaning |
|---|---|---|
| **Role** | `admin` · `supervisor` · `agent` | What you may configure |
| **Tier** | `1` · `2` · `3` | How deep into the escalation chain you work. Admins and supervisors always count as tier 3 |
| **Team** | zero or more teams | Which queues you work, e.g. *Billing* or *Technical* |

- There are always exactly three tiers. Their display names are set under **Admin → Support settings**. The defaults are `L1`, `L2` and `L3`; you could use, for example, *Front desk*, *Specialist* and *Engineering*.
- Every ticket has a **team** (or none, which means it is in **triage**) and a **tier** (1 by default).

What each role may do:

| Area | Agent | Supervisor | Admin |
|---|---|---|---|
| Work tickets they can see (reply, note, status, priority, category, tags, transfer, link, split) | yes | yes | yes |
| See every ticket in every team | no | yes | yes |
| Reopen a closed ticket | no | yes | yes |
| Delete a ticket (audited) | no | no | yes |
| Customers and organizations | read, create | read, write | read, write |
| Teams and membership, categories, SLA policies, business calendars, canned responses | no | yes | yes |
| Automation rules, shared macros, a team's automatic assignment | no | yes | yes |
| Personal macros, the *Away* switch | yes | yes | yes |
| Knowledge base | write drafts | write, publish, manage categories | write, publish, manage categories |
| Custom field definitions, inbound mailboxes, storage | no | no | yes |
| Staff accounts (create, role, tier, disable) | no | no | yes |
| Branding, email, support settings | no | no | yes |
| Webhooks, all API tokens, audit log, queue, failed jobs, system | no | no | yes |
| Two-step verification policy and resets, privacy requests, data exports of others, erasure, retention | no | no | yes |
| Reports | own numbers | everything | everything |

Actions on a single ticket depend on the role, the tier, team membership, whether the user is the assignee, and the ticket's status:

| Action | Agent L1 | Agent L2 | Agent L3 | Supervisor | Admin |
|---|---|---|---|---|---|
| Read, public reply, internal note | yes | yes | yes | yes | yes |
| Assign to self | if in the team and tier ≥ ticket tier | same | same | yes | yes |
| Assign to someone else | if in the team, and the target can see the ticket | same | same | yes | yes |
| Unassign | only if they are the assignee | same | same | yes | yes |
| Escalate (tier + 1) | yes | yes | no (already at the top) | yes | yes |
| De-escalate (tier − 1) | no | if tier ≥ ticket tier and they are the assignee or a team member | same | yes | yes |
| Close a resolved ticket | yes | yes | yes | yes | yes |
| Reopen a closed ticket | no | no | no | yes | yes |
| Edit their own internal note | within 15 minutes | same | same | yes | yes |
| Merge into another ticket | if they can see both and are the assignee of the source | same | same | yes | yes |
| Delete ticket | no | no | no | no | yes |

Two support settings change these rules:
- **Only supervisors can set Urgent** (`urgent_requires_supervisor`, off by default) limits the `urgent` priority to supervisors and admins.
- **Customers can choose the Urgent priority** (`customer_urgent_allowed`, off by default) lets customers pick `urgent` when they create a ticket. Otherwise they can choose `low`, `normal` or `high`.

The server works out which actions the current user may take on a ticket and returns them in the ticket's `allowed_actions` list. The UI shows a button only if its action is in that list. You never have to replicate the rules in an integration: read `allowed_actions`.

### 1.3 Who sees which ticket

| Who | Sees a ticket when… |
|---|---|
| Agent | it is in one of **their projects** (I-11), **and** it is assigned to them, **or** it is in one of their teams and its tier is ≤ their tier, **or** they watch it. Triage tickets (no team) are visible to agents only while the support setting **Agents can see the triage queue** is on, which is the default |
| Supervisor, admin | always |
| Requester | they are the requester, or a watcher |
| Organization manager | the ticket belongs to their organization, or they are the requester or a watcher |

- Reading anything outside your visibility returns **404**, so the API never confirms that a ticket exists.
- An action you are not allowed to take on a ticket you *can* see returns **403**.
- A change can remove your own access, for example escalating past your tier or moving the ticket to a team you are not in. When that happens, the API response says `visible: false` and the agent panel takes you back to the list with the message "Ticket handed over. It is no longer in your queue."

### 1.4 Ticket lifecycle

| Status | Meaning | SLA clock | Customers see |
|---|---|---|---|
| `new` | Created; no staff action yet | running | Open |
| `open` | Staff is working on it | running | Open |
| `pending` | Waiting for the **customer** | paused | Awaiting your reply |
| `on_hold` | Waiting for a third party or an internal dependency | paused | Open |
| `resolved` | Believed solved | stopped | Resolved |
| `closed` | Final; the ticket can no longer change | stopped | Closed |

Allowed transitions:

| From → To | How |
|---|---|
| created → `new` | A customer, staff member, API client or email creates the ticket. It gets a number, its SLA due dates are computed, and the requester and team are notified |
| `new` → `open` | Automatically on the first public staff reply or on assignment, or set explicitly. Internal notes do not count as a first response |
| `new` / `open` / `on_hold` → `pending` | Set by staff, usually together with a reply ("Send and set status") |
| `pending` → `open` | Automatically when the customer replies |
| `new` / `open` → `on_hold` | Set by staff |
| `on_hold` → `open` | Set by staff, or automatically when the customer replies |
| any open status → `resolved` | Set by staff, or by the customer with "My issue is solved". The requester gets an email with a satisfaction-rating link |
| `resolved` → `open` | The customer replies or clicks "Reopen" within the **reopen window** (default 7 days), or staff reopen it |
| `resolved` → `closed` | Closed by staff, or automatically after **auto-close days** (default 7). The auto-close job runs every hour |
| `closed` → `open` | Supervisors and admins only, and audited |
| Customer reply to a `closed` ticket | Creates a **follow-up ticket** linked to the original, with the same team and category and tier 1 |

- Any other transition is refused with **409** and the problem type `invalid-transition`. The response lists the statuses that are allowed from the current one.
- The reopen window and auto-close days are set under **Admin → Support settings**.

The agent list's default sort, **Needs attention**, works like this:
1. Tickets in `new` or `open` come first.
2. Among those, tickets where the customer spoke last come first.
3. Then higher priority, then the earliest due date.

### 1.5 Escalation

Escalation moves a ticket **up one tier** (to a maximum of 3). De-escalation moves it **down one tier**.

- A reason is always required. It is stored in the timeline, together with an internal system message, so staff can see why.
- Escalation can move the ticket to another team in the same step. For example, an L1 *Front desk* ticket can go to L2 *Technical*.
- If the current assignee can't see the ticket at its new tier or in its new team, the ticket is **unassigned** and goes back to the queue. The members of the target team at or above the new tier are notified. If that team uses [automatic assignment](#716-automation-and-assignment-support-setup-supervisor), the ticket is handed to the next eligible agent rather than waiting in the queue.
- After de-escalation the assignee keeps the ticket, and nobody is notified.
- Escalation does not change the status, except that `new` becomes `open`.
- **Escalation does not reset the SLA.** The customer's clock keeps running.
- Customers never see tiers, teams or escalation reasons.

### 1.6 SLA and business hours

SLA policies are defined **per priority** (`low`, `normal`, `high`, `urgent`) under **Admin → SLA policies**. Each policy has three parts:
- a **first response** target, in minutes;
- a **resolution** target, in minutes;
- a **clock**:
  - **Around the clock (24×7)**;
  - **Team's business hours**: the clock only runs during the hours of the ticket's team's calendar.

**Business calendars** are set up under **Admin → Business hours**. Each calendar has:
- an IANA time zone, for example `Europe/Rome`;
- weekly hours, with several intervals per day allowed;
- holidays.

Each team can point to one calendar. A team without a calendar is "Always open (24×7)". Due dates are computed by walking the calendar's open intervals, so they come out right across daylight-saving changes.

How the SLA is computed:
- The first-response due time is the creation time plus the first-response target.
- The resolution due time is the creation time plus the resolution target plus any time spent paused in `pending` or `on_hold`.
- Changing the priority recomputes both due times from the creation time.
- A job runs every minute:
  - at **80 %** of a target it raises `sla.warning`;
  - at **100 %** it raises `sla.breached`.
- Both notify the assignee, or the team when there is no assignee, plus the supervisors. They are also available as webhooks.

### 1.7 Merge, split and links

- **Links** connect tickets. The types are *Parent of*, *Related to* and *Duplicate of*. You must be able to see both tickets.
  - When the last open child ticket is resolved, the parent's assignee is notified.
  - Resolving a parent that still has open children shows a warning but is allowed.
- **Merge** moves the messages, attachments and watchers of ticket A into ticket B.
  - A is closed and marked as a duplicate of B. Both tickets get a system message.
  - If the requesters differ but are in the same organization, A's requester becomes a watcher of B.
  - Merging tickets from unrelated customers is refused (`merge-cross-requester`), because it would leak one customer's conversation to another. Link them instead.
  - The target ticket must not be closed.
- **Split** copies the selected messages into a new **child ticket**, with the same requester and team and tier 1. The original keeps its messages, and they are marked as split.

---

## 2. Installation

### 2.1 Requirements

- A Linux host with Docker Engine 25+ and the Compose plugin v2.24.4+, or a Kubernetes cluster. The compose files use features older versions reject: the long `env_file` syntax, the health-check `start_interval`, and `!reset`/`!override`.
- Hardware: 1 vCPU and 512 MB of RAM are plenty. The app is designed to run with a 256 MB container limit.
- A DNS name and a TLS certificate, normally provided by your reverse proxy.
- Optional extras:
  - an SMTP server for outgoing email;
  - an IMAP mailbox or a mail provider that can POST raw MIME, for incoming email;
  - an OpenID Connect provider such as Authentik or Keycloak;
  - PostgreSQL;
  - S3-compatible object storage.

**Everything** runs in one container, including the database (SQLite), REST API, WebSocket hub, job queue, scheduler and web app. Run **one** instance per data volume and per database.
- The server takes an exclusive lock on `$ST_DATA_DIR/.lock` and refuses to start if another instance holds it.
- Never run two replicas. The queue, the WebSocket hub and the write-behind buffers live in process memory.

### 2.2 Docker Compose

The production compose file `deploy/compose.yml` defines a single service, `app`, with a hardened setup:
- read-only root filesystem, with `/tmp` on a tmpfs;
- every capability dropped, and `no-new-privileges`;
- a non-root user;
- a `/data` volume for all state;
- a health check that runs `/simpletickets healthcheck`;
- example resource limits.

**1. Get the files and create `deploy/.env`.**

```sh
git clone https://github.com/radioBros/SimpleTickets.git
cd SimpleTickets
cp .env.example deploy/.env
```

The compose file reads its environment from `deploy/.env`, next to `deploy/compose.yml`. Set `ST_ENV_FILE` to use a different file; a relative path is resolved against `deploy/`, so use an absolute path for a file elsewhere.

**2. Edit `deploy/.env`.** A fresh install needs two values, `ST_SECRET_KEY` and `ST_PUBLIC_URL`, plus `ST_TRUSTED_PROXIES` once a reverse proxy is in front (leave it out for a local test on `http://localhost:8080`):

```env
ST_SECRET_KEY=<output of: openssl rand -base64 32>
ST_PUBLIC_URL=https://help.example.com
ST_TRUSTED_PROXIES=172.16.0.0/12        # the network your reverse proxy connects from
```

`ST_SECRET_KEY` encrypts the stored SMTP, IMAP and webhook secrets and signs tokens.
- **Back it up together with your data.** Without it, the encrypted secrets can't be read.
- To rotate it, put the old value in `ST_SECRET_KEY_PREVIOUS` and the new one in `ST_SECRET_KEY`. The old key is still accepted for decryption. Then run `simpletickets rotate-secrets` to re-encrypt every stored secret with the new key, and remove `ST_SECRET_KEY_PREVIOUS`.

**3. Start it and open the setup wizard.** No release image is published on `ghcr.io` yet, so build it from the checkout first (about a minute; later releases make this step unnecessary, and `docker compose pull` then replaces the local build):

```sh
docker build -t ghcr.io/radiobros/simpletickets:latest .
docker compose -f deploy/compose.yml up -d
docker compose -f deploy/compose.yml logs app | grep "setup token"
```

Open `https://help.example.com/setup`, paste the token and follow [First-run setup](#4-first-run-setup).

**Useful commands:**

```sh
docker compose -f deploy/compose.yml ps                                          # health
docker compose -f deploy/compose.yml exec app /simpletickets version
docker compose -f deploy/compose.yml exec app /simpletickets config check        # print effective config (secrets masked)
docker compose -f deploy/compose.yml exec app /simpletickets backup              # online backup into /data/backups
```

The image is distroless, so there is no shell inside the container. Every maintenance task is a subcommand of `/simpletickets`; see [CLI reference](#132-cli-reference).

### 2.3 PostgreSQL option

SQLite is the default and the best choice for small and medium installs:
- one file, nothing else to run;
- the lowest memory use;
- online backups with `simpletickets backup`.

Choose PostgreSQL if you already run it, want point-in-time recovery, or prefer to manage the database separately. (SQLite can get continuous off-host replication too, with the optional Litestream overlay: see [Backup and restore](#131-backup-and-restore).) **The app is still a single process.** PostgreSQL doesn't make it horizontally scalable.

**With the bundled overlay.** `deploy/compose.postgres.yml` adds a `postgres` service (PostgreSQL 18, tuned for small hosts, limited to 128 MB) and sets `ST_DATABASE_URL` for the app:

<!-- doctest:pg-overlay -->
```sh
# in deploy/.env: ST_PG_PASSWORD=<random>   (optional: ST_PG_USER, ST_PG_DB; both default to simpletickets)
docker compose -f deploy/compose.yml -f deploy/compose.postgres.yml up -d
```

From then on, pass both `-f` files to every `docker compose` command.

**With an external PostgreSQL**, set the URL yourself:

```env
ST_DATABASE_URL=postgres://simpletickets:secret@db.internal:5432/simpletickets?sslmode=require
ST_DB_POOL=5            # connection pool size (default 5)
```

Migrations for the selected engine run on boot. `/readyz` reports the engine: `ready postgres` or `ready sqlite`.

**Moving an existing install from SQLite to PostgreSQL** (offline):

1. Stop the app and take a backup.
2. Create an empty PostgreSQL database and bring it to the current schema by running `simpletickets migrate` with `ST_DATABASE_URL` set to it.
3. Copy the data:

   <!-- doctest:migrate-engine -->
   ```sh
   simpletickets migrate-engine \
     --from sqlite:///data/simpletickets.db \
     --to   'postgres://simpletickets:secret@db:5432/simpletickets?sslmode=disable'
   ```

4. Set `ST_DATABASE_URL` to the PostgreSQL URL and start the app.

How `migrate-engine` copies the data:
- Both databases must be at the same schema version, and the target must have no users or tickets.
- Each table is verified by row count and checksum.
- If the report says the search index needs rebuilding, run `simpletickets reindex`.

It works in the other direction too (PostgreSQL → SQLite).

**Backups with PostgreSQL:**
- `simpletickets backup` only saves the attachments and prints the matching `pg_dump` command.
- Back up the database with `pg_dump -Fc` and restore it with `pg_restore --clean`.
- See [Backup and restore](#131-backup-and-restore).

### 2.4 S3 storage option

By default, attachments, branding images, knowledge-base images and raw inbound emails are stored under `ST_DATA_DIR`. To use any S3-compatible store (AWS S3, MinIO, Cloudflare R2, Backblaze B2, Garage, …), set:

```env
ST_BLOB_URL=s3://my-bucket/simpletickets?endpoint=https://s3.eu-central-1.amazonaws.com&region=eu-central-1
ST_S3_ACCESS_KEY=AKIA…
ST_S3_SECRET_KEY=…
```

- Credentials always come from `ST_S3_ACCESS_KEY` and `ST_S3_SECRET_KEY`, never from the URL. Both are required when `ST_BLOB_URL` starts with `s3://`.
- Add `path_style=true` for stores that need path-style addressing, such as MinIO, e.g. `s3://bucket/prefix?endpoint=http://minio:9000&region=us-east-1&path_style=true`.
- `file:///path` selects local storage in another directory.
- The S3 client is built in. It uses SigV4 signing and doesn't need an AWS SDK.
- **Admin → Storage** shows the active backend.

**Moving existing files to S3:**

<!-- doctest:blob-migrate -->
```sh
simpletickets blob-migrate --to 's3://my-bucket/simpletickets?endpoint=…&region=…'
# prints: copied N blobs (B bytes); set ST_BLOB_URL=<to> and restart
```

`blob-migrate` copies every attachment, branding asset, knowledge-base image and raw inbound message, and checks the SHA-256 of each. Then set `ST_BLOB_URL` and restart.

With S3, `simpletickets backup` skips the files. Protect them with bucket versioning or lifecycle rules.

### 2.5 Kubernetes and Helm

Two options are provided:
- `deploy/k8s/`: plain manifests (Namespace, ConfigMap, Secret template, PersistentVolumeClaim, Deployment, Service and an Ingress example).
- `deploy/helm/simpletickets/`: a Helm chart that renders the same objects.

**Plain manifests:**

<!-- doctest:k8s-plain -->
```sh
# 1. edit deploy/k8s/10-configmap.yaml (ST_PUBLIC_URL, ST_TRUSTED_PROXIES, …) and 50-ingress.yaml (host, TLS)
kubectl apply -f deploy/k8s/00-namespace.yaml
# 2. create the Secret yourself instead of applying the 11-secret.example.yaml template
kubectl -n simpletickets create secret generic simpletickets \
  --from-literal=ST_SECRET_KEY="$(openssl rand -base64 32)"
# 3. apply the rest
for f in deploy/k8s/*.yaml; do case $f in *secret.example*) ;; *) kubectl apply -f "$f";; esac; done
```

Optional keys in the Secret are `ST_DATABASE_URL` (external PostgreSQL), `ST_BLOB_URL` with `ST_S3_ACCESS_KEY` and `ST_S3_SECRET_KEY` (S3), `ST_OIDC_STAFF_CLIENT_SECRET`, `ST_SMTP_PASSWORD` and `ST_IMAP_PASSWORD`.

**Helm:** a minimal `my-values.yaml` looks like this. All keys are documented in `deploy/helm/simpletickets/values.yaml`.

<!-- doctest:helm-values -->
```yaml
env:
  ST_PUBLIC_URL: https://help.example.com
  ST_TRUSTED_PROXIES: 10.0.0.0/8          # pod network of your ingress controller
secret:
  existingSecret: simpletickets-secrets   # must contain ST_SECRET_KEY (+ e.g. ST_OIDC_STAFF_CLIENT_SECRET)
ingress:
  enabled: true
  host: help.example.com
persistence:
  size: 5Gi
# Optional:
# database: { existingSecret: simpletickets-db, key: ST_DATABASE_URL }     # external PostgreSQL
# blob: { url: "s3://bucket/prefix?endpoint=…&region=…", existingSecret: simpletickets-s3 }  # ST_S3_ACCESS_KEY / ST_S3_SECRET_KEY
# imap: { ST_IMAP_HOST: imap.example.com, ST_IMAP_USERNAME: support@example.com }
```

<!-- doctest:helm-install -->
```sh
helm install simpletickets deploy/helm/simpletickets \
  --namespace simpletickets --create-namespace -f my-values.yaml
```

When the pod is ready, read the setup token from its log:

<!-- doctest:helm-token -->
```sh
kubectl -n simpletickets rollout status deploy/simpletickets --timeout=120s
kubectl -n simpletickets logs deploy/simpletickets | grep "setup token"
```

The chart creates its own Secret if you don't name one. When `secret.values.ST_SECRET_KEY` is empty, it generates a random key at install and keeps it across upgrades.

Rules that matter on Kubernetes:
- **Exactly one replica, with `strategy: Recreate`.** A rolling update would briefly run two pods against one volume and two in-memory queues. The chart refuses to render with more than one replica.
- **Persistent volume** for `/data`, even with PostgreSQL and S3, because the instance lock lives there and so do local backups.
- **Probes:** liveness `GET /healthz`, readiness `GET /readyz`.
- **Security context:** non-root, read-only root filesystem, every capability dropped, `RuntimeDefault` seccomp profile, and an `emptyDir` for `/tmp`.
- **Resources:** a memory limit of 256 Mi is enough. `GOMEMLIMIT` is derived from the limit automatically.
- **Secrets:** keep `ST_SECRET_KEY`, the database URL, the S3 keys and OIDC client secrets in a Kubernetes Secret.
- **Ingress:** it must allow WebSocket upgrades on `/api/v1/ws`, and its read timeout must be above 60 s. The chart's ingress-nginx defaults set `proxy-read-timeout`/`proxy-send-timeout` to 3600 s and `proxy-body-size` to 30m.

### 2.6 Reverse proxies and TLS

SimpleTickets listens on plain HTTP (`ST_LISTEN_ADDR`, default `:8080`). TLS is handled by a reverse proxy in front of it. Examples are in `deploy/examples/`: `Caddyfile`, Traefik labels and an nginx configuration.

Whichever proxy you use:
1. **Forward WebSocket upgrades** for `/api/v1/ws`. Caddy and Traefik do this automatically; nginx needs explicit headers.
2. **Keep idle connections open for more than 60 s.** The server pings every 25 s and drops a connection after 60 s of silence. 75 s is a good proxy read timeout.
3. **Send `X-Forwarded-For` and `X-Forwarded-Proto`**, and list the proxy's address range in `ST_TRUSTED_PROXIES`. Otherwise the logs, rate limits and the audit log record the proxy's IP instead of the client's.
4. **Set `ST_PUBLIC_URL` to the public `https://` address.** Cookies are then always marked `Secure`, and the browser's `Origin` matches the server's check.
5. **HSTS:** the app already sends `Strict-Transport-Security` on HTTPS requests, including requests that a trusted proxy marks as HTTPS.

**Caddy** (automatic TLS, upgrades forwarded automatically). This is a shortened `deploy/examples/Caddyfile`, for Caddy running on the host:

```caddyfile
help.example.com {
	encode zstd gzip
	reverse_proxy 127.0.0.1:8080 {
		stream_timeout 24h
	}
}
```

**nginx** (shortened from `deploy/examples/nginx.conf`, which also has the HTTP→HTTPS redirect and certificate paths):

```nginx
map $http_upgrade $connection_upgrade { default upgrade; '' close; }

server {
    listen 443 ssl;
    http2 on;
    server_name help.example.com;
    # ssl_certificate / ssl_certificate_key …

    client_max_body_size 30m;          # ≥ ST_MAX_UPLOAD_MB

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_read_timeout 75s;
    }
}
```

**Traefik v3:** `deploy/examples/traefik.compose.yml` adds the labels to the `app` service. It assumes an existing Traefik on the external network `proxy`, with an entrypoint `websecure` and a certificate resolver `le`. WebSockets work without extra middleware.

```sh
docker compose -f deploy/compose.yml -f deploy/examples/traefik.compose.yml up -d
```

Test the WebSocket path through your proxy by signing in to the agent panel. If you see the banner "Connection lost — reconnecting…" over and over, the proxy isn't forwarding the upgrade. See [Troubleshooting](#136-troubleshooting).

---

## 3. Configuration

### 3.1 Environment variables and the admin UI

Settings come from two places:

| Source | What lives there |
|---|---|
| Environment variables (`ST_*`) | Everything about the process: secret key, URLs, database, storage, limits, SSO, logging. Optionally SMTP and one IMAP mailbox |
| Admin UI (stored in the database) | Branding, SMTP and sender identities, notification switches, inbound mailboxes, support settings (tier names, reopen and auto-close windows, self-registration, urgent-priority policy, triage visibility, knowledge base on/off and sitemap, default language), the two-step verification policy, privacy and retention settings, automation rules, macros and each team's automatic assignment |
| Each user's profile (stored on the account) | Notification preferences per type and channel, the agent's *Away* switch, the user's own two-step verification methods |

Automation, two-step verification, privacy and the notification centre add **no** environment variables. Their settings live in the database and are listed in [Settings stored in the database](#settings-stored-in-the-database).

Configuration errors are collected and reported **all at once** at startup, so you can fix everything in one go. The process then exits with code 2. To check a configuration without starting the server, run:

```sh
simpletickets config check   # prints every core variable (secrets masked) and "configuration OK"
simpletickets config docs    # prints the core variable table as Markdown
```

### 3.2 Locking rules

Some settings can come from **both** the environment and the admin UI. For those, these rules apply:

1. **The environment wins.** When a matching `ST_*` variable is set, its value is used, whatever is stored in the database.
2. **The field is locked in the UI.** The admin page shows a "Set by environment" badge and the field can't be edited. The API lists such fields in `locked_by_env`.
3. **The stored value is kept.** Saving the form doesn't overwrite an env-locked field. If you remove the variable later, the stored value applies again.
4. **Invalid values are ignored.** A value that doesn't parse, such as a non-numeric port or a malformed address, is skipped without an error. Check the admin page to confirm the value was picked up.

Which settings can be locked:

| Setting | Locked by |
|---|---|
| SMTP connection (Admin → Email) | `ST_SMTP_HOST`, `ST_SMTP_PORT`, `ST_SMTP_SECURITY`, `ST_SMTP_USERNAME`, `ST_SMTP_PASSWORD`, `ST_SMTP_AUTH`, `ST_SMTP_HELO`, `ST_SMTP_TIMEOUT_SECONDS`, `ST_SMTP_ALLOW_INSECURE_TLS` |
| Sender identities (Admin → Email) | `ST_SMTP_FROM_CUSTOMER`, `ST_SMTP_FROM_STAFF` |
| The environment mailbox (Admin → Inbound email) | `ST_IMAP_HOST`, `ST_IMAP_PORT`, `ST_IMAP_SECURITY`, `ST_IMAP_USERNAME`, `ST_IMAP_PASSWORD`, `ST_IMAP_FOLDER`, plus `ST_IMAP_TEAM` and `ST_IMAP_ADDRESS` |
| Customer self-registration | `ST_SELF_REGISTRATION` overrides the **Customers can create their own account** setting for sign-up through the portal and API. Inbound email still follows the stored setting |

Branding and the other support settings have **no** environment override. They are managed only in the admin UI. The same holds for the two-step verification policy, the privacy and retention settings, automation rules and team assignment strategies.

### 3.3 Variable reference

#### Core

The core variables are documented, with defaults, in [configuration.md](configuration.md). That table is generated from the code. In short:

| Variable | Default | Notes |
|---|---|---|
| `ST_SECRET_KEY` | — | **Required.** Base64 or raw, at least 32 bytes. In `ST_DEV` mode it is generated into `ST_DATA_DIR/secret.key` |
| `ST_SECRET_KEY_PREVIOUS` | — | The previous secret, still accepted for decryption during a rotation |
| `ST_PUBLIC_URL` | `http://localhost:8080` | The public base URL. Links, OIDC redirect URIs and the Origin/WebSocket check depend on it |
| `ST_PORTAL_URL` | `ST_PUBLIC_URL` | Base URL of the customer portal |
| `ST_AGENT_URL` | `ST_PUBLIC_URL/agent` | Base URL of the agent panel |
| `ST_LISTEN_ADDR` | `:8080` | HTTP listen address |
| `ST_DATA_DIR` | `/data` | SQLite database, local files, backups and the instance lock |
| `ST_DATABASE_URL` | empty (SQLite) | `sqlite:///path` or `postgres://…` |
| `ST_DB_POOL` | `5` | PostgreSQL pool size |
| `ST_DB_READERS` | `2` | SQLite reader pool size |
| `ST_MIGRATE_ON_BOOT` | `true` | Apply pending migrations when `serve` starts |
| `ST_BLOB_URL` | empty (local) | `file:///path` or `s3://bucket/prefix?endpoint=…&region=…[&path_style=true]` |
| `ST_TRUSTED_PROXIES` | — | Comma-separated CIDRs whose `X-Forwarded-For` and `X-Forwarded-Proto` headers are trusted |
| `ST_MAX_BODY_KB` | `1024` | Maximum JSON request body |
| `ST_MAX_UPLOAD_MB` | `25` | Maximum upload size (attachments, branding, KB images) |
| `ST_QUEUE_CAPACITY` | `1024` | Bound of each in-process queue |
| `ST_WS_MAX_CONNS` | `1000` | Global WebSocket connection cap |
| `ST_WS_MAX_CONNS_PER_USER` | `5` | WebSocket connections per user |
| `ST_METRICS_TOKEN` | — | Enables `/metrics` behind this bearer token |
| `ST_AUDIT_RETENTION_DAYS` | `365` | How long audit-log rows are kept |
| `ST_LOG_FORMAT` | `json` | `json` or `text` |
| `ST_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `ST_SHUTDOWN_TIMEOUT` | `20s` | Graceful shutdown budget |
| `ST_DEV` | `false` | Development mode. Never use it in production |
| `GOMEMLIMIT` | 75 % of the container limit | Go soft memory limit. Derived automatically when unset |
| `GOGC` | Go default | Garbage-collector target |

#### Authentication and sessions

| Variable | Default | Notes |
|---|---|---|
| `ST_AUTH_STAFF_LOCAL` | `false` | Allow staff to sign in with a password. Break-glass admins can always use one |
| `ST_BOOTSTRAP_ADMIN_EMAIL`, `ST_BOOTSTRAP_ADMIN_PASSWORD` | — | Set both or neither. On an **empty** database they create a break-glass admin named "Administrator" and disable the setup wizard. Remove the password from the environment afterwards |
| `ST_SELF_REGISTRATION` | (stored setting) | `true`/`false` overrides customer self-registration |
| `ST_SESSION_IDLE_STAFF` | `12h` | Staff sessions end after this much inactivity |
| `ST_SESSION_ABSOLUTE_STAFF` | `168h` | Maximum staff session lifetime |
| `ST_SESSION_IDLE_CUSTOMER` | `720h` | Customer idle timeout |
| `ST_SESSION_ABSOLUTE_CUSTOMER` | `2160h` | Maximum customer session lifetime |
| `ST_INSECURE_COOKIES` | `false` | Don't mark cookies `Secure`. For plain-HTTP testing only; refused when `ST_PUBLIC_URL` is `https` |

Durations use Go syntax, e.g. `30m`, `12h` or `168h`.

#### Single sign-on

`<A>` is `STAFF` or `CUSTOMER`. An audience is enabled when its `ISSUER` is set. See [Single sign-on](#5-single-sign-on-sso).

| Variable | Default | Notes |
|---|---|---|
| `ST_OIDC_<A>_ISSUER` | — | Issuer URL. Enables SSO for that audience |
| `ST_OIDC_<A>_CLIENT_ID` | — | Required when the issuer is set |
| `ST_OIDC_<A>_CLIENT_SECRET` | — | Client secret (confidential client) |
| `ST_OIDC_<A>_LABEL` | `Single sign-on` | Text of the SSO button |
| `ST_OIDC_<A>_SCOPES` | `openid,email,profile` | Requested scopes. `openid` is always added |
| `ST_OIDC_STAFF_ROLE_CLAIM` | `groups` | Claim that carries the groups |
| `ST_OIDC_STAFF_ROLE_MAP` | — | **Required for staff SSO.** `group=role,…` with roles `admin`, `supervisor`, `agent` |
| `ST_OIDC_STAFF_TIER_MAP` | — | `group=tier,…` with tiers `1`–`3`. Users with no mapped tier get tier 1 |
| `ST_OIDC_STAFF_TEAM_CLAIM` | same as `ROLE_CLAIM` | Claim used for team mapping |
| `ST_OIDC_STAFF_TEAM_MAP` | — | `group=team,…`, where the team is a team id or a team name (case-insensitive). An unknown team stops the server from starting |
| `ST_OIDC_LINK_BY_EMAIL` | `false` | Link an SSO login to an existing account with the same, **verified** email address |

#### Email

| Variable | Default | Notes |
|---|---|---|
| `ST_SMTP_HOST` | — | SMTP server. Setting it locks the field in the UI |
| `ST_SMTP_PORT` | `587` | |
| `ST_SMTP_SECURITY` | `starttls` | `none`, `starttls` or `tls` (implicit TLS, usually port 465) |
| `ST_SMTP_USERNAME`, `ST_SMTP_PASSWORD` | — | Credentials |
| `ST_SMTP_AUTH` | `plain` | `plain`, `login`, `cram-md5` or `none` |
| `ST_SMTP_HELO` | — | HELO/EHLO name |
| `ST_SMTP_TIMEOUT_SECONDS` | `10` | |
| `ST_SMTP_ALLOW_INSECURE_TLS` | `false` | Accept invalid certificates. Avoid it |
| `ST_SMTP_FROM_CUSTOMER` | — | Sender for customer mail, e.g. `Acme Support <support@acme.com>` |
| `ST_SMTP_FROM_STAFF` | — | Sender for staff mail, e.g. `Acme Helpdesk <noreply@acme.com>` |
| `ST_NOTIFY_COALESCE_SECONDS` | `120` | Groups several "customer replied" emails to the same agent about the same ticket into one, sent after this delay. `0` turns it off |
| `ST_IMAP_HOST` | — | Creates and locks one "Environment mailbox" |
| `ST_IMAP_PORT` | `993` | |
| `ST_IMAP_SECURITY` | `tls` | `none`, `starttls` or `tls` |
| `ST_IMAP_USERNAME`, `ST_IMAP_PASSWORD` | — | |
| `ST_IMAP_FOLDER` | `INBOX` | Folder to read |
| `ST_IMAP_TEAM` | — | Team id or name for new tickets. Empty means triage |
| `ST_IMAP_ADDRESS` | the username | The mailbox's public address |
| `ST_INBOUND_RATE_PER_HOUR` | `20` | Maximum inbound messages per sender per hour. More are quarantined. `0` or less turns it off |
| `ST_INBOUND_TRUSTED_AUTHSERV_IDS` | — | Comma-separated `authserv-id`s whose `Authentication-Results` headers are trusted for staff email replies |
| `ST_MAX_ATTACHMENT_MB` | `ST_MAX_UPLOAD_MB` | Maximum attachment size from email. A whole inbound message may be up to ten times this |

#### Webhooks

| Variable | Default | Notes |
|---|---|---|
| `ST_WEBHOOK_ALLOW_PRIVATE` | `false` | Allow webhook targets on private, loopback and link-local addresses |
| `ST_WEBHOOK_ALLOW_INSECURE` | `false` | Allow `http://` webhook targets |

#### Settings stored in the database

These settings have no environment variable. They are edited in the admin UI or by each user, and through the API. This table is the canonical list; defaults, ranges and what each value does are in the linked guide section.

| Setting | Stored as | Where to change it | API | Details |
|---|---|---|---|---|
| Customer portal switch | settings key `support`, field `portal_enabled` (default `true`) | **Administration → System → Support settings**, *Customer portal enabled* (admin) | `GET`/`PATCH /api/v1/admin/settings` | [§7.9](#switching-the-customer-portal-off) |
| Two-step verification policy | settings key `security`, field `mfa_required` (`none`, `staff`, `all`) | **Administration → System → Support settings**, card **Security**, *Require two-step verification* (admin) | `GET`/`PATCH /api/v1/admin/security` | [mfa.md](features/mfa.md#require-it-policy), [§7.17](#717-two-step-verification-system--support-settings-admin) |
| Retention policy | settings key `retention`: `closed_tickets_months`, `closed_tickets_action`, `inbound_email_days`, `audit_log_months`, `export_days` | **Administration → People → Privacy**, tab **Retention and settings** (admin) | `GET`/`PUT /api/v1/admin/privacy/settings` | [gdpr.md](features/gdpr.md#retention-policies), [§7.18](#718-privacy-and-gdpr-people--privacy-admin) |
| Deletion requests and consent | settings key `privacy`: `erasure_auto_approve_days`, `erasure_auto_approve_mode`, `policy_version` | Same tab, section **Deletion requests** (admin) | same | [gdpr.md](features/gdpr.md#automatic-approval) |
| Automatic assignment | per team, `assignment_strategy` (`manual`, `round_robin`, `least_busy`) | **Administration → People → Teams**, edit a team, *Automatic assignment* (supervisor+) | `PATCH /api/v1/admin/teams/{id}` | [automation.md](features/automation.md#1-automatic-assignment) |
| Automation rules, shared macros | their own tables | **Administration → Support setup → Automation rules** / **Shared macros** (supervisor+) | `/api/v1/admin/automation/rules`, `/api/v1/admin/macros` | [automation.md](features/automation.md#2-automation-rules) |
| Away | per agent (`users.away_since`) | User menu (top right) → **Away** | `GET`/`PATCH /api/v1/me/availability` | [automation.md](features/automation.md#away) |
| Notification preferences | per user, `notification_prefs` | **Profile → Notifications** card | `PATCH /api/v1/me` | [notifications.md](features/notifications.md#preferences) |

The [Branding](#78-branding-system--branding-admin) privacy policy URL (`legal.privacy_url`) also switches on the consent checkbox at registration (see [Consent at registration](features/gdpr.md#consent-at-registration)).

#### Compose-only variables

`deploy/compose.yml` and `deploy/compose.postgres.yml` read these from `deploy/.env`. The app itself doesn't use them.

| Variable | Default | Notes |
|---|---|---|
| `ST_IMAGE` | `ghcr.io/radiobros/simpletickets` | Image repository |
| `ST_VERSION` | `latest` | Image tag; pin a release, e.g. `1.2.3` |
| `ST_BIND` / `ST_PORT` | `127.0.0.1` / `8080` | Host address and port to publish |
| `ST_ENV_FILE` | `.env` | Environment file, relative to `deploy/` |
| `ST_PG_USER`, `ST_PG_PASSWORD`, `ST_PG_DB` | `simpletickets`, — (required), `simpletickets` | PostgreSQL overlay credentials |

#### Development and tests only

| Variable | Used by |
|---|---|
| `ST_TEST_POSTGRES_URL` | Go tests: also run every store test on PostgreSQL |
| `ST_TEST_S3_URL` | Go tests: S3 storage tests against MinIO |
| `ST_TEST_IMAP_URL` | Go tests: IMAP integration test against Dovecot |
| `ST_DEV_BACKEND` | Vite dev server: backend address (default `http://localhost:8080`) |

---

## 4. First-run setup

When the server starts and **no active admin exists** (and `ST_BOOTSTRAP_ADMIN_EMAIL` is not set), it logs a one-time setup token at WARN level:

```
setup token: 6vS1…Hc — open https://help.example.com/setup to create the first admin
```

Find it with:

```sh
docker compose -f deploy/compose.yml logs app | grep "setup token"
kubectl -n simpletickets logs deploy/simpletickets | grep "setup token"
```

Until setup is complete, every page in the browser redirects to `/setup`. The wizard has six steps:

| Step | What you do |
|---|---|
| 1. Setup token | Paste the token from the log |
| 2. Administrator | Name, email, password (at least 10 characters, common passwords refused), confirmation |
| 3. Branding | Product name, company name, primary colour, optional logo. **Create the help desk** creates the admin and signs you in |
| 4. Email | SMTP host, port, security, username, password, sender address and name. **Send test email** checks it. **Save and continue** or **Skip for now** |
| 5. Single sign-on | Shows the URLs to register at your identity provider. SSO itself is configured with environment variables (see §5) |
| 6. Done | **Open the agent panel** |

The redirect URIs shown in step 5:
- `<ST_PUBLIC_URL>/api/v1/auth/oidc/staff/callback`
- `<ST_PUBLIC_URL>/api/v1/auth/oidc/customer/callback`
- `<ST_PUBLIC_URL>/api/v1/auth/oidc/staff/backchannel-logout`

Security notes:
- **Protection:** the token protects a freshly exposed instance from being claimed by someone else.
- **Storage:** only a hash of the token is kept, in memory. A new token is printed on **every** restart until an admin exists.
- **After setup:** once an admin exists, `/setup` and its API (`GET /api/v1/setup/status`, `POST /api/v1/setup/complete`) answer 404.
- **Break-glass:** the admin created by the wizard can always sign in with a password, even when staff SSO is enforced.
- **Retries:** setup attempts are rate-limited per IP.

**Headless alternatives** (no wizard):

```sh
# 1) On first boot, through the environment (empty database only):
ST_BOOTSTRAP_ADMIN_EMAIL=admin@example.com
ST_BOOTSTRAP_ADMIN_PASSWORD=<a long password>     # remove it after the first start

# 2) With the CLI; prints a generated password unless you pass one on stdin:
docker compose -f deploy/compose.yml exec app /simpletickets create-admin --email admin@example.com --name "Ops Admin"
printf '%s\n' "$PASS" | docker compose -f deploy/compose.yml exec -T app /simpletickets create-admin --email admin@example.com --password-stdin
```

**After setup:**
1. Configure email under **Admin → Email** if you skipped it. See [Email](#6-email).
2. Configure SSO, or set `ST_AUTH_STAFF_LOCAL=true` if your staff will use passwords.
3. Create teams, categories and SLA policies. See the [Admin guide](#7-admin-guide).
4. Invite staff (**Admin → Staff**) and customers (**Customers**), or let customers register.
5. Optionally, set up two-step verification on your own account and decide whether to require it ([§7.17](#717-two-step-verification-system--support-settings-admin)), set a retention policy ([§7.18](#718-privacy-and-gdpr-people--privacy-admin)), and turn on automatic assignment for your teams ([§7.16](#716-automation-and-assignment-support-setup-supervisor)).

---

## 5. Single sign-on (SSO)

### 5.1 How SSO works in SimpleTickets

- **Two audiences, two clients.** Staff and customers each have their own OIDC client: `ST_OIDC_STAFF_*` and `ST_OIDC_CUSTOMER_*`.
  - The callback URL names the audience. That URL, and therefore the client the user signed in through, decides whether the account is staff or customer.
  - Nothing inside the identity token can turn a customer into staff.
- **Standard OIDC.** Discovery, authorization code flow with **PKCE (S256)**, `state` and `nonce`. Any compliant provider works.
  - Authentik is the reference.
  - Keycloak, Zitadel, Microsoft Entra ID and Google (for customers) all use the same settings.
- **Claims used:** `email`, `email_verified`, `name` (falling back to `preferred_username`, then the email), `sid`, and the group claim. If the ID token lacks a claim, the userinfo endpoint is asked for it.
- **Matching accounts.** A returning user is matched by issuer and subject.
  - If a first-time SSO user has the same email as an existing account, the login is refused by default.
  - With `ST_OIDC_LINK_BY_EMAIL=true`, the two are linked, but only if the provider says the email is verified, the kind matches, and the account is not a break-glass admin. Linking makes the account SSO-only: in the same step its local password is removed and its password sessions are signed out, and from then on it signs in only through the provider; see [Two-step verification and SSO](#57-two-step-verification-and-sso).
- **Customers** who sign in through SSO are always `requester`. If their verified email domain matches an organization, they join it.
- **Provider outages** never block startup or `/readyz`.
  - Discovery is retried in the background, backing off from 2 s up to 5 minutes.
  - The status appears under **Admin → System** as "Staff sign-on" and "Customer sign-on".

Login buttons:
- The agent login page shows a button labelled `ST_OIDC_STAFF_LABEL` (default "Single sign-on").
- When staff password login is off, an **Administrator sign in** link reveals the password form for break-glass admins.
- The portal shows its SSO button, configured with `ST_OIDC_CUSTOMER_LABEL`, next to the password form.

### 5.2 Authentik step by step

You need an Authentik instance and the public URL of your help desk. Below, the help desk is `https://help.example.com` and Authentik is `https://auth.example.com`.

#### Option A: import the blueprint

`deploy/authentik/simpletickets.blueprint.yaml` (tested with Authentik 2026.8.4) creates:
- the groups `st-admins`, `st-supervisors`, `st-agents`, `st-l2` and `st-l3`;
- two confidential OAuth2/OpenID providers, signed with Authentik's self-signed certificate, with the scopes `openid`, `email` and `profile`;
- strict redirect URIs, and back-channel logout pointing to SimpleTickets;
- the applications `simpletickets-staff` and `simpletickets-customer`. Only members of `st-admins`, `st-supervisors` or `st-agents` may use the staff application; any user may use the customer application.

The blueprint reads everything environment-specific from the **Authentik** server's and worker's environment:

| Authentik environment variable | Value |
|---|---|
| `ST_PUBLIC_URL` | Your help desk URL, e.g. `https://help.example.com` |
| `ST_BACKCHANNEL_BASE_URL` | Optional. The base URL the Authentik **worker** sends back-channel logout requests to, when it cannot reach `ST_PUBLIC_URL` itself, e.g. `http://app:8080` when both run in one compose network. Empty or unset means `ST_PUBLIC_URL`. The development stack sets `http://app:8080` in `deploy/.env.dev` |
| `ST_OIDC_STAFF_CLIENT_ID` / `ST_OIDC_STAFF_CLIENT_SECRET` | Random values of your choice. The client id defaults to `simpletickets-staff` |
| `ST_OIDC_CUSTOMER_CLIENT_ID` / `ST_OIDC_CUSTOMER_CLIENT_SECRET` | The same for the customer client. The client id defaults to `simpletickets-customer` |

1. Set those variables on the Authentik **server and worker** containers, and give SimpleTickets the same client ids and secrets.
2. Mount the file into `/blueprints/custom/` of both containers; Authentik discovers and applies it automatically. Alternatively, upload it under **Customization → Blueprints**. The development stack `deploy/compose.dev.yml` mounts it for you.
3. Add your staff to the groups.
4. Continue at [Configure SimpleTickets](#configure-simpletickets).

`deploy/authentik/simpletickets-dev-users.blueprint.yaml` seeds demo users for the development stack only. Never import it in production.

#### Option B: create it by hand

1. **Create the staff provider.** Go to **Applications → Providers → Create → OAuth2/OpenID Provider**.
   - Name: `SimpleTickets staff`.
   - Authorization flow: an authorization flow of your choice, e.g. explicit or implicit consent.
   - Client type: **Confidential**.
   - Redirect URIs (strict): `https://help.example.com/api/v1/auth/oidc/staff/callback`.
   - Signing key: choose a certificate, so tokens are signed with RS256 and can be verified through the JWKS.
   - Scopes: keep `openid`, `email` and `profile`. Authentik's default `profile` scope mapping includes a `groups` claim with the user's group names.
   - Save, then copy the Client ID and Client Secret.
2. **Create the staff application.** Go to **Applications → Applications → Create**. Name it `SimpleTickets staff`, give it the slug `simpletickets-staff`, and select the provider from step 1.
3. **Create the customer provider and application** (optional) the same way:
   - name `SimpleTickets customers`;
   - slug `simpletickets-customer`;
   - redirect URI `https://help.example.com/api/v1/auth/oidc/customer/callback`.
4. **Create groups** under **Directory → Groups**, for example `st-admins`, `st-supervisors` and `st-agents` for roles, and `st-l2` and `st-l3` for tiers. Add your staff to them.
5. Optionally, restrict access to each application with policy bindings, for example to members of the `st-*` groups.

#### Configure SimpleTickets

The issuer of an Authentik application is `https://auth.example.com/application/o/<slug>/`.

```env
# Staff
ST_OIDC_STAFF_ISSUER=https://auth.example.com/application/o/simpletickets-staff/
ST_OIDC_STAFF_CLIENT_ID=<client id>
ST_OIDC_STAFF_CLIENT_SECRET=<client secret>
ST_OIDC_STAFF_LABEL=Sign in with Acme SSO
ST_OIDC_STAFF_ROLE_MAP=st-admins=admin,st-supervisors=supervisor,st-agents=agent
ST_OIDC_STAFF_TIER_MAP=st-l2=2,st-l3=3

# Customers (optional)
ST_OIDC_CUSTOMER_ISSUER=https://auth.example.com/application/o/simpletickets-customer/
ST_OIDC_CUSTOMER_CLIENT_ID=<client id>
ST_OIDC_CUSTOMER_CLIENT_SECRET=<client secret>
```

Recreate the app so it reads the new values: `docker compose -f deploy/compose.yml up -d`. (`docker compose restart` does **not** re-read the env file.) Then open `/agent/login` and click the SSO button. `GET /api/v1/admin/system` reports `"discovered": true` under `oidc.staff` once discovery succeeded.

**The issuer URL must work from the browser and from the app container, and be the same URL for both.** The server fetches the provider's discovery document and keys from `ST_OIDC_STAFF_ISSUER`, and the OIDC library compares the issuer in the tokens with it character for character. If the container can't reach that URL, the SSO button answers `503` and the log shows `oidc discovery failed`. With public DNS names and TLS this just works. For a local test where Authentik runs in Docker on the same machine, `authentik.localhost` resolves to 127.0.0.1 in the browser but to the app container itself inside it, so put the app on Authentik's Docker network, where the Authentik server has that name as an alias:

```yaml
# sso-local.override.yml: docker compose -f deploy/compose.yml -f sso-local.override.yml up -d
services:
  app:
    networks: [default, idp]
networks:
  idp:
    name: <authentik's compose project>_default   # docker network ls
    external: true
```

and give the Authentik server container the network alias `authentik.localhost` (the development stack, `deploy/compose.dev.yml`, does exactly this).

Redirect URIs to register at the provider:

| Purpose | URI |
|---|---|
| Staff login callback | `https://help.example.com/api/v1/auth/oidc/staff/callback` |
| Customer login callback | `https://help.example.com/api/v1/auth/oidc/customer/callback` |
| Staff post-logout redirect | `https://help.example.com/agent/login?logged_out=1` (`ST_AGENT_URL` + `/login?logged_out=1`) |
| Customer post-logout redirect | `https://help.example.com/login?logged_out=1` (`ST_PORTAL_URL` + `/login?logged_out=1`) |
| Back-channel logout (staff) | `https://help.example.com/api/v1/auth/oidc/staff/backchannel-logout` |
| Back-channel logout (customers) | `https://help.example.com/api/v1/auth/oidc/customer/backchannel-logout` |

### 5.3 Group mapping: roles, tiers and teams

Staff role, tier and (optionally) teams are synced from the group claim **on every login**, in the same transaction that creates the session.

- **Role:** `ST_OIDC_STAFF_ROLE_MAP=group=role,…`.
  - If the user is in several mapped groups, the **highest** role wins (admin > supervisor > agent).
  - If none of the user's groups is mapped, the login is refused and an existing account is **disabled**.
  - Remove someone from all mapped groups and they lose access at their next login. Their sessions and API tokens stop working because the account is disabled.
- **Tier:** `ST_OIDC_STAFF_TIER_MAP=group=1..3,…`. The highest mapped tier wins, and the default is 1. Admins and supervisors always act as tier 3.
- **Teams:** `ST_OIDC_STAFF_TEAM_MAP=group=team,…` (optional). The team is a team id or a team name (case-insensitive).
  - Memberships that came from the provider are added and removed to match the groups.
  - Memberships added by hand in **Admin → Teams** are never touched.
  - In the admin UI, provider-managed memberships show "From identity provider" and can't be edited there.
- **Claims:** the group claim is `groups` by default. Change it with `ST_OIDC_STAFF_ROLE_CLAIM`, and set `ST_OIDC_STAFF_TEAM_CLAIM` if teams come from another claim.
- **Locked fields:** role and tier of SSO-managed staff are read-only in **Admin → Staff**, because the provider owns them.

Map syntax: comma-separated `key=value` pairs, split on the first `=`, with whitespace trimmed. A wrong role, a tier outside 1–3, or an unknown team stops the server from starting, with a clear message.

### 5.4 Break-glass administrators

A **break-glass** admin is a local admin account that can always sign in with a password, even when staff password login is off (`ST_AUTH_STAFF_LOCAL=false`, the default). Such an account means a provider outage or a bad mapping can never lock everyone out.

Break-glass admins are created by:
- the setup wizard;
- `ST_BOOTSTRAP_ADMIN_EMAIL` + `ST_BOOTSTRAP_ADMIN_PASSWORD` on an empty database;
- `simpletickets create-admin --email …` (`--name`, `--password-stdin`);
- **Admin → Staff**, with the "Break-glass account" switch.

Every break-glass login is logged at WARN level and written to the audit log as `auth.break_glass_login`. A break-glass admin keeps its password sign-in even when it also signs in through SSO. If such a user later signs in through SSO and is mapped to a role other than admin, the break-glass flag is removed; the account is then SSO-only, so its local password is removed and its password sessions are signed out.

[Two-step verification](#717-two-step-verification-system--support-settings-admin) applies to every break-glass password sign-in, however the account was created (including an admin that came from SSO and was later flagged in **Admin → Staff**): the *All staff accounts* policy covers it, and the `auth.break_glass_login` entry is written after the second step. Keep its recovery codes with the break-glass password. Its SSO sign-ins get their second factor from the provider; see [5.7](#57-two-step-verification-and-sso).

To recover a lost password:

```sh
docker compose -f deploy/compose.yml exec app /simpletickets reset-password --email admin@example.com
# prints a generated password and signs the user out everywhere
```

To recover a lost second factor, run `reset-mfa` the same way (see [Two-step verification](#717-two-step-verification-system--support-settings-admin)):

```sh
docker compose -f deploy/compose.yml exec app /simpletickets reset-mfa --email admin@example.com
```

### 5.5 Logout and back-channel logout

- **Sign out** (`POST /api/v1/auth/logout`) deletes the session and closes its WebSocket connections.
  - For an SSO session, the response redirects the browser to the provider's `end_session_endpoint` (RP-initiated logout), with `id_token_hint`, `client_id` and `post_logout_redirect_uri`.
  - The post-logout redirect is the login page with `?logged_out=1`. Register it at the provider if the provider requires it.
- **Back-channel logout:** the provider can sign users out of SimpleTickets by POSTing a logout token (form field `logout_token`) to `/api/v1/auth/oidc/{staff|customer}/backchannel-logout`.
  - The token's signature and issue time (±5 minutes) are checked, and replayed tokens are ignored.
  - Matching sessions (by subject and/or `sid`) are deleted and their WebSockets closed.
  - Configure this URL in your provider if it supports OIDC back-channel logout.
  - The provider calls it **server to server**, so the URL must be reachable from the provider's own host or container, not just from browsers. Authentik sends it from its worker when the user's Authentik session ends (logout in Authentik, or an admin deleting the session); Keycloak sends it when the user signs out of Keycloak or an admin signs the user out.
  - Verified against Authentik 2026.8.4 and Keycloak 26.8.1: after the logout at the provider, the next `/api/v1/me` answers 401 within a second, and the audit log shows `auth.backchannel_logout`.

### 5.6 Generic OIDC and Keycloak

Any OpenID Connect provider with discovery works. Register a **confidential** client per audience, using the same redirect URIs as above.

**Keycloak:**
1. In your realm, create a client `simpletickets-staff`:
   - Client authentication **On**, Standard flow **On**.
   - Valid redirect URIs: `https://help.example.com/api/v1/auth/oidc/staff/callback`.
   - Valid post logout redirect URIs: `https://help.example.com/agent/login?logged_out=1`.
   - Backchannel logout URL: `https://help.example.com/api/v1/auth/oidc/staff/backchannel-logout`.
2. Add a **Group Membership** mapper to the client's dedicated scope:
   - Token claim name `groups`.
   - **Full group path off**, so the claim holds `st-admins` rather than `/st-admins`.
   - Add to ID token and userinfo.
3. Configure SimpleTickets:

   ```env
   ST_OIDC_STAFF_ISSUER=https://keycloak.example.com/realms/acme
   ST_OIDC_STAFF_CLIENT_ID=simpletickets-staff
   ST_OIDC_STAFF_CLIENT_SECRET=<secret>
   ST_OIDC_STAFF_ROLE_MAP=st-admins=admin,st-supervisors=supervisor,st-agents=agent
   ```

4. For the customer audience, create a second confidential client `simpletickets-customer` the same way (with `/api/v1/auth/oidc/customer/…` URIs) and set `ST_OIDC_CUSTOMER_ISSUER`, `ST_OIDC_CUSTOMER_CLIENT_ID` and `ST_OIDC_CUSTOMER_CLIENT_SECRET`. It needs no group mapper.
5. To map Keycloak **roles** instead of groups, add a role mapper that writes them to a claim such as `roles`, then set `ST_OIDC_STAFF_ROLE_CLAIM=roles`.

Keycloak has no per-application access policy like Authentik's group bindings, so any realm user can reach the staff login. SimpleTickets refuses a user with no mapped group (`/agent/login?error=no_role`) and disables an existing account, exactly as described in [5.3](#53-group-mapping-roles-tiers-and-teams).

A ready-made realm for local testing is in `deploy/keycloak/simpletickets-realm.json`. It has the `st-*` groups, both clients with the group mapper, and back-channel logout URLs. Start it with `docker compose -f deploy/compose.yml -f deploy/compose.keycloak.yml up -d --build --wait` (Keycloak 26.8.1 on `http://keycloak.localhost:8180`, app on `http://localhost:8080`). `web/e2e/sso-keycloak.spec.ts` runs the staff scenarios against it.

**Other providers:**
- If your provider doesn't put groups in the ID token or userinfo by default, request an extra scope with `ST_OIDC_STAFF_SCOPES=openid,email,profile,groups`.
- For a public customer login with Google or Microsoft, configure only the customer audience. Customers never need group claims.

### 5.7 Two-step verification and SSO

SimpleTickets' own [two-step verification](#717-two-step-verification-system--support-settings-admin) protects **local** sign-ins. Accounts that belong to your identity provider get their second factor there, so turn it on in the provider.

- **SSO sign-ins never ask for a code.** The OIDC callbacks create the session directly; the second step only runs after a password, a sign-in link, an email verification link or an invitation link.
- **Which accounts count as SSO accounts.** An account is an SSO account when it has an SSO identity for its audience or is *managed by the identity provider*, which happens when:
  - it was created by an SSO sign-in (staff or customer);
  - a staff member or customer with an SSO identity signs in through SSO (every such login marks the account);
  - `ST_OIDC_LINK_BY_EMAIL=true` linked an existing local account to an SSO identity.
- **SSO accounts sign in only through the provider.** This way the provider's own two-step verification, group mapping and blocking always apply. For SSO accounts:
  - Password sign-in fails exactly like a wrong password. The answer, the timing and the count toward the sign-in lockout are the same, and the audit log records the reason `sso_only`.
  - Sign-in links, email verification links and invitation links open no session, and no new sign-in link is sent.
  - **Forgot password** sends the "sign in with SSO" email instead of a reset link, a reset link sent before the account was linked no longer works, and the profile can't set a password (403).
- **Linking signs out the password.** When an account becomes an SSO account (linked by email, or marked by an SSO sign-in), in the same step:
  - its local password is removed;
  - its password-based sessions are signed out (SSO sessions stay);
  - a sign-in in progress at the second step is cancelled;
  - unused reset and sign-in links are deleted.
  
  The audit log records this as `auth.sso_linked`.
- **What that means for two-step verification.** For SSO accounts the *Require two-step verification* policy doesn't apply, the profile shows "Your account signs in through your organization's identity provider, which handles two-step verification.", and the admin's two-step card shows "managed by the identity provider".
- **Break-glass admins are the exception.** A break-glass admin can always sign in with its password, even when it also has an SSO identity. That includes an admin created by SSO and later flagged *Break-glass account* in **Admin → Staff**. Every such password sign-in goes through SimpleTickets' two-step verification and the policy. Break-glass admins can't be linked by email. See [Break-glass administrators](#54-break-glass-administrators).
- **Personal access tokens** are not affected by either kind of second factor.

---

## 6. Email

SimpleTickets runs fine **without** email:
- Notifications are skipped and logged instead.
- Sign-in flows that need email (self-registration, password reset, magic links) are hidden from the login pages.
- Admins see the banner "Email is not configured: notifications are off." This refers to email only: the [notification centre](#719-notification-centre) (the bell) keeps working without SMTP.
- Two-step verification by email code and the data-export ready email are unavailable; authenticator apps work.

Configure SMTP to turn on notifications and those flows.

### 6.1 Outgoing email (SMTP)

Configure it under **Admin → Email → Mail server (SMTP)**, in the setup wizard, or with `ST_SMTP_*` variables.

| UI field | API field | Environment variable | Values |
|---|---|---|---|
| Server name | `smtp.host` | `ST_SMTP_HOST` | host name |
| Port | `smtp.port` | `ST_SMTP_PORT` | default 587 |
| Encryption | `smtp.security` | `ST_SMTP_SECURITY` | `none`, `starttls` (default), `tls` (implicit) |
| Sign-in method | `smtp.auth` | `ST_SMTP_AUTH` | `plain` (default), `login`, `cram-md5`, `none` |
| User name | `smtp.username` | `ST_SMTP_USERNAME` | |
| Password | `smtp.password` (write-only) | `ST_SMTP_PASSWORD` | encrypted at rest; never returned by the API |
| HELO name | `smtp.helo` | `ST_SMTP_HELO` | |
| Timeout (seconds) | `smtp.timeout_seconds` | `ST_SMTP_TIMEOUT_SECONDS` | 1–120, default 10 |
| Accept invalid certificates | `smtp.allow_insecure_tls` | `ST_SMTP_ALLOW_INSECURE_TLS` | default off |
| Emails to customers: sender name and address | `senders.customer` | `ST_SMTP_FROM_CUSTOMER` | `Name <address>` |
| Emails to staff: sender name and address | `senders.staff` | `ST_SMTP_FROM_STAFF` | `Name <address>` |

**Send test email** sends a message to the address you enter.
- It uses the values **currently in the form**, even if they aren't saved yet, so you can test before saving.
- Failures are explained in plain words, for example authentication failed, TLS mismatch or connection refused.

The status banner at the top of the page says "Email is working: notifications are sent." or "Email is not working: …", followed by the last error.

How sending works:
- Messages are sent through the in-process queue.
- If the first attempt fails, the message is retried from the database with exponential backoff.
- A permanent SMTP rejection (5xx) goes straight to **Failed jobs**, where an admin can retry it.

### 6.2 Sender identities and mailboxes

- **Two sender identities:**
  - **Emails to customers**, used for `customer.*` mail and sign-in mail to customers.
  - **Emails to staff**, used for `staff.*` mail and sign-in mail to staff.
  - Each has a sender name, a sender address and an optional reply-to address.
- **Plus addressing** ("Use plus addressing for replies"): outgoing customer mail uses a reply address like `support+t-<token>@example.com`. The signed token identifies the ticket, so replies thread reliably even if the subject is changed. Turn it on only if your mailbox delivers plus-addressed mail.
- **Team mailboxes:** each team can have a notification address (**Admin → Teams → Team mailbox**) and a **Notifications** mode:
  - *Team members* (`agents`): only the individual agents are notified.
  - *Team mailbox* (`mailbox`): only the team address is notified.
  - *Members and mailbox* (`both`): both are notified.

  The team address receives the new-ticket and escalation notifications for that team.
- **Triage mailbox:** addresses that receive new-ticket notifications for tickets without a team.

### 6.3 Notifications, templates and languages

| Template key | Sent when | To |
|---|---|---|
| `customer.ticket_received` | A ticket is created (any channel) | Requester |
| `customer.reply` | Staff post a public reply | Requester and watchers |
| `customer.resolved` | The ticket is resolved (includes the satisfaction-rating link) | Requester |
| `staff.ticket_new` | A ticket is created | Team members with tier ≥ the ticket's tier and/or the team mailbox; supervisors or the triage mailbox for triage tickets |
| `staff.customer_replied` | The customer replies | Assignee, or the team if nobody is assigned. Grouped per `ST_NOTIFY_COALESCE_SECONDS` |
| `staff.assigned` | A ticket is assigned to you by someone else | New assignee |
| `staff.escalated` | A ticket is escalated | Target team members with tier ≥ the new tier and/or the team mailbox |
| `staff.mentioned` | You are @mentioned in an internal note | Mentioned staff |
| `staff.sla` | An SLA warning or breach | Assignee or team, plus supervisors |
| `staff.children_resolved` | The last open child ticket is resolved | Parent's assignee |
| `staff.webhook_disabled` | A webhook was disabled automatically | Admins |
| `auth.verify_email`, `auth.reset_password`, `auth.magic_link`, `auth.invite`, `auth.already_registered` | Account flows | The user |
| `auth.mfa_code` | A sign-in needs a two-step verification code by email | The user |
| `privacy.export_ready` | A data export is ready to download | Whoever requested the export |

Who gets what:
- **Your own actions:** you are never notified about something you did yourself.
- **Switching templates off:** in **Admin → Email → Notifications**, admins can turn off any template. `auth.*` and `privacy.*` templates are always sent.
- **Personal preferences:** every user chooses per notification type whether to get it by email, in the bell and as a browser pop-up, in the **Notifications** card of their profile. **Reply to my request** emails to customers can't be turned off.
- **Previews:** each template has a **Preview** button that renders it with your branding and sample data, in any language.
- **Notification centre:** the ten ticket templates above (`customer.*` and `staff.*` except `staff.webhook_disabled`) also appear in the bell in the top bar, and optionally as browser notifications. Switching a template off here affects email only. See [Notification centre](#719-notification-centre).

**Format.** Every email is sent as plain text and HTML, built from Markdown. It uses your branding: email logo (PNG or JPEG), header colour, footer and legal links. Public staff replies end with the agent's personal **email signature** (set in the agent's profile).

**Language.** Templates exist in English, Italian, German, French and Spanish. Each recipient gets their own language setting, otherwise the instance default language. Team mailboxes always get the default language.

**Subject and threading headers.** The subject includes the ticket reference, e.g. `[WEB-12]` (see [Projects](features/projects.md)). `Message-ID` and `References` headers are set so mail clients group the conversation and replies can be matched to the ticket.

### 6.4 Inbound email

Inbound email turns incoming messages into tickets, or into replies on existing tickets. Configure it under **Admin → Inbound email**. Each mailbox has:
- a type: **IMAP mailbox** or **Webhook (MIME)**;
- the public email address;
- a team for new tickets (empty means triage);
- an Active switch.

**IMAP mailboxes**
- **Settings:** IMAP server, port, security (`tls`, `starttls`, `none`), user name, password, folder (default `INBOX`), and what to do after import.
  - After import, the message is either moved to a folder (default `SimpleTickets/Processed`, created if missing) or marked as read.
  - Mail is never deleted.
- **Authentication:** a password, or **OAuth 2 (XOAUTH2)** for Gmail and Microsoft 365. XOAUTH2 uses an app registration with a refresh token, which is stored encrypted.
  - If XOAUTH2 isn't an option, forward the mailbox to a provider webhook instead (below).
- **Connection:** uses IMAP IDLE, so new mail arrives within seconds without polling.
  - The connection is refreshed every 15 minutes.
  - Servers without IDLE are polled every 60 seconds.
  - Lost connections are retried with backoff, from 1 second up to 5 minutes.
- **Test connection** checks the saved settings.
- **One mailbox from the environment:** `ST_IMAP_HOST` (and the other `ST_IMAP_*` variables) creates one "Environment mailbox" on first start and locks its fields.
  For example, in `deploy/.env`:

<!-- doctest:imap-env -->
```env
ST_IMAP_HOST=imap.example.com
ST_IMAP_PORT=993
ST_IMAP_SECURITY=tls
ST_IMAP_USERNAME=support@example.com
ST_IMAP_PASSWORD=app-password
ST_IMAP_FOLDER=INBOX
ST_IMAP_ADDRESS=support@example.com
```

**Webhook mailboxes (any mail provider that can POST raw MIME)**

Create a mailbox of type **Webhook (MIME)**. The ingest URL is shown **once**. Copy it into your provider:

```
POST https://help.example.com/api/v1/inbound/mime/<mailbox_token>
Content-Type: message/rfc822          (or application/octet-stream)

<the raw RFC 5322 message>
```

- The token in the URL authenticates the provider. Treat the URL as a secret.
- Optionally, set a **signing secret**. The provider must then send:
  - the header `X-SimpleTickets-Inbound-Signature: t=<unix seconds>,v1=<hex HMAC-SHA256(secret, t + "." + body)>`;
  - a `t` within 5 minutes of the server's clock.
- The server answers `202 {"ok": true}` and processes the message in the background.
- Limits:
  - 5 requests per second, with a burst of 50;
  - each message up to ten times `ST_MAX_ATTACHMENT_MB`.
- **Supported providers:** only the raw message is accepted, not provider-specific JSON or form formats.
  - A Cloudflare Email Worker, for example, can POST the raw message directly.
  - For providers that only deliver parsed JSON or form posts, such as Postmark, Mailgun or SES notifications, put a small relay in between that extracts the raw RFC 5322 message and POSTs it to this URL.

**What happens to each message**
- **Recorded once:** every message is recorded by its `Message-ID`, so duplicates are never processed twice.
- **Content:**
  - Quoted history is cut at the localized "##- Reply above this line -##" marker.
  - HTML-only mail is converted to Markdown.
  - Attachments and inline images become ticket attachments.
- **The original:** the original message is attached as `original.eml` if it is 2 MiB or smaller.
- **New senders:**
  - Unknown senders become customers when self-registration is on. They join an organization by email domain.
  - Otherwise their mail is quarantined as `unknown_sender`.
- **Inbound log:** the result of each message is shown under **Admin → Inbound email → Inbound log**: `processed`, `quarantined`, `failed` or `ignored`. Failed and quarantined messages have a **Retry** button.

### 6.5 Threading

A message is attached to an existing ticket if one of these matches, tried in order:

1. `In-Reply-To` or `References` contains one of SimpleTickets' own message ids: `<ticket-<id>@host>` or `<<event id>.<hash>@host>`.
2. A recipient address carries a valid plus-address token, `local+t-<token>@domain`. The headers checked are `To`, `Cc`, `Delivered-To`, `X-Original-To` and `Envelope-To`.
3. The subject contains the ticket reference `[KEY-123]` (any letter case) or, as before projects, `[#<ticket number>]`.

In every case, a customer sender must be the requester or a watcher of that ticket; otherwise a new ticket is created. Staff senders are handled as described below.

If the matched ticket is **closed**, or **resolved** with the reopen window passed, a customer reply creates a **follow-up ticket**:
- its subject is `Re: <subject>`;
- it keeps the same team, category and priority;
- its body ends with "Follow-up of #N.".

Otherwise the reply is added to the ticket and the normal status rules apply.

### 6.6 Loop protection, quarantine and staff replies

**Ignored, never creating tickets or auto-replies:**
- `Auto-Submitted` other than `no`;
- `Precedence: bulk`, `list` or `junk`;
- `X-Autoreply`, `X-Autorespond` or `X-Autoresponder`;
- `Return-Path: <>`;
- mail sent from the help desk's own addresses, including their plus variants, or carrying its own `Message-ID` domain;
- mailing-list mail with `List-Id`, unless the mailbox allows list mail.

**Quarantined, visible in the inbound log with a reason:**

| Reason | Cause |
|---|---|
| `rate_limited` | The sender exceeded `ST_INBOUND_RATE_PER_HOUR` (default 20 per hour). An admin retry bypasses the limit |
| `unknown_sender` | Unknown sender, and self-registration is off |
| `disabled_sender` | The sender's account is disabled |
| `staff_new_ticket` | A staff address sent mail that doesn't thread to an existing ticket |
| `staff_spoof` | Mail from a staff address without a passing DKIM or aligned SPF result from a trusted server |

**Staff replying by email.** Staff can answer a ticket notification straight from their mail client:
- The reply must thread to an existing ticket.
- It must carry an `Authentication-Results` header with a DKIM pass or an aligned SPF pass. Only headers from your own mail server count: list its `authserv-id` in `ST_INBOUND_TRUSTED_AUTHSERV_IDS`, or set it per IMAP mailbox.
- If the first line of the body is `#internal` (case-insensitive), the reply becomes an **internal note**. Otherwise it is a public reply.
- Mail that fails these checks is quarantined, so nobody can post replies as your staff by forging the From address.

---

## 7. Admin guide

The admin pages live in the agent panel under **Administration**, grouped in the drawer as *People*, *Support setup*, *Channels* and *System*. A page appears only if your role may open it.
- Pages marked **supervisor+** are available to supervisors and admins.
- All other pages are for admins only.

### 7.1 Staff (People → Staff)

- **List:** shows each staff member's role, tier, teams, last sign-in, source (SSO or local) and a *Break-glass* chip.
- **Create or edit:** role (Agent / Supervisor / Administrator), tier, teams, status, and the *Break-glass account* switch.
  - Enter an **initial password**, or leave it empty and send an **invitation email**.
  - A local password only works when `ST_AUTH_STAFF_LOCAL=true`, or for break-glass accounts.
- **SSO-managed staff:** role and tier are read-only, because your identity provider owns them. See [Group mapping](#53-group-mapping-roles-tiers-and-teams).
- **Disabling** a user signs them out everywhere and makes their API tokens stop working immediately.
- **Edit staff member** dialog, admins only:
  - the **Privacy** card exports the person's data or erases it (see [Privacy and GDPR](#718-privacy-and-gdpr-people--privacy-admin));
  - the two-step verification card shows whether it is on, with **Reset two-step verification** (see [Two-step verification](#717-two-step-verification-system--support-settings-admin)).

### 7.2 Teams (People → Teams, supervisor+)

- **Settings:** name and description.
- **Automatic assignment:** *Manual* (the default), *Round robin* or *Least busy*. See [Automation and assignment](#716-automation-and-assignment-support-setup-supervisor).
- **Notifications:** *Team members*, *Team mailbox* or *Members and mailbox*, plus the team mailbox address. See [Sender identities and mailboxes](#62-sender-identities-and-mailboxes).
- **Business hours:** the team's calendar, or *Always open (24×7)*. This drives business-hours SLAs.
- **Members:** add and remove members. Members synced from the identity provider show *From identity provider* and can't be changed here.

### 7.3 Categories (Support setup → Categories, supervisor+)

- **Fields:** name, default team, default priority, Active.
- **Routing:** a new ticket in a category goes to the category's default team and priority, unless they are set explicitly.
- **Order:** drag rows to change the order customers see in the new-ticket form.

### 7.4 Custom fields (Support setup → Custom fields, admin)

| Setting | Notes |
|---|---|
| Key | Machine name, used in the API (`custom_fields.<key>`) and in list filters (`cf.<key>=<value>`). It can't be changed after creation |
| Label | Shown in the UI |
| Type | Text (`text`), Long text (`textarea`), Number (`number`), Date (`date`), Single choice (`select`), Multiple choice (`multiselect`), Checkbox (`checkbox`) |
| Options | The values for single and multiple choice |
| Required | The field must be filled in |
| Visible to customers | Off: the field never appears in customer API responses, customer WebSocket frames, customer emails or customer-scoped webhooks |
| Editable by customers | Customers can set it when they open a ticket (requires *Visible to customers*) |
| Categories | Limit the field to some categories. Empty means all categories |
| Sort order | Display order |

Fields that are already in use are **archived**, not deleted. *Show archived* lists them again, and *Reactivate* brings one back. Agents can show custom fields as extra columns in the ticket list and filter on them.

### 7.5 SLA policies and business hours (supervisor+)

- **SLA policies:** a grid with one row per priority.
  - Each row has a **first response** and a **resolution** target in minutes (1 to 525 600).
  - Each row also has a **clock**: *Around the clock (24×7)* or *Team's business hours*.
- **Business hours:** each calendar has a name, an IANA **time zone**, **weekly hours** and **holidays**.
  - A day can have several intervals; use *Add hours*, or mark the day *Closed*.
  - The page previews the "next business minute from now", so you can check the calendar.
- How the clock runs is described in [SLA and business hours](#16-sla-and-business-hours).

### 7.6 Canned responses and tags (supervisor+)

- **Canned responses:** each one has a title, a Markdown text, and a team (empty means all teams).
  - Agents insert them from the composer.
  - The variables `{{requester.name}}`, `{{ticket.number}}` and `{{agent.name}}` are filled in when inserted.
  - Agents see the responses for all teams plus those for their own teams.
- **Tags:** a read-only list with usage counts. Agents create tags by typing them on a ticket.

### 7.7 Knowledge base (supervisor+ for categories and publishing)

The knowledge base can be turned on or off with *Knowledge base enabled* under **Support settings**. It is on by default.

- **Categories** (*Support setup → Knowledge base categories*) form a tree. Each category has a parent, a slug, a title per language, a **visibility** and a sort order.
- **Visibility** controls who can read:

  | Visibility | Who can read |
  |---|---|
  | `public` | Anyone, without signing in |
  | `customers` | Signed-in customers and staff |
  | `internal` | Staff only |

  An article can never be more visible than its category. Drafts are staff-only.
- **Articles** (*Knowledge base* in the agent drawer):
  - **Fields:** category, an optional slug (generated from the title when empty), visibility, and one tab per language with a title and Markdown text. Images can be uploaded into articles.
  - **Who can do what:** agents write and save **drafts**. Supervisors and admins can also **Publish**, **Archive** and **Delete**.
  - **Statuses:** `draft`, `published`, `archived`.
- **Languages:** a missing translation falls back to the default language, and the reader sees "This article is not available in your language yet…".
- **Where readers find it:**
  - The public help center is at `/kb`, with search.
  - Customers get article suggestions while they type a new ticket's subject.
  - Readers can answer "Was this article helpful?".
- **Linking from replies:** agents can link articles in replies. A public reply can only link articles the customer can read.
- **Sitemap:** *Publish a sitemap of public articles* serves `/kb/sitemap.xml` for search engines.

### 7.8 Branding (System → Branding, admin)

Changes are previewed live for the login page, the portal and the agent panel before you save. Every setting is also available in the API (`GET`/`PUT /api/v1/admin/branding`).

| Group | Settings (API keys) |
|---|---|
| Identity | Product name (`product_name`; replaces "SimpleTickets" in the UI, page titles and emails), company name (`company_name`), support website (`support_url`) |
| Logos and images | Logo for the light theme (`logo_light`), logo for the dark theme (`logo_dark`), icon (`icon`), browser icon (`favicon`), email logo (`email_logo`, PNG or JPEG only), login background (`login_background`). Upload PNG, JPEG, WebP or SVG up to 1 MB; SVG is sanitised. *Use default* resets a slot |
| Colours | Primary, secondary and accent (`colors`), with a contrast check against white text. Optional different colours for the customer portal (`portal_colors`) and for the agent panel (`agent_colors`) |
| Look | Default theme (`default_theme`: light, dark, system), corners (`radius`: sharp, default, rounded), font (`font`: system, inter, roboto, source-sans, noto-sans; all self-hosted, with no calls to external font services) |
| Login page | Title, subtitle, background colour (`login.title`, `login.subtitle`, `login.background_color`) |
| Customer portal texts | Welcome text on the portal home and an introduction on the new-ticket page, both Markdown (`portal.welcome_md`, `portal.new_ticket_intro_md`) |
| Emails | Header colour and footer in Markdown (`email.header_color`, `email.footer_md`) |
| Legal links | Privacy policy, terms of service and imprint URLs (`legal.privacy_url`, `legal.terms_url`, `legal.imprint_url`), shown in the portal footer and in emails. The "Powered by SimpleTickets" link can be switched off (`powered_by`) |
| Languages | Available languages (`available_locales`) and default language (`default_locale`), from EN, IT, DE, FR and ES |
| Custom CSS | Up to 20 KB (`custom_css`), served from the same origin |

- Uploaded assets are served from `/branding/<hash>.<ext>` and cached indefinitely. Replacing a logo changes its URL, so browsers pick up the new one at once.
- An uploaded favicon is also served at `/favicon.ico`.

### 7.9 Support settings (System → Support settings, admin)

| Setting | Default | Effect |
|---|---|---|
| Support tiers | L1, L2, L3 | Display names of tiers 1–3 |
| Close resolved tickets after (days) | 7 | Auto-close; `0` disables it |
| Customers can reopen for (days) | 7 | Reopen window. After it, a reply creates a follow-up ticket |
| Customer portal enabled | on | Master switch for the customer web app (`support.portal_enabled`). **Off: only admins and staff can sign in.** See [Switching the customer portal off](#switching-the-customer-portal-off). There is no environment variable for it |
| Customers can create their own account | on | Self-registration in the portal and from inbound email. Needs working email. `ST_SELF_REGISTRATION` overrides it for the portal |
| Customers can choose the Urgent priority | off | Otherwise customers can only pick low, normal or high |
| Only supervisors can set Urgent | off | Agents can't set `urgent` |
| Agents can see the triage queue | on | Agents see tickets without a team, up to their tier |
| Knowledge base enabled | on | Shows the help center and the KB menus |
| Publish a sitemap of public articles | off | Serves `/kb/sitemap.xml` |
| Default language | en | Used for new users, emails and fallbacks |

#### Switching the customer portal off

Turn **Customer portal enabled** off to run SimpleTickets as a staff-only tool, for example when customers only ever write by email. Nothing is deleted, and switching it back on restores everything.

| Area | While it is off |
|---|---|
| Web app | Every portal URL (`/`, `/login`, `/register`, `/forgot-password`, `/tickets…`, `/profile…`, `/organization/members`, …) redirects to `/agent/login`. `/` goes to `/agent`. The SPA hides every customer entry point (Sign in, My tickets, New ticket, "open a ticket" in help articles); visitors see **Staff sign in** instead. `GET /api/v1/auth/config` returns `portal_enabled: false` and every `customer.*` sign-in method as `false` |
| Public help center | Not affected. `/kb` follows only its own **Knowledge base enabled** setting. With the portal off and the help center on, `/kb` stays public and has no customer links |
| Customer API | `POST /auth/login` with audience `customer`, `/auth/register`, `/auth/verify-email`, `/auth/magic-link`, `/auth/invite/accept` (for a customer), and `/auth/oidc/customer/start` and `/callback` answer **404** with the problem `portal-disabled`. An invitation stays valid while the portal is off |
| Customer tokens and sessions | Switching the portal off deletes every customer session, closes the open customer sockets and ends the signed-in customers' tabs at their next request. A customer personal access token stops working: `401` on every route, `404 portal-disabled` on `/api/v1/portal/*`, which answers 404 `portal-disabled` to every customer principal. Staff keep getting a plain 404 there |
| WebSocket | A customer connection is accepted and closed at once with close code **4403** |
| Staff | Unaffected: sign-in, the agent panel, the admin API, tokens and sockets. Staff can still create tickets on a customer's behalf (**New ticket** with the customer's email) |
| Email | Customers still receive ticket emails, and their email replies still thread into the ticket through inbound email. Self-service links in those emails (sign in, rate, reset password) lead to the staff sign-in. Password-reset requests for a customer account send nothing |

The setting can be changed through `PATCH /api/v1/admin/settings` (`{"portal_enabled": false}`) and is published in `GET /api/v1/auth/config`.

Below the form, the **Security** card holds *Require two-step verification*. See [Two-step verification](#717-two-step-verification-system--support-settings-admin).

### 7.10 Email and inbound email (Channels, admin)

See [Email](#6-email): SMTP, sender identities, plus addressing, triage mailbox, notification switches and previews, IMAP and webhook mailboxes, and the inbound log with retry.

### 7.11 Webhooks (Channels → Webhooks, admin)

- **Create a webhook:**
  - **Target URL:** must be `https://`. Private and loopback addresses are refused unless `ST_WEBHOOK_ALLOW_PRIVATE=true`.
  - **Events:** the event types to send.
  - **Include internal notes and reasons:** sends `note.created` and the internal data of other events. Use it only for trusted staff tools.
  - **Active.**
- **Signing secret:** shown **once** after creation. *Rotate secret* creates a new one.
- **Test** sends a `ping` event and shows the response status and time.
- **Deliveries** shows the log of each delivery attempt: time, event, status, latency, attempt number and error.
- **Automatic disabling:** a webhook is disabled after 20 consecutive failures, or at once when the receiver answers `410 Gone`. Admins get an email, and *Re-enable* turns it back on.
- The payload format and how to verify signatures are in [Webhooks](#12-webhooks).

### 7.12 API tokens (System → All API tokens, admin)

Lists every personal access token on the instance, with its owner, scopes, last use and expiry. An owner filter narrows the list. Admins can revoke any token. Users manage their own tokens in their profile (see [Agent guide](#88-profile-tokens-and-shortcuts)).

### 7.13 Audit log (System → Audit log, admin)

A read-only record of security-relevant actions, such as sign-ins, logouts, break-glass logins and changes to staff and settings.
- **Columns:** time, person, action (e.g. `staff.updated`), target, IP address and details.
- **Filters:** action, person and date.
- **Retention:** entries can't be edited or deleted, and are kept for `ST_AUDIT_RETENTION_DAYS` (default 365).
- **Privacy (GDPR):** data exports, erasure, retention policies (which can shorten this period) and consent records are on **People → Privacy**. See [Privacy and GDPR](#718-privacy-and-gdpr-people--privacy-admin).
- **New actions:** two-step verification writes `auth.mfa_*` and `auth.reauth_failed` entries and the privacy tools `privacy.*` entries. The lists are in [mfa.md](features/mfa.md#audit-log-entries) and [gdpr.md](features/gdpr.md#audit-entries).

### 7.14 Queue and failed jobs (System → Queue / Failed jobs, admin)

- **Queue** updates live. It shows:
  - jobs waiting for a retry;
  - failed jobs;
  - ticket events not yet dispatched;
  - for each in-process queue (realtime, email, webhooks, search, …): waiting items, capacity, workers and fill level.
- **Failed jobs** lists jobs that are retrying or have failed: emails, webhook deliveries and inbound messages.
  - Columns: attempts, next attempt and last error. You can view a job's data.
  - **Retry** queues a job again. Fix the cause first, for example the SMTP settings.

How retries work:
- A job is retried with exponential backoff, starting around 30 s and capped at 6 h.
- After its last attempt it becomes *failed*.
- Successful jobs are never written to the database. Only retries and failures are, which keeps disk writes low.

### 7.15 System and storage (System → System / Storage, admin)

- **System** shows:
  - **Application:** version, commit, uptime and live WebSocket connections.
  - **Database and disk:** engine, schema version, database size, and disk used and free in the data directory.
  - **Status:** *Staff sign-on*, *Customer sign-on* and *Outgoing email* (Working, Failing, Provider unreachable, Not configured), and the number of retrying and failed jobs.
- **Storage** shows the attachment backend (local disk or S3-compatible), with endpoint and bucket, space used and file count. Moving storage is a CLI task: see [S3 storage option](#24-s3-storage-option).

### 7.16 Automation and assignment (Support setup, supervisor+)

SimpleTickets can do routine ticket work for you. Everything automatic is done by the **Automation** system user: the timeline shows *Automation rule applied* with the rule's name, and customers never see rule names or that entry. The full guide, with every condition, action and API example, is [features/automation.md](features/automation.md).

- **Automatic assignment** (**People → Teams**, edit a team, *Automatic assignment*):
  - *Manual* (the default) leaves unassigned tickets in the queue.
  - *Round robin* takes turns over the eligible members.
  - *Least busy* picks the eligible member with the fewest tickets that are not resolved or closed.
  - **Eligible** means what it means for manual assignment (active, a member of the team, tier at least the ticket's tier), and **not marked away**. If nobody is eligible, the ticket stays unassigned.
  - It runs whenever a ticket of the team ends up without an owner: a new ticket, a transfer, an escalation that removes the owner, an unassign, or an agent losing the team, tier or account.
- **Automation rules** (**Support setup → Automation rules**): *when this happens and these conditions hold, do that*.
  - **Events:** *Ticket created*, *Customer replied*, *Ticket updated*, and *Time-based (hourly)* for rules such as "pending for 3 days".
  - **Conditions** combine with *all* or *any*: subject and message text (contains or a pattern), channel, priority, status, support level, category, team, organization, requester email domain, tags, custom fields, business hours, and for time-based rules the hours since the last update or in the current status.
  - **Actions** (up to 10): set priority, category, team, status or support level; add or remove a tag; assign with a strategy or to a named agent; add an internal note; send a canned reply; escalate one level; add a watcher.
  - Rules run in list order; *Stop processing later rules* ends the run for that event. Reorder them with the arrows.
  - **Dry run** (the flask icon) tests a saved rule, active or not, against a ticket number and changes nothing.
  - **Safety rails:** changes made by automation never trigger rules, at most 10 actions run per event across all rules, rules run in the background through the durable job queue, and a time-based rule fires once per ticket until the ticket changes again.
- **Shared macros** (**Support setup → Shared macros**) are offered to every agent; only supervisors and admins edit them. Agents also keep personal macros. See [Macros and availability](#89-macros-and-availability).

### 7.17 Two-step verification (System → Support settings, admin)

Two-step verification (two-factor authentication, MFA) adds a second step after the password for **local** accounts: staff, including break-glass admins, and customers. Accounts that sign in through your identity provider are not covered (see [Two-step verification and SSO](#57-two-step-verification-and-sso)). The methods are an **authenticator app** (TOTP), a **code by email** (needs SMTP) and 10 one-time **recovery codes**. The full guide is [features/mfa.md](features/mfa.md).

- **Policy:** **System → Support settings**, card **Security**, *Require two-step verification*: *Nobody (optional)* (the default), *All staff accounts* or *Everyone*. A covered user without a second factor is taken through the setup right after their password, before they get a session. Users already signed in keep their session until their next sign-in. Set it up on your own account before you require it.
- **Reset a user** who lost their device and recovery codes: in the **Edit staff member** dialog (**People → Staff**) or on the customer's detail page, choose **Reset two-step verification**. It removes all their methods and keeps their sessions. Only admins can reset.
- **Locked out yourself?** Run `simpletickets reset-mfa --email …` on the server (see [CLI reference](#132-cli-reference)). With `reset-password`, the break-glass admin can always get back in.
- **Users** turn it on in their profile; see [Profile, tokens and shortcuts](#88-profile-tokens-and-shortcuts) and the [Customer portal guide](#9-customer-portal-guide).
- **API tokens are not affected.** A personal access token keeps working whether or not its owner uses two-step verification, so give tokens short expiry dates and the smallest scopes.
- **Secrets:** authenticator secrets are encrypted with `ST_SECRET_KEY`, and `simpletickets rotate-secrets` re-encrypts them. Rotate before you remove `ST_SECRET_KEY_PREVIOUS`, or every authenticator app stops working.
- Every policy change, reset and failed code is in the [audit log](#713-audit-log-system--audit-log-admin).

### 7.18 Privacy and GDPR (People → Privacy, admin)

Tools that help you answer data protection requests and keep only the data you need. **They are tools, not legal advice**: what you must export, erase or keep depends on your situation and jurisdiction. The full guide, with the export contents, the exact effect of each erasure mode and what an erasure does not cover, is [features/gdpr.md](features/gdpr.md).

- **Requests** tab: every export and deletion request, with filters. **Approve** a pending deletion request with an erasure mode, or **Reject** it with a reason shown to the customer. Ready exports have a **Download** button until they expire.
- **Retention and settings** tab:
  - **Retention policy:** a daily job anonymizes or deletes closed tickets after N months, deletes raw inbound emails, old audit entries and finished export files. **Preview** shows what the job would process now. The policy can only make the built-in periods (90 days of raw email, `ST_AUDIT_RETENTION_DAYS`) shorter.
  - **Deletion requests:** *Approve automatically after (days)* (0 = admins decide), the mode for automatic approvals, and the **Privacy policy version** recorded with each consent.
- **Per person:** the **Privacy** card on a customer's detail page and in the **Edit staff member** dialog has **Export data** (optionally *Include internal notes and staff-only fields*) and **Erase personal data**.
- **Erasure** is irreversible. *Anonymize* replaces the name and email, removes sign-in methods, sessions and tokens and disables the account; *Anonymize and redact* also redacts the messages the person wrote and deletes their files. Tickets stay as business records. You can't erase your own account, a break-glass admin or the last active admin.
- **Self-service:** customers and staff export their own data from **My data**; customers can also ask for their account to be deleted. See the [Customer portal guide](#9-customer-portal-guide).
- **Consent:** when **System → Branding → Legal links → Privacy policy URL** is set, registration requires accepting the policy and records the acceptance.
- **Backups:** export files are left out of `simpletickets backup` on purpose. Backups taken before an erasure still contain the erased data until you rotate them out.

### 7.19 Notification centre

The bell in the top bar of the agent panel and the customer portal lists ticket notifications, live, with an unread badge. Agents and customers can also get them as browser pop-ups. The full guide is [features/notifications.md](features/notifications.md).

- **Nothing to set up.** It runs inside the single container, with no push service, and works without SMTP.
- It uses the same rules as the ticket emails ([§6.3](#63-notifications-templates-and-languages)), so a notification and its email go to the same people. Team and triage **mailboxes** get email only.
- **Admin → Email → Notifications** switches templates off for **email only**; the bell and browser pop-ups are not affected.
- Each user picks the channels per type in their profile; see [Notification centre](#810-notification-centre) for agents and the [Customer portal guide](#9-customer-portal-guide).
- **Retention:** notifications are kept for 90 days, and at most the 500 newest per user. They store no message text; names and subjects are looked up when the list is shown, so an erasure or a deleted ticket is reflected at once.

### 7.20 AI assistants (MCP) (System → Support settings, admin)

The **Integrations** card has one switch, **AI assistants (MCP)**. It turns on the built-in Model Context Protocol endpoint `/mcp`, which AI assistants (Claude Code, Claude Desktop, Cursor and other MCP clients) use to work with the help desk. It is **off by default**; while off, `/mcp` answers 404. The change is audited as `integrations.updated`.

- Users connect their own assistant with their own personal access token; the *API tokens* page then shows a **Connect an AI assistant** panel with the endpoint and ready-to-copy configurations.
- An assistant has exactly the permissions of the token: the user's role, limited by the token's scopes. Customers' assistants get customer tools only and never see internal notes or staff-only fields.
- To cut off one assistant, revoke its token (*System → All API tokens*). To cut off all of them, turn the switch off.

Details, tools and client setup: [features/mcp.md](features/mcp.md).

### 7.21 Projects (Administration → Projects, supervisor+)

A project groups tickets under a key: `WEB-12`, `MOB-3`. Every installation has at least one, the **Default project** (key `HD`), which the upgrade creates and fills with everything that existed; a single-project installation works exactly as before and the agent panel hides the project switcher.

- **Supervisors and admins** create, rename, deactivate and delete projects, choose the key (2–10 upper-case letters and digits, fixed once the project has tickets) and manage the **members**.
- **Agents see only the tickets of the projects they are members of** (on top of the team and tier rules). Supervisors and admins see every project. New staff join the default project unless you choose their projects.
- A member with the **project admin** role manages that project's categories, the teams serving it, its SLA overrides, KB categories, automation rules, shared macros, canned responses and portal branding, and nothing global.
- Each category, inbound mailbox and ticket belongs to one project. Teams can serve several. SLA policies fall back to the global ones.
- **Customers** pick a project when several are open to them. An organization can be limited to some projects. A portal-visible project has its own entry at `/p/{key}` with its own title, introduction, logo and colour.
- Email subjects carry `[KEY-123]`; replies thread with it or with the old `[#1042]`.

**Admin guide.** Open *Administration → Projects*: create a project (key, name, colour), set its default team, add members and give the trusted ones the *project admin* role, map the teams that serve it and, if needed, override the SLA per priority (empty rows inherit the global policy). Pick the project on categories, mailboxes, KB categories, rules, macros, canned responses, custom fields, teams, organizations and webhooks. In *Staff* you edit a person's projects. The portal fields (title, introduction, colour, logo) brand the project's entry `/p/{key}`.

**Agent guide.** With several projects a **project menu** in the top bar limits every list, queue badge and report to one project (or *All projects*); your choice is remembered. Tickets show `WEB-123` and a coloured project chip; `/agent/tickets/WEB-123` and the search box open a ticket by reference. *Move to project* in the ticket moves it and gives it the next reference of the target.

**Portal.** Customers who may open tickets in several projects pick one in the new-ticket form (hidden when there is only one). `/p/WEB` is a branded entry that preselects the project; an unknown or non-public key shows a "not found" page.

Details, permissions and API: [features/projects.md](features/projects.md) (the screens: [§11](features/projects.md#11-the-user-interface)).

---

## 8. Agent guide

Sign in at `/agent/login`, usually with **Continue with single sign-on**.

### 8.1 Layout

- **Top bar:**
  - Search box: "Search tickets or jump to #number". Type `#1042` or `1042` to open that ticket directly.
  - Keyboard-shortcuts help, language switcher, light/dark/system theme, the **notification bell** ([8.10](#810-notification-centre)), and the account menu: the **Away** switch and *My macros* ([8.9](#89-macros-and-availability)), *Profile*, *API tokens*, *Sign out*.
- **Drawer:**
  - **New ticket**.
  - **Queues** with live counts: *My tickets*, *Unassigned*, *Triage*, *All tickets*.
  - One queue per **Tier** you can see. Agents see tiers up to their own; supervisors and admins see all three.
  - Your **Saved views**, plus shared ones.
  - *Customers*, *Organizations*, *Knowledge base*, *Reports* and, if you may open them, the *Administration* pages.
- **Live updates:** lists, counts and the open ticket update live over WebSocket. If the connection drops, a banner says "Connection lost — reconnecting…". The panel catches up automatically when it reconnects.

### 8.2 Ticket list

**Filtering and sorting**
- Filters: search text, status, priority, tier, team (including *Triage (no team)*), assignee (*Me*, *Unassigned* or a person), category, tag, *SLA breached only*, and custom fields.
- Sort orders:
  - *Needs attention* (the default; see [Ticket lifecycle](#14-ticket-lifecycle));
  - *Recently updated* and *Least recently updated*;
  - *Newest first* and *Oldest first*;
  - *Highest priority first*;
  - *Due soonest*;
  - *Highest number first*.
- Every filter is part of the URL, so you can bookmark or share a view.

**Columns**
- Columns: number, subject (with tags and parent/child chips), requester, status, priority, tier, team, assignee, updated, and due (with a *Breached* chip).
- **Columns** adds custom-field columns. Your choice is remembered in the browser.

**Saved views and bulk actions**
- **Save view** stores the current filters under a name. *Share with all staff* makes the view visible to everyone.
- **Bulk actions:** tick rows, then choose *Assign to*, *Set status*, *Set priority* or *Move to team*. The result shows how many tickets were updated and which ones failed.

### 8.3 Working a ticket

The ticket page has a **Conversation** tab and a **Timeline** tab. The timeline lists every event, for example *Escalated* with its reason, *SLA breached*, *Merged* or *Automation rule applied* (by the **Automation** user, with the rule's name and what each action did). Only the controls you are allowed to use are shown.

**Macros.** The **Macros** button in the ticket header applies a bundle of reply, status, priority, tags and assignee in one step. See [Macros and availability](#89-macros-and-availability).

**Presence.** Avatars in the header show other staff viewing or typing on the ticket. The composer warns "{name} is replying…" when someone else is typing.

**Composer**
- **Reply** sends a public message. It is emailed to the requester and watchers.
- **Internal note** (lock icon, dashed style) is visible to staff only: "Only staff can see internal notes."
  - Type `@` to mention a colleague, who is then notified.
  - If you switch a draft from note to reply, the composer warns you that the text will go to the customer.
- **Editor:** Markdown with *Write* and *Preview* tabs. **Ctrl+Enter** (or Cmd+Enter) sends.
- **Canned response:** search for one and insert it. Variables are filled in.
- **Insert article link:** links a knowledge-base article. A public reply can only link articles the customer can read.
- **Attachments:** drag and drop, paste, or click to choose. Up to 10 files per message, within the instance's upload limits.
- **Send and set status:** the ▲ next to *Send* sends the message and sets *Pending*, *On hold* or *Resolved* in one step.
- **Status errors:** if the status change isn't allowed from the current status, a dialog lists the allowed statuses or offers to send without changing the status. Your draft is kept.
- **Editing notes:** you can edit your own internal notes for 15 minutes. Public replies are never edited; they can only be followed by another reply.

**Properties panel**
- **Fields:** status, priority, category and team.
  - Moving a ticket to a team you're not in may remove your access; the panel warns you.
- **Assignment:** assignee, and *Assign to me*. The picker lists only staff who can see the ticket.
- **Tier**, with *Escalate* and *De-escalate*.
- **SLA:** *First response* and *Resolution* badges, showing on track, due soon, breached, met or paused.
- **Requester**, with a link to their organization.
- **Tags, watchers and fields:** tags; *Watch* / *Stop watching* / *Add watcher*; and custom fields.

**Conflicts.** If someone else changes the ticket while you edit, a *Ticket changed* dialog offers to reload. Your draft is kept. Behind the scenes, updates send `If-Match`, so you never overwrite someone else's change by accident.

### 8.4 Escalating and handing back

1. Press **Escalate** (or the `e` key). The target tier is the next one up.
2. Optionally pick a **target team** ("Leave empty to keep the current team.").
3. Enter a **reason**. It is required, and only staff see it.

What happens next:
- If the ticket leaves your view, you are taken back to the list with "Ticket handed over. It is no longer in your queue."
- The target team is notified.
- The assignee is cleared if they can't see the new tier.

**De-escalate** ("Hand back to the previous tier") works the same way. The assignee keeps the ticket.

### 8.5 Links, merge and split

- **Linked tickets → Add link:**
  - Find a ticket by number or subject and choose *Parent of*, *Related to* or *Duplicate of*.
  - The panel groups linked tickets as parent, children, related, duplicate of and duplicated by.
- **⋮ → Merge into another ticket:**
  - Pick the target. "Messages and attachments of #A move to #B, and #A is closed as a duplicate."
  - Merging tickets of unrelated customers is refused. Link them instead.
- **⋮ → Split into a new ticket:**
  - Tick the messages to move, enter a subject, then *Create child ticket*.
  - The new ticket is linked as a child.

### 8.6 Creating tickets for customers

**New ticket** opens a ticket on behalf of a customer:
1. Pick an existing customer by name or email, or switch to *New customer* and enter a name and email. The customer is found or created.
2. Fill in the subject, message, category, team, priority, custom fields and attachments.

### 8.7 Customers, organizations, knowledge base and reports

- **Customers:**
  - Search, create and edit customers: name, email, organization, role (*Requester* or *Organization manager*), language and status.
  - *Send an invitation email* to let them set a password.
  - A customer's page lists their tickets.
- **Organizations:**
  - Name, **email domains** (customers who verify an address on these domains join automatically), internal notes, and active/archived.
  - Members and tickets are listed on the organization's page.
- **Knowledge base:** write and update articles. See [Knowledge base](#77-knowledge-base-supervisor-for-categories-and-publishing).
- **Reports:**
  - Pick a date range, a team (supervisors only) and how SLA time is counted: *Around the clock* or *Team business hours*.
  - The report shows:
    - tickets created, resolved, closed and reopened;
    - first response time and resolution time, as median and 90th percentile;
    - customer satisfaction;
    - SLA met versus breached;
    - breakdowns by tier and by team, and escalations in.
  - Agents see their own numbers; supervisors and admins see everything.

### 8.8 Profile, tokens and shortcuts

- **Profile:**
  - Name and language.
  - Password, for local accounts only. Changing it signs out your other sessions.
  - **Email signature:** Markdown, up to 2 000 characters, appended to your public replies in customer emails.
  - Active sessions, each with *Sign out this session*.
  - **Two-step verification** (local accounts only, below *Password*): set up an authenticator app or codes by email, and get recovery codes. Every change asks for your password first. See [features/mfa.md](features/mfa.md#for-users).
  - **Notifications:** which notification types reach you in the app, by email and as browser pop-ups. See [Notification centre](#810-notification-centre).
  - **My data** (header button): download an export of your personal data. See [features/gdpr.md](features/gdpr.md#for-customers-and-staff-my-data).
- **API tokens:**
  - Create a token with a name, scopes and an optional expiry date.
  - The token is shown **once**; copy it right away.
  - The list shows each token's prefix and when it was last used. *Revoke* deletes a token.
- **Keyboard shortcuts** (press `?` for help):

  | Key | Where | Action |
  |---|---|---|
  | `j` / `k` | Ticket list | Next / previous ticket |
  | `Enter` | Ticket list | Open the focused ticket |
  | `x` | Ticket list | Select / unselect the focused ticket |
  | `r` | Ticket | Reply |
  | `n` | Ticket | Internal note |
  | `e` | Ticket | Escalate |
  | `?` | Anywhere | Shortcut help |
  | `Ctrl/Cmd + Enter` | Any editor | Send |

  Shortcuts are paused while you type in a field or while a dialog is open.

### 8.9 Macros and availability

**Macros** apply a bundle in one click: a public reply or an internal note (with the placeholders `{{customer.name}}`, `{{ticket.number}}` and `{{agent.name}}`), a status, a priority, tags to add and remove, and an assignee (*me*, *nobody* or a named agent).
- **Personal macros** are yours: **My macros** in the account menu.
- **Shared macros** are offered to every agent and are managed by supervisors and admins under **Support setup → Shared macros**.
- **Apply** one with the **Macros** button in the ticket header. It runs as you, in one step: reply first, then priority, tags, assignee and status. You need permission for every part; if any part isn't allowed, nothing changes and you see why.

**Away.** The **Away** switch in the account menu stops automatic assignments to you while you are out ("You receive no automatic assignments."). Anyone can still assign you a ticket by hand. It stays on until you turn it off.

Automation rules and team assignment strategies are described in [Automation and assignment](#716-automation-and-assignment-support-setup-supervisor); the full guide is [features/automation.md](features/automation.md).

### 8.10 Notification centre

The **bell** in the top bar shows how many notifications are unread. It updates live, and reading a notification in one tab updates every other tab.
- **The dropdown** shows the 10 newest. Click one to mark it read and open its ticket. **Mark all as read** clears the badge; **See all notifications** opens `/agent/notifications`, with *All* / *Unread*, a filter by kind, delete and **Load more**.
- **What you get:** new tickets in your queue, customer replies on your tickets (or unassigned ones in your team), tickets assigned to you by someone else, escalations to your team, @mentions in notes, SLA warnings and breaches, and resolved linked tickets. Never about your own actions, and only for tickets you can see. When a team's notifications go to the *Team mailbox* only, its new-ticket and escalation notices go to that mailbox and not to the bell.
- **Preferences:** the **Notifications** card in your profile has one row per type and a checkbox for *In the app*, *Email* and *Browser pop-up*. Browser pop-ups are off by default.
- **Browser pop-ups** need **Allow browser notifications** in that card, and appear only while a SimpleTickets tab is open in the background. Nothing arrives when every tab is closed, so use email for anything you must not miss.

The full guide, including the limits of browser notifications, is [features/notifications.md](features/notifications.md).

---

## 9. Customer portal guide

> The portal exists only while **Customer portal enabled** is on (it is by default). With it off, these pages redirect to the staff sign-in, see [Switching the customer portal off](#switching-the-customer-portal-off).

The portal is at the root of your help desk, e.g. `https://help.example.com/`. It uses your branding and languages.

**Signing in.** On `/login`, customers can:
- sign in with email and password;
- use the SSO button, if configured;
- choose *Sign in with a link sent by email* (magic link);
- use *Forgot your password?*;
- or *Create an account*.

The email-based options appear only when email is working. *Create an account* also needs self-registration to be on. A new account must verify its email address before it can be used. When the help desk publishes a privacy policy, registration asks you to tick "I have read and accept the privacy policy".

If you turned on **two-step verification**, or the help desk requires it, you are asked for a code after your password or sign-in link: from your authenticator app, by email (**Email me a code**), or one of your recovery codes. When the help desk requires it and you have no method yet, you set one up right there before you get in.

**My tickets**
- Lists your tickets, with search and a status filter. Statuses use customer wording: *Open*, *Awaiting your reply*, *Resolved*, *Closed*.
- **Organization managers** can switch between *Mine* and *Organization*, and see the requester column.
- The list updates live.

**New ticket**
- Enter a subject. While you type, matching help articles are suggested ("These articles might already answer your question:").
- Pick a category and a priority. *Urgent* appears only if the help desk allows it.
- Describe the problem in Markdown and add attachments.
- Fill in any extra fields the help desk asks for.
- Organization managers can open a ticket **on behalf of** a colleague.

**A ticket's page**
- Shows the public conversation only. Internal notes are never visible to customers.
- **Reply** to add a message. Replying to a ticket that is *Awaiting your reply* moves it back to *Open*.
- **My issue is solved** marks the ticket resolved. **Reopen** brings it back within the reopen window.
- On a **closed** ticket, the reply button reads *Send and open follow-up* and creates a new ticket linked to the old one.
- **Satisfaction rating:** after resolution, rate the support from 1 to 5, with an optional comment. The link in the resolution email works without signing in.

**Profile** (`/profile`, from the account menu)
- Name, language, theme and password (for password accounts).
- **Notifications:** one row per type (*Request received*, *Reply to my request*, *Request resolved*) with *In the app*, *Email* and *Browser pop-up*. Replies to your requests are always emailed. See [Notifications](#portal-notifications) below.
- **Two-step verification** (`/profile/security`, password accounts only): set up an authenticator app or codes by email, keep your recovery codes, or turn it off. See [features/mfa.md](features/mfa.md#for-users).
- **My data and privacy** (`/profile/data`): see [My data](#portal-my-data) below.
- **Active sessions:** sign out other devices.
- **API tokens:** with the scopes *Read my tickets* (`tickets:read`) and *Open and reply to tickets* (`tickets:write`), for your own scripts.
- **Organization members** (organization managers only): invite colleagues, change their role (*Member* or *Organization manager*), and disable or re-enable them.

<a id="portal-notifications"></a>
**Notifications**
- The **bell** in the top bar shows unread notifications about your requests: a request the support team opened for you, a new reply, a resolved request. It updates live; **See all notifications** opens `/notifications`.
- A notification shows only the ticket number and subject. You are never notified about internal notes, assignments or escalations.
- **Browser pop-ups** are off by default. Turn them on in the profile's **Notifications** card with **Allow browser notifications**. They only appear while a tab of the help desk is open in the background. Details: [features/notifications.md](features/notifications.md#browser-notifications).

<a id="portal-my-data"></a>
**My data** (**Profile → My data and privacy**)
- **Request a data export** prepares a ZIP of the personal data the help desk stores about you. You get an email when it is ready, and the page shows a **Download** button (use **Refresh**). The file is kept for 7 days by default.
- **Request account deletion** sends a deletion request to the help desk's admins, with an optional note. They approve or reject it; a rejection reason is shown here.
- **Consent records** lists the privacy policies you accepted.

What an export contains and what deletion does: [features/gdpr.md](features/gdpr.md).

**Help center** (`/kb`)
- Browse categories, search (from 2 characters) and read articles in your language.
- Answer "Was this article helpful?".
- *Still need help?* leads to the new-ticket form.

---

## 10. REST API

The web app uses exactly the same API that integrations use, so anything the UI can do, a script can do too.

- **Specification:** [`api/openapi.yaml`](../api/openapi.yaml), OpenAPI 3.1.
  - A running instance serves it at `GET /api/v1/openapi.yaml`.
  - A static, script-free reference page listing every operation and its scopes is at `GET /api/docs`.
- **Base path:** `/api/v1`. Bodies are JSON (`application/json`), except uploads (`multipart/form-data`) and downloads.
- **Timestamps:** RFC 3339 in UTC, e.g. `2026-10-20T10:00:00Z`.
- **Ids:** UUIDv7 strings. A ticket can also be fetched by its reference, `GET /api/v1/agent/tickets/by-ref/WEB-12` (customers: `/portal/tickets/by-ref/…`), or by its global number, `GET /api/v1/agent/tickets/by-number/1042`.
- **Projects:** tickets carry `project_id`, `ref` and `project`; lists, counts, reports and lookups take a `project` filter (id or key). See [features/projects.md](features/projects.md).

### 10.1 Areas

| Prefix | Who | Contents |
|---|---|---|
| `/api/v1/auth/*` | anyone | Login, logout, OIDC, registration, password reset, magic link, invitations, the second sign-in step (`/auth/mfa/*`), public configuration (`GET /auth/config`) |
| `/api/v1/me*` | any signed-in user | Profile, tokens, sessions, email signature (staff), notifications, two-step verification, My data, availability (staff) |
| `/api/v1/portal/*` | customers only (staff get 404) | Tickets, replies, attachments, categories, custom fields, organization members, KB suggestions |
| `/api/v1/agent/*` | staff only (customers get 404) | Tickets, messages, escalation, links, merge, split, watchers, customers, organizations, views, canned responses, macros, reports, KB editor |
| `/api/v1/admin/*` | admins; supervisors for support configuration; project admins for their projects' configuration | Staff, teams, categories, SLA, calendars, custom fields, automation rules and shared macros (supervisor+), settings, security policy and two-step resets, privacy, branding, email, inbound, webhooks, tokens, jobs, queue, system |
| `/api/v1/kb/*` | anyone; results depend on who asks | Knowledge base browse, search, feedback |
| `/api/v1/ws` | any signed-in user or token | WebSocket (see §11) |

### 10.2 Authentication

**Personal access tokens (scripts and integrations).** Create one in the UI (*Profile → API tokens*) or with `POST /api/v1/me/tokens`, then send it as a bearer token:

<!-- doctest:api-me -->
```sh
export ST=https://help.example.com
export API_TOKEN=st_pat_…     # shown once when created

curl -sS "$ST/api/v1/me" -H "Authorization: Bearer $API_TOKEN"
```

| Scope | Grants |
|---|---|
| `tickets:read` | Read tickets, messages, attachments and events; subscribe to the WebSocket. Implies `kb:read` |
| `tickets:write` | Create tickets, reply, add notes, update fields, escalate |
| `directory:read` | Read users, organizations, teams, categories (staff only) |
| `directory:write` | Create and update customers and organizations (staff only) |
| `kb:read` | Read knowledge-base articles visible to the owner |
| `kb:write` | Write KB drafts; publish and archive only if the owner is a supervisor or admin |
| `admin` | Everything the owner (an admin) can do in the UI |

- A token can **never** do more than its owner can do right now: its effective permissions are the owner's permissions intersected with the token's scopes, checked on every request.
- Disabling a user stops all their tokens at once.
- Customer tokens can only have `tickets:read` and `tickets:write`.
- Only a hash of each token is stored, and tokens can expire.
- Requests with a bearer token don't need CSRF tokens.

**Session cookie (browsers).** The web app signs in with `POST /api/v1/auth/login` and receives the `st_session` cookie (HttpOnly, SameSite=Lax, and `Secure` on HTTPS). A browser integration on the same origin must follow two rules for every `POST`, `PUT`, `PATCH` and `DELETE`:
1. Send the header `X-CSRF-Token` with the `csrf_token` value from `GET /api/v1/me`.
2. Come from the same origin as `ST_PUBLIC_URL`, as shown by the `Origin` (or `Referer`) header.

Requests that fail either check get `403` with the problem type `csrf`. Login endpoints are rate-limited per IP to 10 attempts per minute. After 10 failed logins in 15 minutes, an account is locked for 15 minutes for IP addresses that have never signed in to it successfully. Wrong two-step verification codes count as failed logins.

**Two-step verification.** For an account with a second factor, `POST /api/v1/auth/login` (and the verify-email, magic-link and invitation endpoints) answers `200` with an `MfaChallenge` instead of `Me`, and sets no cookie:

```json
{"mfa_required": true, "mfa_token": "…", "methods": ["totp", "recovery"], "enrollment_required": false, "expires_at": "…"}
```

Complete it with `POST /api/v1/auth/mfa/verify {mfa_token, method, code}`, which returns `Me` and the cookie. The steps, including the email code and forced setup, are in [features/mfa.md](features/mfa.md#api). Personal access tokens never go through this step.

**The `admin` scope**, which only admins can hold, is required with a token for automation rules, shared macros, the security policy and two-step resets, and every privacy route, including the self-service `/api/v1/me/privacy*`. Supervisors therefore manage automation rules and shared macros in the browser only. `/api/v1/me/mfa*` refuses tokens altogether.

### 10.3 Pagination, filtering and sorting

Lists are cursor-paginated:

```
GET /api/v1/agent/tickets?limit=50&cursor=<opaque>
→ { "items": [ … ], "next_cursor": "…" | null }
```

`limit` ranges from 1 to 200 and defaults to 50. Keep calling with `next_cursor` until it is `null`.

`GET /api/v1/agent/tickets` takes these filters:

| Filter | Values |
|---|---|
| `status` | Comma-separated: `new,open,pending,on_hold,resolved,closed` |
| `priority` | Comma-separated: `low,normal,high,urgent` |
| `team_id` | A team id, or `none` for triage |
| `assignee` | `me`, `none` or a user id |
| `tier` | `1`–`3` |
| `category_id`, `organization_id`, `requester_id`, `tag` | One value each |
| `updated_since` | An RFC 3339 timestamp |
| `breached` | `true` |
| `q` | Full-text search |
| `cf.<key>` | A custom field value, e.g. `cf.contract=gold` |

`sort` is one of `attention` (the default), `updated_at`, `-updated_at`, `created_at`, `-created_at`, `priority`, `-priority`, `due`, `number` or `-number`.

### 10.4 Errors

Errors use [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) `application/problem+json`:

```json
{
  "type": "https://simpletickets.dev/problems/invalid-transition",
  "title": "Invalid status transition",
  "status": 409,
  "detail": "closed → pending is not allowed",
  "allowed": ["open"]
}
```

Validation errors (422) carry an `errors` array of `{field, code, message}`. The codes (`required`, `email`, `max_length`, …) are stable, so clients can map them to form fields.

| Status | Problem `type` slug | Meaning |
|---|---|---|
| 400 | `bad-request` | Malformed request |
| 401 | `unauthenticated` | Not signed in, or the token is invalid, expired or revoked |
| 403 | `forbidden`, `csrf` | You can see the object but may not do this; or the CSRF/Origin check failed |
| 404 | `not-found` | Doesn't exist, **or** you may not see it |
| 409 | `conflict`, `invalid-transition`, `merge-cross-requester`, `link-cycle`, `idempotency-in-flight`, `email-disabled`, `setup-required` | State conflicts |
| 412 | `precondition-failed` | The `If-Match` value is stale |
| 413 | `payload-too-large` | The body or upload is too big |
| 415 | `unsupported-media-type` | Wrong content type |
| 422 | `validation`, `field-required`, `idempotency-mismatch` | Invalid input |
| 429 | `rate-limited` | Slow down; see the `Retry-After` header |
| 503 | `unavailable` | Temporarily unavailable |

### 10.5 Idempotency

`POST` requests that create things accept an `Idempotency-Key` header (up to 200 characters, e.g. a UUID). These are:
- `/agent/tickets` and `/portal/tickets`;
- `/agent/tickets/{id}/messages` and `/portal/tickets/{id}/messages`;
- `/agent/tickets/{id}/split`;
- `/agent/tickets/{id}/macros/{macro_id}/apply`.

How a key is handled:
- **First request:** its response is stored for 24 hours, per user and key.
- **Same key, same body:** the stored response is returned with the header `Idempotency-Replayed: true`. A network retry can't create a duplicate ticket or reply.
- **Same key, different body:** `422 idempotency-mismatch`.
- **Same key while the first request is still running:** `409 idempotency-in-flight`.
- **Server errors (5xx):** not stored, so you can retry with the same key.

Use one key per logical action, for example one per draft or form.

### 10.6 Optimistic concurrency (`If-Match`)

`GET /api/v1/agent/tickets/{id}` returns an `ETag` header. Send it back as `If-Match` on `PATCH /api/v1/agent/tickets/{id}`:
- If someone changed the ticket in between, you get `412 precondition-failed`. Reload and try again.
- `If-Match: *` or no header at all means "last write wins".
- Successful updates return the new `ETag`.

### 10.7 Ticket updates and `allowed_actions`

`PATCH /api/v1/agent/tickets/{id}` accepts any combination of these fields:
- `status`, `priority`, `category_id`;
- `team_id` (transfer);
- `assignee_id` (`null` to unassign);
- `subject`, `tags`, `custom_fields`.

Each changed field is authorised separately, and the whole patch is applied in one transaction. If any field is forbidden, nothing changes.

Ticket mutations (PATCH, escalate, de-escalate) return `{"ticket": {…}, "visible": true|false}`. `visible: false` means the change removed your own access, for example a transfer to another team.

Every staff ticket response includes `allowed_actions`: the actions you may perform right now. The possible values are:
- `reply`, `note`;
- `set_status`, `set_priority`, `set_category`;
- `assign`, `unassign`, `transfer`;
- `escalate`, `deescalate`;
- `link`, `merge`, `split`;
- `edit_fields`, `edit_subject`, `watch`;
- `close`, `reopen`, `resolve`, `delete`, `csat`.

### 10.8 Examples

**Create a ticket on behalf of a customer.** The customer is found by email, or created:

<!-- doctest:create-ticket -->
```sh
curl -sS -X POST "$ST/api/v1/agent/tickets" \
  -H "Authorization: Bearer $API_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d '{
        "subject": "Printer on 2nd floor is offline",
        "body_md": "Since this morning the printer shows **offline**.",
        "requester_email": "ann@example.com",
        "requester_name": "Ann Example",
        "priority": "high",
        "tags": ["hardware"]
      }'
# 201 → { "id": "0192f3c4-…", "number": 1042, "status": "new", … }
```

**Upload an attachment and reply with it:**

<!-- doctest:upload-reply -->
```sh
ATT=$(curl -sS -X POST "$ST/api/v1/agent/attachments" \
  -H "Authorization: Bearer $API_TOKEN" \
  -F "file=@driver-instructions.pdf" | jq -r .id)

curl -sS -X POST "$ST/api/v1/agent/tickets/$TICKET_ID/messages" \
  -H "Authorization: Bearer $API_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $(uuidgen)" \
  -d "{\"kind\":\"public\",\"body_md\":\"Please reinstall the driver as described.\",\"attachment_ids\":[\"$ATT\"],\"set_status\":\"pending\"}"
```

**Add an internal note** (staff only, never shown to the customer):

<!-- doctest:internal-note -->
```sh
curl -sS -X POST "$ST/api/v1/agent/tickets/$TICKET_ID/messages" \
  -H "Authorization: Bearer $API_TOKEN" -H "Content-Type: application/json" \
  -d '{"kind":"internal","body_md":"Hardware issue, ordering a replacement fuser."}'
```

**Escalate to the next tier**, optionally moving the ticket to another team:

<!-- doctest:escalate -->
```sh
curl -sS -X POST "$ST/api/v1/agent/tickets/$TICKET_ID/escalate" \
  -H "Authorization: Bearer $API_TOKEN" -H "Content-Type: application/json" \
  -d "{\"reason\":\"Needs a firmware fix\",\"target_team_id\":\"$TECH_TEAM_ID\"}"
# 200 → { "ticket": { "tier": 2, … }, "visible": true }
```

**Assign and change priority safely with `If-Match`:**

<!-- doctest:if-match -->
```sh
ETAG=$(curl -sS -D - -o /dev/null "$ST/api/v1/agent/tickets/$TICKET_ID" \
  -H "Authorization: Bearer $API_TOKEN" | awk -F': ' 'tolower($1)=="etag"{print $2}' | tr -d '\r')

curl -sS -X PATCH "$ST/api/v1/agent/tickets/$TICKET_ID" \
  -H "Authorization: Bearer $API_TOKEN" -H "Content-Type: application/json" \
  -H "If-Match: $ETAG" \
  -d "{\"assignee_id\":\"$AGENT_ID\",\"priority\":\"urgent\"}"
```

**List open tickets in a team, oldest first, following the cursor:**

<!-- doctest:list-tickets -->
```sh
curl -sS "$ST/api/v1/agent/tickets?status=new,open&team_id=$TEAM_ID&sort=created_at&limit=100" \
  -H "Authorization: Bearer $API_TOKEN" | jq '.items[] | {number, subject, status}, .next_cursor'
```

**As a customer, with a customer token, open and reply to a ticket:**

<!-- doctest:customer -->
```sh
curl -sS -X POST "$ST/api/v1/portal/tickets" \
  -H "Authorization: Bearer $CUSTOMER_TOKEN" -H "Content-Type: application/json" \
  -d '{"subject":"Invoice 2026-114 is wrong","body_md":"The VAT number is missing."}'

curl -sS -X POST "$ST/api/v1/portal/tickets/$TICKET_ID/messages" \
  -H "Authorization: Bearer $CUSTOMER_TOKEN" -H "Content-Type: application/json" \
  -d '{"body_md":"Here is the corrected address."}'
```

If the ticket is closed, the reply opens a follow-up ticket instead. The response is then `409 conflict`, with the new ticket's id in `followup_ticket_id`.

**Other useful endpoints:**
- Timeline: `GET /agent/tickets/{id}/events`.
- Watchers: `POST /agent/tickets/{id}/watchers` and `DELETE /agent/tickets/{id}/watchers/{user_id}`.
- Links: `GET`/`POST /agent/tickets/{id}/links`.
- Merge: `POST /agent/tickets/{id}/merge` with `{"into_ticket_id": …}`.
- Split: `POST /agent/tickets/{id}/split` with `{"message_ids": […], "subject": …}`.
- Sidebar counts: `GET /agent/tickets/counts`.
- Reports: `GET /agent/reports/summary`.
- Canned responses: `GET /agent/canned-responses` and `GET /agent/canned-responses/{id}`.
- Customers editing their customer-editable custom fields on an existing ticket: `PATCH /portal/tickets/{id}/fields` with `{"custom_fields": {…}}`.

### 10.9 Automation, two-step verification, privacy and notifications

Each feature guide has the complete route table, with request bodies and status codes. In short:

| Feature | Routes | Who | Guide |
|---|---|---|---|
| Automation rules | `/admin/automation/rules` (CRUD), `/admin/automation/rules/reorder`, `/admin/automation/rules/{id}/dry-run` | supervisor+ (tokens: `admin` scope) | [automation.md](features/automation.md#rules) |
| Macros | `/agent/macros` (yours plus shared), `/admin/macros` (shared), `POST /agent/tickets/{id}/macros/{macro_id}/apply` | staff; shared ones supervisor+ | [automation.md](features/automation.md#macros) |
| Assignment | `GET`/`PATCH /me/availability`; team field `assignment_strategy` on `/admin/teams` | staff; supervisor+ for teams | [automation.md](features/automation.md#availability-and-team-strategy) |
| Two-step verification | `/auth/mfa/*` (second sign-in step), `/me/mfa*` (self-service, session only), `/admin/security`, `/admin/users/{id}/mfa`, `/admin/users/{id}/mfa/reset` | anyone / the user / admin | [mfa.md](features/mfa.md#api) |
| Privacy | `/me/privacy*` (own exports and deletion request), `/admin/privacy/*` (requests, per-user export and erasure, settings, retention preview) | the user / admin | [gdpr.md](features/gdpr.md#for-integrators) |
| Projects | `/admin/projects*` (CRUD, members, teams, SLA overrides, logo), `/agent/projects`, `/portal/projects`, `by-ref` lookups, `project` filters, `GET /auth/config?project=KEY` | supervisor+; project admins for their project; staff; customers | [projects.md](features/projects.md#9-administration-api) |
| Notifications | `/me/notifications`, `/me/notifications/unread-count`, `/me/notifications/{id}/read`, `/me/notifications/read-all`, `DELETE /me/notifications/{id}`; `notification_prefs` on `PATCH /me` | the user | [notifications.md](features/notifications.md#for-integrators) |

Things to know:
- **Automation is an actor.** Changes made by a rule or an assignment strategy are ordinary ticket events with the actor id `automation`; webhooks send them with `actor: null`. Each applied rule also writes the staff-only timeline event `ticket.automation_applied`, which is not a webhook event type.
- **Macro apply** behaves like `PATCH /agent/tickets/{id}`: it returns `{ticket, visible}`, honours `If-Match` and `Idempotency-Key`, and changes nothing when any part is forbidden (`403`).
- **Partial or full updates.** `PATCH /admin/automation/rules/{id}` changes only the fields you send. `PATCH` on a macro and on `/admin/teams/{id}` replaces the whole object, so send every field you want to keep (a team keeps its `assignment_strategy` when the field is absent).
- **Registration and consent.** When a privacy policy URL is configured, `POST /auth/register` needs `accept_privacy: true`, otherwise `422` with the field code `required`.

### 10.10 AI assistants (MCP)

`POST /mcp` (at the site root, not under `/api/v1`) is a [Model Context Protocol](https://modelcontextprotocol.io) server over the Streamable HTTP transport. An admin turns it on first ([§7.20](#720-ai-assistants-mcp-system--support-settings-admin)); until then every method answers 404.

- **Authentication:** `Authorization: Bearer st_pat_…` only; no cookie, no CSRF token. Recommended scopes: staff `tickets:read tickets:write directory:read kb:read`, customers `tickets:read tickets:write kb:read` (drop `tickets:write` for read-only use).
- **Same rules as REST:** each tool runs the matching REST operation in-process with the token, so scopes, visibility, the lifecycle, `allowed_actions` and the customer views apply unchanged. Changes are ticket events by the token owner with the channel `api`.
- **Tools:** `search_tickets`, `list_my_tickets`, `get_ticket`, `create_ticket`, `reply_to_ticket`, `update_ticket`, `search_kb`, `get_kb_article`, `list_categories`; staff also `add_internal_note`, `escalate_ticket`, `deescalate_ticket`, `apply_macro`, `list_teams`. Failures are tool results with `isError: true` and the REST problem slug. Resources: `ticket://{ref}` (e.g. `ticket://WEB-12`; `ticket://{number}` still resolves), `kb://{slug}`.
- **Protocol revisions:** 2026-07-28 (stateless, per-request `_meta` and `MCP-Protocol-Version`/`Mcp-Method`/`Mcp-Name` headers, `server/discover`) and 2025-11-25, 2025-06-18, 2025-03-26 (`initialize` and `Mcp-Session-Id`; batches in 2025-03-26 only).
- **Origin:** a request whose `Origin` header is not the origin of `ST_PUBLIC_URL` (or listed in `ST_ALLOWED_ORIGINS`) is refused with 403.
- `GET /api/v1/integrations/mcp` (any signed-in user) returns whether MCP is on, the endpoint URL and the recommended scopes; `GET`/`PATCH /api/v1/admin/integrations` reads and sets `mcp_enabled` (admin).

```sh
claude mcp add --transport http simpletickets https://help.example.com/mcp \
  --header "Authorization: Bearer st_pat_…"
```

Claude Desktop and Cursor configurations, the tool reference and the error table: [features/mcp.md](features/mcp.md).

---

## 11. WebSocket protocol

Live updates use one WebSocket per client, for both the web app and API clients. Changes are always made through REST; the WebSocket only delivers events.

### 11.1 Connecting

| Item | Value |
|---|---|
| URL | `wss://help.example.com/api/v1/ws` |
| Subprotocol | `simpletickets.v1` (**required**) |
| Browser auth | The session cookie, plus an `Origin` that matches `ST_PUBLIC_URL` (otherwise 403 before the upgrade) |
| Token auth | `Authorization: Bearer st_pat_…` with `tickets:read` |
| Clients that can't set headers | `POST /api/v1/ws/ticket` (authenticated) → `{"ticket": "…", "expires_in": 30}`; then connect to `/api/v1/ws?ticket=…`. Single use, valid for 30 s |

Limits:
- 5 connections per user (`ST_WS_MAX_CONNS_PER_USER`) and 1 000 in total (`ST_WS_MAX_CONNS`);
- 200 topics per connection;
- client frames up to 4 KB, at most 20 per second with a burst of 40;
- the server pings every 25 s and drops silent connections after 60 s.

The server checks the user again every 60 s and whenever the account is disabled or a session is revoked. If the user lost access, the connection closes with `4401`. If the user's role or tier changed, every subscription is re-evaluated.

### 11.2 Frames

All frames are JSON text messages.

**Client → server:**

```json
{ "op": "subscribe",   "id": "c1", "topics": ["tickets", "ticket:0192f3c4-…", "counts", "me"] }
{ "op": "unsubscribe", "id": "c2", "topics": ["ticket:0192f3c4-…"] }
{ "op": "resume",      "id": "c3", "last_event_id": "0192f3d0-…" }
{ "op": "ping",        "id": "c4" }
{ "op": "presence",    "id": "c5", "topic": "ticket:0192f3c4-…", "state": "viewing" }
```

`presence` is for staff only. Its states are `viewing`, `typing` and `idle`.

**Server → client:**

```json
{ "type": "ack",    "id": "c1", "ok": true, "denied": ["ticket:…"] }
{ "type": "event",  "topic": "ticket:0192f3c4-…", "event_id": "0192f3d0-…", "event": "message.created",
  "at": "2026-10-20T10:00:00Z", "data": { "ticket": { … }, "message": { … }, "changes": { … } } }
{ "type": "counts", "data": { "mine": 3, "unassigned": 7, "triage": 2, "breached": 1, "tier": { "1": 4, "2": 2, "3": 1 } } }
{ "type": "resync", "reason": "gap_too_large" }
{ "type": "pong",   "id": "c4" }
{ "type": "presence", "topic": "ticket:…", "users": [ { "id": "…", "name": "Mia", "state": "typing" } ] }
```

- `denied` lists topics you may not subscribe to. No reason is given, for the same reason that reads of invisible objects return 404.
- An `event` frame's `event` field holds one of the event names in [Webhooks](#122-event-types), plus `ticket.access_lost`, staff-only timeline events such as `ticket.automation_applied`, and the `me` notices listed in [Topics](#113-topics). Every `event` frame carries `at`.
- `data.ticket` and `data.message` use the same schemas as the REST API. Customers receive the customer variants, with customer statuses and only fields visible to customers.

### 11.3 Topics

| Topic | Who | Delivers |
|---|---|---|
| `tickets` | Anyone with `tickets:read` | Changes to every ticket you can see, for refreshing lists |
| `ticket:<id>` | Anyone who can see the ticket | Detail events for one ticket. Internal notes and internal events go to staff only |
| `counts` | Staff | Sidebar counts, at most once per second |
| `me` | Anyone | Personal notices, e.g. assignment, mention, session revoked, branding changed; `notification.created` and `notification.read` for the notification centre ([guide](features/notifications.md#for-integrators)) |
| `queue:<team_id>` | Team members; supervisors and admins for any team | New and escalated tickets entering that team's queue |
| `project:<project_id>` | Project members; supervisors and admins for any project | Every event of the project's tickets you can see |
| `kb` | Staff | Knowledge-base editor updates |
| `admin:system` | Admins (tokens need `admin`) | `queue.changed` when the queue counters moved, for the admin queue page |

**Customer connections never receive:** internal notes, assignments, escalation reasons, tier or team data, other customers' tickets, counts, or queue topics. A customer connection that subscribes to a staff-only topic (`counts`, `kb`, `queue:*`, `project:*`, `admin:*`) breaks the protocol and is closed with `4403`; other topics it may not see are listed in `denied`.

If a ticket leaves your visibility while you are subscribed, for example because it was escalated past your tier, you get a final `ticket.access_lost` event and the subscription is dropped.

### 11.4 Resume after a reconnect

Events are ordered by `event_id`. Remember the last `event_id` you processed. After a reconnect:
1. Subscribe again.
2. Send `{"op":"resume","last_event_id":"…"}`.
3. The server replays the missed events you are allowed to see, up to 1 000 events or 24 hours. The replay is streamed as fast as your client reads it; a client that reads nothing for 10 seconds is closed as a slow consumer (`4008`).
4. If the gap is larger, you get `{"type":"resync"}`. Reload your data over REST.

Counts, presence and the `notification.*` frames on `me` are not replayed. After reconnecting, fetch `GET /agent/tickets/counts` and `GET /me/notifications/unread-count`.

Reconnect with jittered backoff, from 1 s up to 30 s.

### 11.5 Close codes

| Code | Meaning | What to do |
|---|---|---|
| `1001` | Server shutting down | Reconnect with backoff, then resume |
| `1002` | Missing or unknown subprotocol | Send `simpletickets.v1` |
| `4401` | Unauthenticated, or the session/token was revoked | Sign in again or get a new token |
| `4403` | Forbidden | Don't reconnect with the same request |
| `4008` | Slow consumer: your client fell behind (more than 64 queued messages) | Reconnect, resume or resync |
| `4029` | Rate limited | Send fewer frames |

### 11.6 JavaScript example

**In the browser, on the same origin as the help desk.** The cookie is sent automatically:

```js
const ws = new WebSocket(`wss://${location.host}/api/v1/ws`, 'simpletickets.v1');
let lastEventId = null;

ws.onopen = () => {
  ws.send(JSON.stringify({ op: 'subscribe', id: 's1', topics: ['tickets', 'me'] }));
  if (lastEventId) ws.send(JSON.stringify({ op: 'resume', id: 'r1', last_event_id: lastEventId }));
};
ws.onmessage = (msg) => {
  const f = JSON.parse(msg.data);
  if (f.type === 'event') {
    lastEventId = f.event_id;
    console.log(f.event, f.data.ticket?.number, f.data.ticket?.status);
  } else if (f.type === 'resync') {
    // reload lists over REST
  }
};
ws.onclose = (e) => console.log('closed', e.code, e.reason); // reconnect with backoff unless 4401/4403
```

**In Node.js 22+, with a personal access token.** Node's built-in WebSocket can't set headers, so get a one-time ticket first:

```js
const BASE = 'https://help.example.com';
const TOKEN = process.env.API_TOKEN;

const res = await fetch(`${BASE}/api/v1/ws/ticket`, {
  method: 'POST',
  headers: { Authorization: `Bearer ${TOKEN}` },
});
const { ticket } = await res.json();

const ws = new WebSocket(`${BASE.replace(/^http/, 'ws')}/api/v1/ws?ticket=${encodeURIComponent(ticket)}`, 'simpletickets.v1');
ws.onopen = () => ws.send(JSON.stringify({ op: 'subscribe', id: 's1', topics: ['tickets'] }));
ws.onmessage = (m) => {
  const f = JSON.parse(m.data);
  if (f.type === 'event') console.log(f.at, f.event, `#${f.data.ticket?.number}`);
};
setInterval(() => ws.send(JSON.stringify({ op: 'ping', id: 'p' })), 25_000);
```

### 11.7 Python example

This example uses the [`websockets`](https://pypi.org/project/websockets/) package (version 13 or newer, for `additional_headers`):

```python
import asyncio, json, os
import websockets

URL = "wss://help.example.com/api/v1/ws"
TOKEN = os.environ["API_TOKEN"]

async def main():
    last_event_id = None
    while True:
        try:
            async with websockets.connect(
                URL,
                subprotocols=["simpletickets.v1"],
                additional_headers={"Authorization": f"Bearer {TOKEN}"},
            ) as ws:
                await ws.send(json.dumps({"op": "subscribe", "id": "s1", "topics": ["tickets"]}))
                if last_event_id:
                    await ws.send(json.dumps({"op": "resume", "id": "r1", "last_event_id": last_event_id}))
                async for raw in ws:
                    f = json.loads(raw)
                    if f["type"] == "event":
                        last_event_id = f["event_id"]
                        t = f["data"].get("ticket", {})
                        print(f["event"], t.get("number"), t.get("status"))
                    elif f["type"] == "resync":
                        print("gap too large: reload over REST")
        except websockets.ConnectionClosed as e:
            if e.rcvd and e.rcvd.code in (4401, 4403):
                raise
        await asyncio.sleep(2)  # use jittered backoff in production

asyncio.run(main())
```

### 11.8 Go example

This example uses [`github.com/coder/websocket`](https://github.com/coder/websocket), the same library the server uses:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func main() {
	ctx := context.Background()
	c, _, err := websocket.Dial(ctx, "wss://help.example.com/api/v1/ws", &websocket.DialOptions{
		Subprotocols: []string{"simpletickets.v1"},
		HTTPHeader:   http.Header{"Authorization": {"Bearer " + os.Getenv("API_TOKEN")}},
	})
	if err != nil {
		panic(err)
	}
	defer c.CloseNow()

	if err := wsjson.Write(ctx, c, map[string]any{"op": "subscribe", "id": "s1", "topics": []string{"tickets"}}); err != nil {
		panic(err)
	}
	for {
		var f struct {
			Type    string          `json:"type"`
			Event   string          `json:"event"`
			EventID string          `json:"event_id"`
			Data    json.RawMessage `json:"data"`
		}
		if err := wsjson.Read(ctx, c, &f); err != nil {
			fmt.Println("closed:", websocket.CloseStatus(err), err)
			return
		}
		if f.Type == "event" {
			fmt.Println(f.EventID, f.Event, string(f.Data))
		}
	}
}
```

---

## 12. Webhooks

Webhooks push ticket events to your systems as HTTPS `POST` requests. Create them under **Admin → Webhooks** or with `POST /api/v1/admin/webhooks` (see [Admin guide → Webhooks](#711-webhooks-channels--webhooks-admin)).

### 12.1 Request format

```http
POST /your/endpoint HTTP/1.1
Content-Type: application/json
User-Agent: SimpleTickets-Webhook/1
X-SimpleTickets-Event: ticket.escalated
X-SimpleTickets-Delivery: 0192f3e1-…          (new for every attempt)
X-SimpleTickets-Signature: t=1760954400,v1=5f2c…

{
  "id": "0192f3d0-…",                         // event id: the same on every retry, so deduplicate on it
  "type": "ticket.escalated",
  "created_at": "2026-10-20T10:00:00Z",
  "data": {
    "ticket":  { "id": "…", "number": 1042, "subject": "…", "status": "open", "priority": "high", "tier": 2, … },
    "actor":   { "id": "…", "name": "Mia" },
    "changes": { "from_tier": 1, "to_tier": 2, … },
    "message": { … }                           // for message events
  }
}
```

Delivery rules:
- **At least once.** Delivery is at-least-once, and events can arrive out of order across tickets. Use `id` to drop duplicates.
- **Success** is any `2xx` response within 10 seconds. Redirects are not followed and count as failures.
- **Retries:** up to 8 attempts in total, with exponential backoff from about 30 s up to 6 h.
- **Automatic disabling:** a `410 Gone` disables the webhook at once. 20 consecutive failures disable it and email the admins.
- **Internal data:** internal notes (`note.created`) and internal details such as escalation reasons are sent only when *Include internal notes and reasons* is on.
- **Test:** the **Test** button sends an event of type `ping`.

### 12.2 Event types

| Event | When |
|---|---|
| `ticket.created` | A ticket was created (portal, agent, API or email) |
| `ticket.status_changed` | The status changed |
| `ticket.priority_changed` | The priority changed |
| `ticket.assigned` | The assignee changed |
| `ticket.team_changed` | The ticket moved to another team |
| `ticket.escalated` / `ticket.deescalated` | The tier changed |
| `ticket.category_changed` | The category changed |
| `ticket.tags_changed` | The tags changed |
| `ticket.fields_changed` | Custom field values changed |
| `message.created` | A public message was added (by staff, the customer or email) |
| `note.created` | An internal note was added (only with *Include internal notes and reasons*) |
| `sla.warning` / `sla.breached` | An SLA target is at 80 % / 100 % |
| `ticket.csat` | The customer rated the ticket |
| `ticket.followup_created` | A reply to a closed ticket created a follow-up |
| `ticket.merged` | The ticket was merged into another |
| `ticket.split` | Messages were split into a child ticket |
| `ticket.linked` / `ticket.unlinked` | A link was added or removed |
| `ticket.children_resolved` | The last open child of a parent was resolved |

Changes made by [automation rules and assignment strategies](#716-automation-and-assignment-support-setup-supervisor) arrive as these ordinary events, with `actor: null`. The staff-only `ticket.automation_applied` timeline event is not sent to webhooks.

### 12.3 Verifying signatures

`X-SimpleTickets-Signature` has the form `t=<unix seconds>,v1=<hex>`, where the hex value is `HMAC-SHA256(secret, t + "." + raw_body)`. To verify a request:
1. Compute the HMAC over the **raw** request body, exactly as received, before any JSON parsing.
2. Compare it with `v1` in constant time.
3. Reject the request if `t` is more than 300 seconds away from your clock.

**Python:**

<!-- doctest:verify-python -->
```python
import hashlib, hmac, time

def verify(secret: str, header: str, raw_body: bytes, tolerance: int = 300) -> bool:
    parts = dict(p.split("=", 1) for p in header.split(","))
    t, sig = parts.get("t"), parts.get("v1")
    if not t or not sig or abs(time.time() - int(t)) > tolerance:
        return False
    mac = hmac.new(secret.encode(), f"{t}.".encode() + raw_body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(mac, sig)
```

**Node.js:**

<!-- doctest:verify-node -->
```js
import crypto from 'node:crypto';

export function verify(secret, header, rawBody /* Buffer */, tolerance = 300) {
  const parts = Object.fromEntries(header.split(',').map((p) => p.split('=', 2)));
  const t = Number(parts.t);
  if (!parts.v1 || !t || Math.abs(Date.now() / 1000 - t) > tolerance) return false;
  const mac = crypto.createHmac('sha256', secret).update(`${parts.t}.`).update(rawBody).digest('hex');
  const a = Buffer.from(mac), b = Buffer.from(parts.v1);
  return a.length === b.length && crypto.timingSafeEqual(a, b);
}
// Express: app.post('/hook', express.raw({ type: 'application/json' }), (req, res) => { verify(secret, req.get('X-SimpleTickets-Signature'), req.body) … })
```

**Go:**

<!-- doctest:verify-go -->
```go
func Verify(secret, header string, body []byte, now time.Time) bool {
	var ts, sig string
	for _, p := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(p, "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			sig = v
		}
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || sig == "" || math.Abs(float64(now.Unix()-t)) > 300 {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(sig))
}
```

The same scheme, with the header `X-SimpleTickets-Inbound-Signature`, protects the inbound email webhook. See [Inbound email](#64-inbound-email).

### 12.4 Target restrictions

To prevent server-side request forgery, webhook URLs must use `https://` and must not resolve to:
- loopback, private, link-local, multicast or unspecified addresses;
- CGNAT (`100.64.0.0/10`) or other reserved ranges;
- IPv4 addresses embedded in IPv6.

The address is checked when the webhook is saved and again on every connection. For testing against a local receiver, set `ST_WEBHOOK_ALLOW_PRIVATE=true` and/or `ST_WEBHOOK_ALLOW_INSECURE=true`. Don't do this in production.

---

## 13. Operations

### 13.1 Backup and restore

**What to back up:**
- the **database**;
- the **files** (attachments, branding, KB images, raw inbound mail);
- **`ST_SECRET_KEY`**. Without it, the encrypted SMTP, IMAP and webhook secrets and the users' authenticator-app secrets in a restored database can't be read.

Data export files (blob prefix `privacy/`) are deliberately left out of backups: they are short-lived, and users can request a new one. Backups taken before an [erasure](#718-privacy-and-gdpr-people--privacy-admin) still contain the erased person's data, so rotate them out on your retention schedule.

**SQLite (default): online backup.** It is safe while the server runs:

```sh
docker compose -f deploy/compose.yml exec app /simpletickets backup
# backup written to /data/backups/simpletickets-20261021-093000
docker compose -f deploy/compose.yml exec app /simpletickets backup --out /data/backups
```

A backup is a directory named `simpletickets-YYYYMMDD-HHMMSS` (UTC) containing:

| File | Contents |
|---|---|
| `simpletickets.db` | A transactionally consistent copy of the SQLite database (`VACUUM INTO`) |
| `blobs.tar.gz` | Local files: `attachments/`, `branding/`, `kb/`, `inbound/` |
| `manifest.json` | Creation time, engine, schema version, and the SHA-256 of every file |

Copy the backup directory off the host, for example with `docker cp` or a backup tool that reads the volume.

**PostgreSQL.** `simpletickets backup` writes only the files and the manifest, and prints the matching dump command:

```sh
docker compose exec -T postgres pg_dump -U <user> -Fc <db> > simpletickets.pgdump
# restore: pg_restore -U <user> -d <db> --clean simpletickets.pgdump
```

**S3 storage.** Files are not included in the backup ("Attachments live in S3: use bucket versioning/lifecycle for them."). Turn on bucket versioning.

**Restoring (SQLite).** The restore runs **offline**:

```sh
docker compose -f deploy/compose.yml stop app
docker compose -f deploy/compose.yml run --rm app restore --from /data/backups/simpletickets-20261021-093000 --force
docker compose -f deploy/compose.yml start app
```

What `restore` does:
- verifies every checksum;
- writes the database atomically to `$ST_DATA_DIR/simpletickets.db`;
- unpacks the files when local storage is used.

Things to know:
- **`--force`** is required if a database file already exists.
- **The server must be stopped.** Restore refuses to run while a server holds the data-directory lock or the database's single-instance lock. `migrate-engine` refuses the same way.
- **The target path follows the configuration.** Restore writes to the SQLite file of `ST_DATABASE_URL` (default `$ST_DATA_DIR/simpletickets.db`). With `--force`, the replaced database is kept next to it as `simpletickets.db.pre-restore`.
- **Checks first.** Restore refuses a backup written by a newer release and runs an integrity check on the backed-up database before swapping it in.
- **Files.** Files are unpacked into the local storage directory: `$ST_DATA_DIR`, or the path of a `file:///…` `ST_BLOB_URL`. With S3 storage they are not touched.
- **PostgreSQL:** use `pg_restore` for the database, then `simpletickets restore` only if you have local files to restore.

**In-flight queue items after a restore.** Successful jobs are kept only in memory. After a restore, the dispatcher continues from the event cursor saved in the restored database. As a result:
- **Repeats:** emails and webhooks for events after the backup point may be **sent again**.
  - Emails use deterministic `Message-ID`s.
  - Webhooks carry stable event `id`s.
  - Receivers can therefore spot the duplicates.
- **Lost:** sign-in emails (verification, password reset, magic link, invitation) that were queued when the backup was taken are lost. Users simply request them again.

**Restore drill.** Test a restore on a fresh host now and then. Compare ticket and message counts, and open a few attachments. `scripts/restore-drill.sh --engine sqlite|postgres` automates it with Docker: it seeds data, takes the backup above (plus `pg_dump` on PostgreSQL), restores into new volumes on new containers, and compares every table's row count, every stored file's SHA-256 and the attachments served by the restored instance. CI runs it nightly on both engines.

**Continuous replication with Litestream (optional, SQLite only).** Backups are taken at a point in time. To lose at most a second or two of data when a host or its volume dies, add the [Litestream](https://litestream.io) overlay. It streams the SQLite WAL to S3-compatible storage (AWS S3, Cloudflare R2, Backblaze B2, MinIO, Garage). It is opt-in and not part of `deploy/compose.yml`, so the default install stays one container. With PostgreSQL, use your Postgres point-in-time recovery instead.

```sh
cp deploy/litestream.env.example deploy/litestream.env   # bucket, path, endpoint, region, keys
docker compose -f deploy/compose.yml -f deploy/compose.litestream.yml up -d
docker compose -f deploy/compose.yml -f deploy/compose.litestream.yml logs litestream   # "snapshot complete", "compaction complete"
```

The overlay adds two containers:

| Service | What it does |
|---|---|
| `litestream-restore` | Runs once, before the app, on every `up`. If `/data/simpletickets.db` is missing (a new host or a lost volume), it restores the latest replica. Otherwise it does nothing (`-if-db-not-exists -if-replica-exists`) |
| `litestream` | The replication sidecar. It runs as uid 65532 with a read-only root, `mem_limit: 128m` and `cpus: 0.5`, and ships the WAL every second |

**Restoring after a disaster** needs no extra command. Start the same three files on a host with an empty volume: the restore step pulls the replica, then the app boots on it. To restore to an earlier point in time instead of the latest state, start from an empty volume and run `docker compose -f deploy/compose.yml -f deploy/compose.litestream.yml run --rm litestream-restore "litestream restore -config /etc/litestream.yml -timestamp 2026-10-21T09:00:00Z /data/simpletickets.db && chown -R 65532:65532 /data"` before `up` (the drill below covers only the latest-state restore).

Things to know:
- **Attachments are not replicated.** Litestream copies the database only. Keep files safe with `simpletickets backup` (`blobs.tar.gz`), or use S3 storage (`ST_BLOB_URL`) with bucket versioning.
- **Keep `ST_SECRET_KEY`.** A restored database still needs the same key to read the encrypted SMTP, IMAP and webhook secrets.
- **Checkpoints.** SimpleTickets opens SQLite in WAL mode with a single writer connection, the default auto-checkpoint and a `PASSIVE` checkpoint every 5 minutes. A passive checkpoint never blocks or truncates the WAL past an open reader, and Litestream holds its own read transaction to pace checkpoints, so no app setting changes. Don't run a second writer on the file, and don't run `wal_checkpoint(TRUNCATE)` by hand.
- **One replica target per database.** Never point two instances at the same bucket path.
- **Drill:** `scripts/litestream-drill.sh` uses a throwaway MinIO. It creates 100 tickets, SIGKILLs every container, deletes the data volume, starts again on an empty volume, and checks that the ticket count and all 100 ticket ids match.

### 13.2 CLI reference

The binary is `/simpletickets` in the container. Run commands with `docker compose -f deploy/compose.yml exec app /simpletickets <command>`. Offline commands, such as `restore` and `migrate-engine`, run in a one-off container while the app is stopped. Run `simpletickets help` for the list.

| Command | Flags | Description |
|---|---|---|
| `serve` | — | Run the server. This is the default when no command is given |
| `migrate` | `status` | Apply pending migrations and print `migrated: engine=… schema=…`. `migrate status` prints `engine=… schema=… head=… pending=…`. Migrations only go forward |
| `healthcheck` | — | Calls `/readyz` on `ST_LISTEN_ADDR` and exits 0 on HTTP 200. Used by the container `HEALTHCHECK` |
| `version` | — | Prints `simpletickets <version> <commit>` |
| `config` | `check` (default) · `docs` | Validate and print the configuration (secrets masked); or print the core variable table |
| `backup` | `--out DIR` (default `$ST_DATA_DIR/backups`) | Online backup (see above) |
| `restore` | `--from DIR` (required), `--force` | Offline restore |
| `create-admin` | `--email` (required), `--name` (default `Administrator`), `--password-stdin` | Create a break-glass admin (tier 3). Prints a generated password unless one is read from stdin |
| `reset-password` | `--email` (required), `--password-stdin` | Set a new local password and revoke all of the user's sessions |
| `reset-mfa` | `--email` (required) | Remove the user's two-step verification (authenticator app, email codes, recovery codes, sign-ins in progress), audited with `via: cli`. Sessions stay. It takes no instance lock, so it works while the app runs (`exec`) and with the app stopped (`docker compose -f deploy/compose.yml run --rm app reset-mfa --email …`) |
| `migrate-engine` | `--from URL`, `--to URL` (both required) | Copy all data between SQLite (`sqlite:///path`) and PostgreSQL (`postgres://…`). Offline; verified per table |
| `blob-migrate` | `--to URL` (required; `s3://…` or `file:///path`) | Copy every stored file to another backend, checking SHA-256 |
| `reindex` | — | Rebuild the ticket search index |
| `rotate-secrets` | — | Re-encrypt every stored secret (SMTP password, webhook and inbound-mailbox secrets, authenticator-app secrets) with `ST_SECRET_KEY`. Needs the old key in `ST_SECRET_KEY_PREVIOUS`; all or nothing |
| `seed` | `--tickets N` (default 50) | Fill an **empty** database with demo users and tickets. Demo accounts have no password; use `reset-password` to sign in as one |
| `seed --demo` | `--tickets N` (default 60), `--smtp-mailpit`, `--force`, `--password P` (default `demo-password-123`) | Fill every part of the app with demo data (see [Demo data](#demo-data)). Refuses a database that is not empty unless an earlier `seed --demo` filled it **and** `--force` is given |
| `seed-kb` | — | Add demo knowledge-base categories and articles (EN and IT) |

Exit codes: `0` success, `1` error, `2` configuration error or unknown command.

### 13.3 Upgrades and migrations

1. Read the release notes.
2. **Take a backup**, and for PostgreSQL also a `pg_dump`.
3. Pull and start the new image. Images are published as `ghcr.io/radiobros/simpletickets:<version>`, `:<major>.<minor>` and `:latest`, for amd64 and arm64:

   ```sh
   # in deploy/.env: ST_VERSION=1.2.3 (or 1.2 to follow patch releases); the default is latest
   docker compose -f deploy/compose.yml pull
   docker compose -f deploy/compose.yml up -d
   ```

4. Pending migrations run at startup (`ST_MIGRATE_ON_BOOT=true`, the default).
   - Each migration file runs in its own transaction.
   - On SQLite, `serve` first writes an automatic copy, `$ST_DATA_DIR/backups/pre-migrate-<schema>-<time>.db`, and keeps the newest three. PostgreSQL has no automatic copy (the image has no `pg_dump`), so take the dump in step 2.
   - Before migrating, `serve` checks the database (`PRAGMA quick_check` on SQLite) and refuses to start on corruption. The result is shown under **Admin → System**.
   - With `ST_MIGRATE_ON_BOOT=false`, run `simpletickets migrate` yourself. `/readyz` returns 503 until the schema matches the binary.
5. Check `docker compose -f deploy/compose.yml ps` (healthy) and **Admin → System** (version and schema).

**Rolling back:** start the **previous image** and **restore the pre-upgrade backup**.
- Migrations only go forward; there are no automatic down-migrations.
- An older binary refuses to start against a newer schema, with the message `database schema version X is newer than this binary (Y); refusing to start`. A mistaken rollback therefore can't damage your data.

**Upgrade drill.** `scripts/upgrade-drill.sh --engine sqlite|postgres --from <previous image> --next-migration` rehearses the whole procedure on throwaway volumes: the previous image writes data, the new image (this checkout plus one extra migration) migrates it on boot with every row kept and a schema identical to a fresh install, the previous image then refuses the upgraded database, and the pre-upgrade backup rolls it back.

On Kubernetes, the `Recreate` strategy stops the old pod before the new one starts, which is required: only one instance may run at a time.

### 13.4 Monitoring

| Endpoint | Auth | Answers |
|---|---|---|
| `GET /healthz` | none | `ok` while the process runs (liveness) |
| `GET /readyz` | none | `200 ready <engine>` when the database answers, the schema matches, storage answers and the queue isn't stalled; otherwise `503 not ready` (the reason is logged at WARN) |
| `GET /metrics` | `Authorization: Bearer $ST_METRICS_TOKEN` | Prometheus text format. Returns 404 while `ST_METRICS_TOKEN` is unset |
| `GET /api/v1/admin/system` | admin | Version, uptime, database engine/size/schema, disk, WebSocket connections, queue, SMTP and OIDC status |
| `GET /api/v1/admin/queue` | admin | In-memory queue depths and worker counts, and retrying/failed job counts |

Metrics:

| Metric | Meaning |
|---|---|
| `st_http_requests_total{method,route,code}` | Requests per route and status class (`2xx`, `4xx`, …) |
| `st_http_request_duration_seconds` | Request latency histogram |
| `st_ws_connections` | Open WebSocket connections |
| `st_queue_dispatched_total` | Events dispatched by the queue |
| `st_queue_deferred_writes_total` | Jobs written to the database for retry |
| `st_queue_dead_total` | Jobs that failed permanently. **Alert when this grows** |
| `st_go_memory_bytes`, `st_go_goroutines`, `st_go_cpu_seconds_total`, `st_go_gc_cpu_seconds_total` | Runtime health |

Prometheus scrape example:

```yaml
scrape_configs:
  - job_name: simpletickets
    scheme: https
    authorization: { credentials: "<ST_METRICS_TOKEN>" }
    static_configs: [{ targets: ["help.example.com"] }]
```

**Logs** go to stdout as JSON by default (`ST_LOG_FORMAT`, `ST_LOG_LEVEL`). Every request has an `X-Request-ID`. Lines worth alerting on:
- `readyz failed`;
- `break-glass admin login`;
- SMTP and IMAP errors;
- webhook auto-disable.

### 13.5 Footprint tuning

The defaults are tuned for a 1 vCPU / 512 MB host and also fit in 256 MB. Normally you don't need to change anything.

| Knob | Default | When to change |
|---|---|---|
| Container memory limit | 256 MB in the examples | Raise it for large installs. `GOMEMLIMIT` follows automatically at 75 %, and the log line `memory limit derived from cgroup` shows the value |
| `GOMEMLIMIT` | derived | Set it explicitly only without a cgroup limit. Don't go below about 80 MiB |
| `GOGC` | 100 | Higher values use less CPU and more memory |
| `ST_DB_READERS` | 2 | More SQLite readers for many concurrent agents on more cores |
| `ST_DB_POOL` | 5 | PostgreSQL connections |
| `ST_QUEUE_CAPACITY` | 1024 | The in-memory buffer per queue. Overflow is written to the database, never dropped |
| `ST_WS_MAX_CONNS`, `ST_WS_MAX_CONNS_PER_USER` | 1000 / 5 | Caps on WebSocket memory |
| `ST_NOTIFY_COALESCE_SECONDS` | 120 | Fewer "customer replied" emails |

To measure your own build, run `scripts/footprint.sh [--engine sqlite|postgres]`. It reports image size, idle RSS, idle CPU and cold start against the targets and hard limits; `--help` lists its options.

How the app keeps its footprint small:
- **Idle CPU is near zero.** There are no polling loops shorter than one second, and work is event-driven.
- **Writes are batched.** Chatty writes such as "last seen" times are flushed every 30 seconds.
- **SQLite housekeeping.** The SQLite write-ahead log is checkpointed every 5 minutes.

### 13.6 Troubleshooting

| Symptom | Likely cause and fix |
|---|---|
| The container exits at start with a list of errors | A configuration error; all problems are listed together (exit code 2). Run `simpletickets config check` |
| `another simpletickets instance is using /data (lock held)` | A second instance or a stale container uses the same volume. Stop it. Never run two replicas |
| `database schema version X is newer than this binary` | You started an older image against a newer database. Use the newer image, or restore the pre-upgrade backup |
| No setup token in the logs | An admin already exists, or `ST_BOOTSTRAP_ADMIN_EMAIL` is set. Use `create-admin` or `reset-password` |
| Every save fails with 403 `csrf` | `ST_PUBLIC_URL` doesn't match the address in the browser (scheme, host or port), or the proxy rewrites `Origin` |
| Agent panel keeps showing "Connection lost — reconnecting…" | The proxy doesn't forward WebSocket upgrades, or its timeout is under 60 s. See [Reverse proxies](#26-reverse-proxies-and-tls). Also check `ST_PUBLIC_URL`, because the WebSocket checks `Origin` |
| Staff can't sign in with a password | `ST_AUTH_STAFF_LOCAL` is `false` (the default). Enable it, use SSO, or sign in as a break-glass admin |
| SSO login: "Your account has no support role…" | None of the user's groups is in `ST_OIDC_STAFF_ROLE_MAP`, or the group claim is missing. Check the claim name (`ST_OIDC_STAFF_ROLE_CLAIM`) and the provider's scope mapping |
| SSO login refused for an existing user | An account with the same email exists. Set `ST_OIDC_LINK_BY_EMAIL=true` (the provider must report the email as verified) |
| A user who once signed in with SSO gets "invalid email or password" with the right password | The account is linked to the identity provider and can sign in only through SSO; linking removed its password. Use the SSO button. Only break-glass admins keep a password ([5.7](#57-two-step-verification-and-sso)) |
| "Staff sign-on: Provider unreachable" in Admin → System | The issuer URL is wrong or unreachable from the container. Discovery keeps retrying in the background |
| The SSO button answers `503`; the log says `oidc discovery failed` | The app container cannot reach `ST_OIDC_STAFF_ISSUER` (see [Authentik step by step](#52-authentik-step-by-step): the issuer must be reachable from the browser and from the container at the same URL) |
| Logs and audit entries show the proxy's IP | Set `ST_TRUSTED_PROXIES` to the proxy's network |
| Cookies not `Secure` / login lost after redirect | `ST_PUBLIC_URL` isn't `https://`, or the proxy doesn't send `X-Forwarded-Proto` from a trusted address |
| No emails | Check Admin → Email (status banner and test button) and Admin → Failed jobs. Check that the template isn't switched off |
| Inbound mail doesn't create tickets | Check Admin → Inbound email → Inbound log for the reason (`unknown_sender`, `rate_limited`, `staff_spoof`, …) and use **Retry** after fixing it |
| Webhook stopped firing | It was disabled after 20 failures or a `410`. Check *Deliveries*, fix the receiver, then *Re-enable* |
| Search misses older tickets | Run `simpletickets reindex` |
| An admin lost their authenticator app and recovery codes | Another admin resets it (**Reset two-step verification** on the user), or run `simpletickets reset-mfa --email …`. See [Two-step verification](#717-two-step-verification-system--support-settings-admin) |
| *Code by email* is greyed out, or sending a code answers `email-disabled` | SMTP is not configured. Configure it under Admin → Email; users who rely only on email codes need a recovery code or a reset meanwhile |
| Tickets are not assigned automatically | The team's *Automatic assignment* is *Manual*, or no member is eligible (inactive, not in the team, tier too low, or marked **Away**). See [Automation and assignment](#716-automation-and-assignment-support-setup-supervisor) |
| An automation rule doesn't fire | Use **Dry run** on the rule with the ticket number. Changes made by automation never trigger rules, and a time-based rule fires once per ticket until the ticket changes again |
| Browser notifications don't appear | Pop-ups need a SimpleTickets tab open in the background, HTTPS (or `localhost`), the browser's permission and the *Browser pop-up* box ticked for that type. Operating-system focus modes can hide them. See [features/notifications.md](features/notifications.md#browser-notifications) |
| OOM kills | Raise the container memory limit; don't set `GOMEMLIMIT` above the limit |

---

## 14. Security model

These are the rules SimpleTickets enforces. Most of them have dedicated tests named after them.

1. **Staff and customers are separate kinds of account.**
   - The kind is set at creation and can't change.
   - Customers are served by separate handlers and queries.
   - A customer calling a staff or admin URL gets 404.
2. **Customers never see internal notes.** This holds everywhere:
   - The portal queries are written so they can't select internal messages.
   - Internal notes and internal events are never put into customer emails, customer WebSocket frames or webhooks without *Include internal notes and reasons*.
3. **Customers only see their own tickets.** That means tickets where they are the requester or a watcher, plus their organization's tickets for organization managers.
4. **Agents only see what they work on.** That means tickets assigned to them, tickets in their teams up to their tier, tickets they watch, and triage when it is allowed. Supervisors and admins see everything.
5. **Hidden means "not found".** Reads outside your visibility return 404. Forbidden actions on visible tickets return 403.
6. **Visibility is part of every query.** Lists, counts, search, single-ticket reads and WebSocket subscriptions all use the same visibility rule, built into the database query. Live events are filtered per subscriber against an in-memory snapshot of the ticket.
7. **Hidden custom fields stay hidden.** Fields not marked *Visible to customers* never reach customers. Customers can only write fields marked *Editable by customers*.
8. **Knowledge-base visibility is enforced.**
   - Anonymous visitors see `public` articles.
   - Customers see `public` and `customers` articles.
   - Staff see everything, including drafts.
   - Search follows the same rule.
9. **Tokens never exceed their owner.** A token's permissions are the owner's current permissions intersected with its scopes, checked on each request. Only token hashes are stored.
10. **Disabled means out.** Disabling a user revokes their sessions, rejects their tokens and closes their WebSockets. The WebSocket hub also re-checks every connection every 60 s.
11. **SSO roles are re-synced on every login.** Removing someone from an identity-provider group takes effect at their next login. An unmapped user is disabled.
12. **Break-glass access is limited and logged.** Only flagged admins can use a password when staff password login is off. Each such login goes through two-step verification when the account has it or the policy requires it, and is logged and audited.
13. **Browser requests are checked for CSRF.** Cookie-authenticated writes need the `X-CSRF-Token` and a same-origin `Origin`/`Referer`. WebSocket upgrades from browsers must come from `ST_PUBLIC_URL`'s origin.
14. **Secrets are encrypted at rest.** SMTP and IMAP passwords, IMAP OAuth refresh tokens, webhook secrets and authenticator-app (TOTP) secrets are stored with AES-256-GCM under `ST_SECRET_KEY`, and the API never returns them after setup. Passwords are hashed with Argon2; recovery codes and sign-in challenges are stored as SHA-256 hashes.
15. **The setup wizard needs a token.** A fresh instance can only be claimed with the token from the server log.
16. **Webhooks can't reach internal addresses.** Webhook targets must use HTTPS and public addresses, checked on every connection. Every request is signed.
17. **Email can't impersonate staff.** Staff replies by email need DKIM or aligned SPF verified by a trusted server. Automatic mail never creates tickets, and senders are rate-limited.
18. **Uploads are checked.** Content types are sniffed on upload and downloads are served with `nosniff`. SVG brand assets are sanitised. Markdown is rendered with sanitising.
19. **History can't be rewritten.** The ticket event log and the audit log are append-only, enforced by database triggers.
20. **Hardened by default.**
    - The app runs as a non-root user in a distroless image with a read-only root filesystem and no shell.
    - It sends security headers: Content-Security-Policy, `X-Frame-Options: DENY`, `nosniff`, a strict referrer policy, and HSTS on HTTPS.
21. **A second factor means no session before the second step.** For a local account with two-step verification, or covered by the policy, a correct password yields only a short-lived challenge (5 minutes, 5 attempts). Wrong codes count toward the sign-in lockout, and an authenticator code is accepted only once.
22. **Automation can't loop or escalate privileges.** Rule-made changes never trigger rules, at most 10 actions run per event, and a named assignee must be allowed to own the ticket. Macros run with the permissions of the agent who applies them.
23. **Self-service data exports contain only what the person may see.** They never include internal notes, staff-only fields, other customers' messages or the identities of the staff who acted on the account.
24. **SSO accounts can't fall back to a password.** An account with an SSO identity, or managed by the identity provider, signs in only through the provider, so the provider's second factor, group mapping and blocking always apply. Its password sign-in fails like a wrong password (same answer, timing and lockout count). Sign-in, invitation and reset links and password changes are refused for it. Linking an account removes its local password and signs out its password sessions. Break-glass admins are the only exception, and their password sign-ins go through two-step verification. Every local way in (password, sign-in link, email verification, invitation) passes the same two-step check, password reset never signs in, and the setup wizard only creates a new account.
25. **Agents only see their projects (I-11).** An agent sees no ticket outside the projects they are a member of, whatever the team, tier, assignee or watcher rules say, in lists, counts, search, reports, WebSocket frames, notifications and MCP. Owners must work in the ticket's project, and leaving a project unassigns its tickets at once. Project admins configure only their projects.

To report a vulnerability, follow [SECURITY.md](../SECURITY.md).

---

## 15. Development and contributing

### 15.1 Repository layout

```
cmd/simpletickets/     CLI entry point: serve, migrate, backup, restore, …
internal/
  app/                 wiring of all modules (app.Build), readiness, memory limit
  server/              router, middleware (auth, CSRF, gzip, security headers), SPA, health, metrics, idempotency
  config/              ST_* parsing and validation
  domain/              enums, Principal, scopes
  store/               SQLite + PostgreSQL access, migrations for both engines, backup/restore, search
  authn/               sessions, local login, two-step verification, OIDC, PATs, WebSocket tickets, setup wizard, CLI admin commands
  authz/               permission checks (Can) and visibility predicates
  directory/           users, organizations, teams, categories, staff admin
  tickets/             ticket service, lifecycle, escalation, SLA, custom fields, links, merge/split, search, reports,
                       assignment strategies, automation rule execution, macros
  automation/          automation rule model: validation and condition evaluation (pure)
  privacy/             GDPR tools: data export, erasure, retention policy, consent records
  kb/                  knowledge base
  notify/              SMTP sender, email templates (5 languages), recipients, in-app notification centre
  mailin/              inbound email: IMAP IDLE, MIME webhook, parser, threading
  webhooks/            webhook CRUD, signing, delivery
  queue/               in-process queue, dispatcher, deferred jobs, scheduler, write-behind
  events/              event types and payloads
  realtime/            WebSocket hub
  settings/            typed settings with env overrides and locks
  crypto/              AES-256-GCM secret box
  blob/                local and S3 file storage
  problems/            RFC 9457 errors
  apispec/             test helpers that check routes and responses against api/openapi.yaml
api/openapi.yaml       the API contract (OpenAPI 3.1)
web/                   Vue 3 + Vuetify SPA (portal, agent panel, setup, KB), embedded into the binary
deploy/                compose files, Authentik blueprint, proxy examples, Kubernetes manifests, Helm chart
docs/                  this manual, configuration reference, feature guides (docs/features), internals
```

The contributor contract (package map, wiring, events, migrations, testing rules) is [docs/dev/internals.md](dev/internals.md).

### 15.2 Running locally

You need Go 1.27, Node.js 24 with corepack (the repo uses pnpm 11), and optionally Docker.

The shortest path from a clean clone to a signed-in agent panel (6 commands) is [docs/dev/getting-started.md](dev/getting-started.md).

```sh
make dev        # Go API on 127.0.0.1:8080 (ST_DEV=true, rebuilt and restarted on change) + Vite on :5173
                # open http://localhost:5173/agent; data lives in data/dev
make seed       # demo users and tickets (TICKETS=50); demo accounts have no password, use reset-password
make dev-deps   # optional: Authentik (SSO) + Mailpit (email) in Docker
make help       # every target
```

What `make dev` sets up:
- **Backend:** runs with `ST_DEV=true`, which generates a secret into the data directory, plus `ST_PUBLIC_URL=http://localhost:5173`, `ST_AUTH_STAFF_LOCAL=true` and `ST_INSECURE_COOKIES=true`.
- **First start:** follow the `setup token` line to `/setup`.
- **Frontend:** the Vite dev server proxies `/api` (including the WebSocket), `/branding`, `/kb/sitemap.xml`, `/healthz` and `/readyz` to the backend. Set `ST_DEV_BACKEND` to point it elsewhere.

**The whole stack in containers.** This runs the app built from source, Authentik 2026.8.4 with the SimpleTickets blueprint and demo users, and Mailpit (a mail catcher with a web UI):

```sh
docker compose -f deploy/compose.yml -f deploy/compose.dev.yml up -d --build
# app http://localhost:8080 · Authentik http://authentik.localhost:9000 · Mailpit http://localhost:8025
```

The dev credentials are in `deploy/.env.dev`. They are public, so never reuse them.

**Production build from source:**

```sh
make build      # pnpm build into web/dist (embedded via web/embed.go), then a static bin/simpletickets
make image      # container image simpletickets:local (multi-stage Dockerfile, distroless, non-root)
```

The `Dockerfile` pins its base images by digest and cross-compiles, so `make image-multiarch` builds amd64 and arm64 without emulating Go.

<a id="demo-data"></a>
**Demo data.** `simpletickets seed --demo` fills a fresh installation so that every feature shows real content: 3 organizations, 12 staff accounts (admin, supervisor, an L1/L2/L3 agent for each of the 3 teams and one IdP-managed agent), 12 customers (requesters and an org manager per organization, one pending and one disabled), teams with business calendars (24×7 and business hours with holidays), categories, custom fields of every type, SLA policies, about 60 tickets spread over the last two months (every status, priority, channel and tier, escalations, SLA warnings and breaches, CSAT, merge, split, links, follow-up, attachments, @mentions), canned responses, saved views, a multilingual knowledge base (EN, IT, DE, FR, ES) and the settings: branding ("SimpleTickets Demo"), senders and triage mailbox, a disabled webhook (`http://localhost:9/demo`, so nothing is ever sent), an inactive inbound mailbox with a few log entries, and an admin API token (printed once). It also creates three **projects**: *Web App* `WEB` (the renamed Default project, portal-visible with its own title and introduction), *Mobile* `MOB` (portal-visible, with an SLA override, a project rule, macro, canned response, KB category and custom field; `freya.fields@demo.test` is its project admin and `mona.mobile@demo.test` works in it only) and the staff-only *Internal IT* `IT`, with tickets in all three. Globex can open tickets in `WEB` only. The printout lists each project with sample references.

It also seeds the D-19 features: 8 [automation rules](#716-automation-and-assignment-support-setup-supervisor) (5 on, 3 off, every trigger) with a few rule runs, 8 macros (4 shared, 4 personal) and an assignment strategy per team (round robin, least busy, manual) with one agent marked away; one agent enrolled in [two-step verification](#717-two-step-verification-system--support-settings-admin) (`tomas.tran@demo.test`; the summary prints his TOTP secret, `otpauth://` URI and recovery codes once, and `security.mfa_required` stays `none`); 5 [privacy requests](#718-privacy-and-gdpr-people--privacy-admin) (3 ready exports, a pending and a rejected erasure), consent records and non-destructive retention settings; and several hundred [notifications](#719-notification-centre) for staff and customers, part of them unread.

```sh
make demo                                                            # dev database; --force wipes an earlier demo
docker compose -f deploy/compose.yml exec app /simpletickets seed --demo     # a running container (the `simpletickets seed --demo` command)
simpletickets seed --demo --smtp-mailpit                             # email → Mailpit on localhost:1025 (otherwise email stays disabled)
simpletickets seed --demo --password 'my-demo-pass-42' --tickets 120 # one password for every account, more tickets
```

- **Every account has the same password** (`demo-password-123` unless `--password`), printed in a summary table with the entity counts, the portal and agent URLs and the API token. Local staff sign-in needs `ST_AUTH_STAFF_LOCAL=true` (the admin, a break-glass account, always works). The IdP-managed agent has no password.
- **Real service layer.** The seeder builds the app like `serve` does and drives it in-process, signed in as the demo users, with a simulated clock. Events, timelines, SLA due dates, snapshots, the search index and the audit log are therefore the production ones. It never starts the queue and opens no network connection; the dispatcher cursor is moved past the seeded events, so a server started later sends no mail and no webhooks for them. Only data without an API (webhook delivery rows, inbound log rows, KB counters, customer passwords, the IdP flag, consent records) is written directly. In-app notifications, which only the queue consumer writes, are produced by replaying each seeded event through the notify module's in-app path (no email).
- **Safe to repeat.** The seed is deterministic (fixed random seed; timestamps are relative to "now"). A database that already holds users is refused unless it was filled by `seed --demo` itself *and* `--force` is given; `--force` then wipes all data and seeds again. At least the 32 scripted tickets are always created; `--tickets` adds generated ones.
- **Restart the server afterwards.** A running server caches settings, teams and categories in memory and does not see rows written by another process.

### 15.3 Tests

```sh
make test          # go test -race on SQLite AND PostgreSQL 18 (throwaway container on :55432) + web unit tests
make test-sqlite   # SQLite only; the PostgreSQL half is skipped locally
make lint          # gofmt, go vet, golangci-lint, eslint, vue-tsc, SPDX headers, dependency gate
# or by hand against your own PostgreSQL:
ST_TEST_POSTGRES_URL='postgres://st:st@localhost:55432/st_test?sslmode=disable' go test ./...
```

- **Both engines.** Every test that touches the store runs on SQLite **and** PostgreSQL through `storetest.Engines`. Without `ST_TEST_POSTGRES_URL` the PostgreSQL half is skipped locally; in CI (`CI=true`) it fails instead.
  - Each test gets an isolated schema.
  - `TestSchemaParity` keeps the two migration sets identical.
- **Optional integration tests:**
  - `ST_TEST_S3_URL` runs the S3 tests against MinIO.
  - `ST_TEST_IMAP_URL` runs the IMAP test against Dovecot.
  - `deploy/compose.test.yml` starts both containers. It is for development and CI only.
- **API conformance.** Handler tests go through the real router, and responses are validated against `api/openapi.yaml`. Every route must exist in the spec.
- **Invariants.** Tests are named after the invariants they prove, e.g. `TestVisibility_L1CannotSeeL2Ticket` and `TestWS_CustomerNeverReceivesInternal`.

**Frontend:**

```sh
cd web
pnpm lint         # ESLint, including project rules (no inline validation rules, validate() before submit)
pnpm typecheck    # vue-tsc
pnpm test         # i18n completeness check (all 5 languages) + Vitest
pnpm build && pnpm budget   # initial JS per area: warn > 200 KB gzip, fail > 300 KB
```

**End-to-end (Playwright):**

```sh
make e2e           # builds the SPA, installs chromium, runs Playwright against a fresh binary
```

- Without `E2E_BASE_URL`, Playwright starts the Go server itself, on port `E2E_PORT` (default 18080) with a temporary SQLite database.
  - It signs in as `admin@e2e.test` / `e2e-admin-password-123`, created through `ST_BOOTSTRAP_ADMIN_*`.
  - It runs on desktop Chrome and a mobile profile.
- Set `E2E_BASE_URL` to test a running stack instead, such as a compose deployment.
- The setup-wizard test needs a fresh instance without a bootstrap admin. Provide `E2E_SETUP_BASE_URL` and `E2E_SETUP_TOKEN`.

### 15.4 Conventions

- **Lightweight is a requirement.**
  - Go 1.27, `CGO_ENABLED=0`.
  - At most 15 runtime Go dependencies, listed in `tools/deps.go`. Don't add modules.
  - No polling loop under one second, and bounded buffers everywhere.
- **The spec comes first.** Change `api/openapi.yaml` together with the handler. Tests fail on undocumented routes and on responses that don't match the spec.
- **Database:**
  - Every migration exists for **both** engines, with the same number and name.
  - Use `?` placeholders; dialect-specific SQL goes in a `dialect.go` next to the query.
  - Timestamps are UTC with microsecond precision, and ids are UUIDv7.
- **Errors** go through `internal/problems` (RFC 9457). Never call `http.Error`.
- **Ticket changes** write their event with `events.Write` inside the same transaction. Everything else (email, webhooks, WebSocket, search) reacts to the event.
- **Frontend:**
  - Every input with a requirement or format has validation rules from `web/src/constants/validations.ts`.
  - Every submit calls `validate()` first.
  - Every form is registered in `web/src/forms/registry.ts`.
  - Every string exists in all five languages.
- **Licence.** SimpleTickets is Copyright © 2026 RadioBros (https://radiobros.com/) and licensed under AGPL-3.0-only. Contributions are licensed under AGPL-3.0-only. Every `.go`, `.ts` and `.vue` file starts with `SPDX-License-Identifier: AGPL-3.0-only`; `scripts/spdx.sh --fix` adds it.
- **CI** (`.github/workflows/ci.yml`) runs:
  - Go tests on both engines and the web checks;
  - OpenAPI lint and route inventory, SPDX headers, the dependency gate, `.env.example` drift and Kubernetes validation;
  - end-to-end tests;
  - the image build with its size, Trivy and footprint gates, and a load test.

  `.github/workflows/release.yml` publishes multi-arch images to `ghcr.io/radiobros/simpletickets`.

How to propose changes, the pull-request checklist and the code of conduct are in [CONTRIBUTING.md](../CONTRIBUTING.md).

---

## 16. FAQ

**Can I run more than one replica for high availability?**
No. The queue, WebSocket hub and write-behind buffers live in the process, so SimpleTickets runs as exactly one instance, even with PostgreSQL. It scales vertically, and a single small instance handles dozens of agents. For availability, rely on fast restarts (cold start is about a second), health checks and backups.

**SQLite or PostgreSQL?**
SQLite, unless you have a reason to choose PostgreSQL: existing database operations, point-in-time recovery, or company policy. SQLite uses the least memory and has built-in online backups. You can switch later with `migrate-engine`.

**Do I need SSO?**
No. Without SSO:
- customers use passwords or magic links;
- staff use passwords if you set `ST_AUTH_STAFF_LOCAL=true`.

The admin created at setup is break-glass and can always sign in.

**Do I need email?**
No, but without SMTP there are no email notifications, password resets, magic links, self-registration or two-step codes by email. The notification bell and authenticator apps work without it. You can add SMTP later in **Admin → Email**.

**Does SimpleTickets support two-factor authentication?**
Yes, for local accounts: authenticator apps, codes by email and recovery codes, optionally required for staff or for everyone. SSO accounts use their identity provider's second factor. See [Two-step verification](#717-two-step-verification-system--support-settings-admin).

**Is SimpleTickets GDPR compliant?**
It gives you the tools: data exports, erasure, retention policies and consent records ([Privacy and GDPR](#718-privacy-and-gdpr-people--privacy-admin)). Whether your use is compliant depends on your processes, so involve your data protection officer.

**Can tickets be assigned automatically?**
Yes. Each team can use round robin or least busy, and automation rules can route, tag, reply and escalate. See [Automation and assignment](#716-automation-and-assignment-support-setup-supervisor).

**Can one person be both an agent and a customer?**
Not with one account. Staff and customers are separate kinds; use two accounts with different email addresses.

**Can I add more than three tiers, or custom roles?**
No. Three tiers and three staff roles are fixed, which keeps the permission rules simple and fully testable. You can rename the tiers.

**Which languages are supported?**
English, Italian, German, French and Spanish, for the UI, emails and the knowledge base. Choose the offered languages and the default under **Admin → Branding**.

**How do I remove the "Powered by SimpleTickets" link?**
Switch it off under **Admin → Branding → Legal links**. Under the AGPL, if you modify SimpleTickets and offer it as a network service, you must still make your source code available to its users.

**How do I move attachments to S3 later?**
Run `simpletickets blob-migrate --to 's3://…'`, set `ST_BLOB_URL` and the S3 credentials, and restart. See [S3 storage option](#24-s3-storage-option).

**I lost the admin password.**
Run `simpletickets reset-password --email you@example.com`. It prints a new password and signs you out everywhere.

**Where are the API docs?**
The specification is [`api/openapi.yaml`](../api/openapi.yaml), also served at `/api/v1/openapi.yaml`, and every instance has a reference page at `/api/docs`. Sections 10–12 of this manual cover the essentials.

**Why does an API call return 404 for a ticket that exists?**
You can't see it: it may be outside your team or tier, or belong to another customer. SimpleTickets doesn't reveal whether hidden objects exist.

**How do I get live updates in my integration without polling?**
Open a WebSocket with a personal access token and subscribe to `tickets`. See [WebSocket protocol](#11-websocket-protocol). For server-to-server notifications, use [webhooks](#12-webhooks).
