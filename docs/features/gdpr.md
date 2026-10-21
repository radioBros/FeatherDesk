# Privacy and GDPR tools

> Part of the [SimpleTickets manual](../DOCUMENTATION.md). The overview is in [Admin guide → Privacy and GDPR](../DOCUMENTATION.md#718-privacy-and-gdpr-people--privacy-admin) and the [Customer portal guide](../DOCUMENTATION.md#portal-my-data); this page has every detail.

SimpleTickets has tools that help you answer data protection requests and keep only the data you need:

- **Data export**: a person can download a copy of the personal data the help desk stores about them (a ZIP file).
- **Erasure**: a customer can ask for their account to be deleted, and an admin can erase any account directly.
- **Retention policies**: a daily job removes or anonymizes old closed tickets, raw inbound emails, old audit entries and expired
  export files. A dry run shows what it would do first.
- **Consent**: when you publish a privacy policy, registration asks people to accept it and records the acceptance.

> **These are tools, not legal advice.** They help you carry out decisions; they do not decide for you. Whether you must export,
> erase or keep a piece of data, how long you may keep tickets, and what your privacy policy must say depend on your situation and
> your jurisdiction (for example GDPR articles 15, 17 and 20, tax and commercial record-keeping rules, or a legal hold). Ask your
> data protection officer or a lawyer. Also read [What is not covered](#what-is-not-covered) before you rely on an erasure.

## For customers and staff: "My data"

**Customers** open **Profile → My data and privacy** (`/profile/data`). **Staff** open their **Profile** and choose the **My data**
button in the page header (`/agent/profile/data`).

- **Request a data export** starts an export. It takes a few seconds to a few minutes. You get an email when it is ready, and after
  **Refresh** the page shows a **Download** button. The file stays available for 7 days by default (the admin can change this); after that it is deleted and
  you can request a new one. Only one export can be in preparation at a time.
- **Request account deletion** (customers only) sends a deletion request to the admins. You can add a note. While it is pending you cannot
  file another one. If an admin rejects it, their reason replaces your note on the page. Staff accounts are closed by an admin
  instead.
- **Consent records** lists the privacy policies you accepted, with the date.

## What an export contains

The ZIP holds JSON files (UTF-8, times in UTC) and the files the person uploaded:

| File | Contents |
|---|---|
| `README.txt` | What each file holds |
| `profile.json` | Name, email, role, tier (staff), organization, teams (staff), language, signature (staff), notification preferences, last sign-in, sign-in identities (SSO issuer and subject), sessions (IP, browser, dates), API tokens (name, prefix, scopes, dates; never the secret) |
| `tickets.json` | Every ticket the person requested: number, subject, status, channel, dates, satisfaction rating, the conversation and the custom-field values |
| `messages.json` | Every message the person wrote, on any ticket |
| `attachments.json` + `attachments/` | Metadata of the files the person uploaded, and the files themselves |
| `consents.json` | Accepted privacy policies: URL, version, date, IP |
| `audit.json` | Security and administration events about the account (for example sign-ins, a password reset, the account being disabled) and events the person caused |
| `requests.json` | The person's export and deletion requests |

A **self-service export never contains**:

- internal notes, system messages or staff-only custom fields;
- messages written by other customers (for example someone in CC on the same ticket);
- the names, ids or IP addresses of the staff or other people who acted on the account (`audit.json` says only "staff" or "system").
  This also holds for admin exports.

An **admin export** can include internal data: switch on **Include internal notes and staff-only fields** on the person's
**Privacy** card. Use this when you need the
complete record, for example for a court request, and review the file before you hand it out. An admin export is downloadable by
admins only, never by the person, and the ready email goes to the admin who requested it.

## For admins: the Privacy page

Open **People → Privacy** (admins only).

**Requests** lists every export and erasure request, newest first, with filters for type and status.

- **Pending erasure requests**: choose **Approve** and pick a mode (see below), or **Reject** with a reason. The reason is shown to
  the customer on their "My data" page; it is not written to the audit log.
- **Ready exports** have a **Download** button until they expire.

**Retention and settings** holds the retention policy, the automatic approval of erasure requests and the policy version (see
[Retention policies](#retention-policies) and [Consent](#consent-at-registration)).

On a **customer's detail page** and in a **staff member's edit dialog** (**Edit staff member**), the **Privacy** card has **Export
data** and **Erase personal data** for that person, and lists their recent requests. An export can't start while another export of
that person is still being prepared.

### Automatic approval

Under **Deletion requests**, set **Approve automatically after (days)** (0 = admins decide, the default; at most 365) and the
**Mode for automatic approvals**. The daily retention job then approves every pending request older than N days, using the N in
force when it runs. Admins can still approve or reject before that. The customer's "My data" page does not show a planned date.

## Erasure

Erasing an account is **irreversible**. There are two modes:

| | `anonymize` | `anonymize_and_redact` |
|---|---|---|
| Name | becomes "Deleted user #n" | same |
| Email | becomes `erased+<id>@invalid` | same |
| Password, sign-in identities (SSO links), sessions, API tokens, sign-in links, two-step verification, in-app notifications, personal saved views, team memberships, watcher entries, signature, notification preferences | removed | same |
| Account | disabled; open browser sessions and WebSockets are closed at once (an account that was already disabled stays so) | same |
| IP address in consent records | cleared (the record stays, as proof of the lawful basis) | same |
| Raw inbound emails sent from the address | sender address cleared, raw file deleted | same, plus the subject cleared |
| Exports of the person | finished files deleted; exports still being prepared are cancelled | same |
| Messages the person wrote | kept | body replaced with `[redacted]` |
| Files the person uploaded | kept | deleted (rows and files) |
| Custom-field values and satisfaction comments of the person's tickets | kept | deleted |
| Search index | unchanged (names and emails are not indexed) | refreshed: redacted text is no longer found |

**Tickets stay**, with their numbers, dates, status, team and history, because they are business records. The requester shows as
"Deleted user #n" everywhere, including the ticket list and reports. Erasing a staff member also unassigns the tickets they no longer
may own, exactly like disabling them.

An erasure is refused (409) for your own account, for a break-glass admin, and for the last active admin.

You can run an erasure again, for example `anonymize_and_redact` after an earlier `anonymize`; the "Deleted user #n" number stays.

### What is not covered

Plan for these yourself:

- **Text written by other people** that mentions the person (for example "Hi Anna" in an agent's reply, or the ticket subject) is not
  changed. Edit or redact it by hand if needed, or let the retention policy anonymize the ticket later.
- **The ticket event log** (the timeline) is append-only: it keeps ids and a snapshot of each ticket's subject until the ticket is
  purged.
- **The audit log** is append-only: earlier entries keep their IP addresses until the audit retention removes them. Audit entries
  never contain names or email addresses.
- **The webhook delivery log** (30 days), **failed background jobs** (dead jobs are kept 30 days) and **emails already sent** may contain
  the old name or address.
- **Backups** taken before the erasure still contain the data until you rotate them out.
- **Your identity provider**: erasing removes the SSO link in SimpleTickets, not the account in Authentik, Keycloak or your other IdP.
  Remove or block it there too, or a later SSO sign-in can create a new account.
- **Copies outside SimpleTickets**: exported files, CRM syncs through webhooks or the API, mailboxes of staff.

## Retention policies

A job runs every 24 hours (the first run shortly after the server starts, not at a fixed time of day) and applies the policy.
**Preview** on the settings page shows how many items the job would process right now, using the retention values in the form even
before you save them; the count of deletion requests to approve uses the **saved** automatic-approval setting. The next run
processes exactly those items.

| Rule | Default | Range | What happens |
|---|---|---|---|
| Closed tickets older than N months (by closing date) | off (0) | 0–240 | **anonymize**: subject and every message body become `[redacted]`, attachments, custom-field values and the satisfaction comment are deleted, the raw inbound emails of the ticket lose sender, subject and file; numbers, dates, status, team and requester stay for reports. **purge**: the ticket is deleted completely, with its messages, files and event history; its raw inbound emails lose sender, subject and file too |
| Raw inbound emails older than N days | 90 | 1–90 | the inbound log entry and its raw email file are deleted (quarantined mail waits for an admin) |
| Audit log entries older than N months | off (0): only `ST_AUDIT_RETENTION_DAYS` applies | 0–120 | deleted |
| Finished exports | 7 days | 1–30 | the file is deleted and the request shows as expired |

The built-in housekeeping keeps running: raw inbound emails are always deleted after 90 days, and audit entries after
`ST_AUDIT_RETENTION_DAYS` (default 365). The policy can only make these periods **shorter**.

Each run that changes something writes one `privacy.retention_run` audit entry with the counts.

## Consent at registration

When **System → Branding → Legal links → Privacy policy URL** is set, the customer registration form shows a required checkbox
("I have read and accept the privacy policy") with a link to the policy. The server refuses a registration without it. The acceptance is
recorded with the time, the policy URL, the **policy version** from the Privacy settings (for example a date; empty by default) and the
client IP. It appears on the customer's "My data" page and in their export.

When you publish a new version of your policy, change the policy version, so later records show which one was accepted. Accounts
created another way (an invitation, SSO, a customer created by staff or by an inbound email) do not pass through this checkbox; collect
consent for them by your own process if you need it.

## Audit entries

Every action is in **System → Audit log**, without names or email addresses:

| Action | When |
|---|---|
| `privacy.export_requested` | an export was requested (by the person or an admin; `include_internal` is recorded) |
| `privacy.export_downloaded` | an export was downloaded |
| `privacy.erasure_requested` | a customer asked for deletion |
| `privacy.erasure_rejected` | an admin rejected a request |
| `privacy.user_erased` | an account was erased: mode and counts (messages redacted, files deleted, field values removed, tickets kept) |
| `auth.user_disabled` | written by the erasure too, with the reason `privacy_erasure`, unless the account was already disabled |
| `privacy.settings_updated` | the retention or privacy settings changed |
| `privacy.retention_run` | the daily job removed or anonymized something |

## For integrators

All routes are in the OpenAPI document (`/api/v1/openapi.yaml`, tag **Privacy**).

| Route | Who | Purpose |
|---|---|---|
| `GET /api/v1/me/privacy` | signed-in user | Own requests (the latest 20), consents, `can_request_erasure`, `export_days` |
| `POST /api/v1/me/privacy/export` | signed-in user | Start an export (202; 409 while one is pending; 429 above 1 per minute, burst 3) |
| `GET /api/v1/me/privacy/exports/{id}` | the requester | Download (404 when not ready or expired) |
| `POST /api/v1/me/privacy/erasure` | customers | File a deletion request (`{reason?}`; 202; 409 while one is pending) |
| `GET /api/v1/admin/privacy/requests` | admin | List (`type`, `status`, `user_id`, `cursor`, `limit`) |
| `POST /api/v1/admin/privacy/requests/{id}/approve` | admin | `{mode}` → erasure result (200; 404 for a non-erasure request; 409 when it is no longer pending) |
| `POST /api/v1/admin/privacy/requests/{id}/reject` | admin | `{reason}`, 1–1000 characters (200) |
| `GET /api/v1/admin/privacy/exports/{id}` | admin | Download any ready export |
| `POST /api/v1/admin/privacy/users/{id}/export` | admin | `{include_internal?}` (202; 409 while an export of that user is unfinished) |
| `POST /api/v1/admin/privacy/users/{id}/erase` | admin | `{mode: anonymize \| anonymize_and_redact}` (200) |
| `GET`/`PUT /api/v1/admin/privacy/settings` | admin | Retention policy, automatic approval, policy version |
| `GET /api/v1/admin/privacy/retention/preview` | admin | Dry-run counts; query parameters override the saved policy |

The self-service routes are meant for the browser session. A personal access token needs the `admin` scope to use them, and only
admins can hold that scope, so customers, agents and supervisors use them through the browser only. The settings are stored under
the keys `retention` and `privacy`; see [Settings stored in the database](../DOCUMENTATION.md#settings-stored-in-the-database). Registration
takes `accept_privacy: true` when a privacy policy URL is configured (otherwise 422 with the field code `required`).
