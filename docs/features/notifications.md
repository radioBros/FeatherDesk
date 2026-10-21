# Notifications: notification centre and browser notifications

> Part of the [SimpleTickets manual](../DOCUMENTATION.md). The overview is in [Admin guide → Notification centre](../DOCUMENTATION.md#719-notification-centre), [Agent guide → Notification centre](../DOCUMENTATION.md#810-notification-centre) and the [Customer portal guide](../DOCUMENTATION.md#portal-notifications); this page has every detail.

SimpleTickets tells people about ticket activity on three channels:

- **In-app**: the notification centre, i.e. the bell in the top bar.
- **Email**: the existing ticket emails ([DOCUMENTATION.md §6.3](../DOCUMENTATION.md#63-notifications-templates-and-languages)).
- **Browser**: a desktop notification from your browser while SimpleTickets is open in a background tab.

Every user picks the channels per notification type. This guide covers users and admins. The last section has API details for integrators.

## What creates a notification

The notification centre uses the same rules as the ticket emails, so a notification and its email always go to the same people.
- **Your own actions:** you are never notified about something you did yourself.
- **Visibility:** staff are only notified about tickets they can see (team, tier, assignee).
- **Inactive accounts** (disabled, or not yet verified) get nothing.
- **Team mailboxes:** when a team's notifications go to the *Team mailbox* only, its *New ticket in my queue* and *Ticket escalated
  to my team* notices go to the mailbox and not to the members' bells. *Customer replied* and SLA notices still reach the agents.

**Staff notifications**

| Type | When |
|---|---|
| New ticket in my queue | A ticket is created in one of your teams at a tier you work at (supervisors and admins: triage tickets) |
| Customer replied | The customer replied on a ticket assigned to you, or an unassigned one in your team (supervisors and admins: unassigned triage tickets) |
| Ticket assigned to me | Someone else assigned a ticket to you |
| Ticket escalated to my team | A ticket was escalated to your team and tier (shows the new tier) |
| Mentioned in a note | You were @mentioned in an internal note |
| SLA warnings and breaches | An SLA warning or breach on your ticket or your team's ticket (supervisors also get these) |
| Linked tickets resolved | The last open child of a ticket you are assigned to was resolved |

**Customer notifications**

| Type | When |
|---|---|
| Request received | The support team opened a ticket on your behalf |
| Reply to my request | The support team replied on your ticket (or on one you watch) |
| Request resolved | Your ticket was resolved |

Customers are never notified about internal notes, assignments, escalations, tiers, teams or SLAs. A customer notification shows only the ticket number and subject. It names the person only on a new reply, as the ticket thread already does.

Team and triage **mailboxes** get email only. They have no notification centre.

## The bell and the notification page

The bell is in the top bar of the agent panel and of the customer portal (for customers; staff visiting the portal don't see it).
Its badge shows how many notifications are unread, up to "99+".

**Updates are live.**
- A new notification raises the badge at once, without a reload, through the realtime connection.
- Reading a notification in one tab updates every other open tab.
- After a lost connection, or when you come back to a tab, the badge is refreshed.

**The dropdown** shows the 10 newest notifications.
- Click one to mark it read and open its ticket.
- **Mark all as read** clears the badge.
- **See all notifications** opens the full page.

**The full page** is at `/agent/notifications` for staff and `/notifications` for customers.
- Show **All** or **Unread**, and filter by **Kind**.
- Mark single notifications read, delete them, or mark everything read.
- **Load more** shows older entries.

## Preferences

Your profile has a **Notifications** card: `/agent/profile` for staff, `/profile` for customers. It shows a matrix with one row per type and a checkbox per channel.

| Channel | Default | Notes |
|---|---|---|
| In the app | on | Turn it off and that type no longer appears in the bell |
| Email | on | The same setting as the existing email opt-out. **Reply to my request** emails to customers cannot be turned off |
| Browser pop-up | **off** | Needs In the app for the same type, and browser permission (see below) |

Account emails are not in the matrix and are always sent: email verification, password reset, sign-in link, invitation, "already
registered", two-step verification codes and the data-export ready email (every `auth.*` and `privacy.*` template). The admin email
about a disabled webhook is not in the matrix either. Saving the card stores the matrix on your account, so it follows you to every
browser.

## Browser notifications

Browser notifications are shown by your browser while a SimpleTickets tab is open but not visible: another tab is in front, or the window is minimised. Each tab decides for itself, so a SimpleTickets tab in the background can still pop up while you look at another SimpleTickets tab. A window that is merely covered by other windows usually counts as visible, so it may not pop up. While you are looking at a tab, its bell is enough.

**To turn them on:**
1. Open your profile, **Notifications** card.
2. Click **Allow browser notifications** and allow them in your browser's prompt.
3. Tick **Browser pop-up** for the types you want, for example *Ticket assigned to me* and *Mentioned in a note*, then click **Save notification settings**.

Optionally, switch on **Play a sound with each pop-up (this browser only)** and save. This setting is stored per browser, not on your account.

Clicking a browser notification asks the browser to bring the SimpleTickets tab to the front (some browsers refuse), opens the ticket and marks the notification read. If several SimpleTickets tabs are in the background, each one shows the pop-up under the same tag, so the browser shows one notification; the sound can still play once per tab.

> **Limitation: browser notifications only arrive while a SimpleTickets tab is open somewhere.**
>
> SimpleTickets uses the browser's Notification API on an open page. It does **not** use Web Push, service workers or a push service. That keeps the help desk self-contained: there is no extra service, key pair or third-party push provider.
>
> - If every SimpleTickets tab is closed, or the browser is not running, nothing is shown. The notification is still in the bell, and still emailed if Email is on, the next time you look.
> - The operating system can hide them: "Do not disturb", Focus modes, or notifications switched off for your browser in the OS settings.
> - If you clicked **Block** on the prompt, the browser does not ask again. Re-enable notifications for the help desk's site in your browser's site settings (usually the lock icon in the address bar), then reload the page.
> - The page must be served over HTTPS, or from `localhost`. Browsers refuse notifications on plain HTTP.

Use email for anything you must not miss while you are away from the browser.

## Retention

Notifications are kept for **90 days**, and at most the **500 newest per user**. An hourly background job deletes older ones. Deleting a ticket deletes its notifications.

## For admins

- **Nothing to set up.** The notification centre is part of the single container, with no extra service, and it works without SMTP. On an instance where email is not configured, the bell is the only channel.
- **Admin → Email → Notifications** switches templates off for **email only**. It does not affect the bell or browser notifications.
- The banner "Email is not configured: notifications are off." on an instance without SMTP refers to email only; the bell keeps working.
- **Privacy.** A notification stores:
  - who it is for, its type, ticket, actor and time;
  - a few staff-only details: the tiers of an escalation, which SLA was hit.

  It stores no message text. The ticket number, subject and person's name are looked up when the list is shown, so renaming, anonymising or deleting a user or ticket is reflected at once. Notifications are deleted together with the user or ticket.
- **Load.** Each ticket event writes all of its notifications in one database transaction. Read and delete actions are one short transaction each (the change plus the new unread count).

## For integrators

The REST endpoints are in [`api/openapi.yaml`](../../api/openapi.yaml), tag *Me*. Each one acts on the caller's own notifications; another user's id returns 404.

| Method and path | Purpose |
|---|---|
| `GET /api/v1/me/notifications?unread=true&type=…&limit=50&cursor=…` | Newest first, `{items, next_cursor, unread_count}`. `limit` is 1–200 (default 50); `next_cursor` is `null` at the end |
| `GET /api/v1/me/notifications/unread-count` | `{unread_count}` |
| `POST /api/v1/me/notifications/{id}/read` | Mark one read → `{unread_count}` |
| `POST /api/v1/me/notifications/read-all` | Mark all read → `{unread_count: 0}` |
| `DELETE /api/v1/me/notifications/{id}` | Delete one → 204 |
| `PATCH /api/v1/me` with `notification_prefs` | Partial matrix, e.g. `{"notification_prefs": {"staff.assigned": {"browser": true}}}`. `GET /me` returns the full matrix for your audience |

Types are `staff.ticket_new`, `staff.customer_replied`, `staff.assigned`, `staff.escalated`, `staff.mentioned`, `staff.sla`, `staff.children_resolved`, `customer.ticket_received`, `customer.reply` and `customer.resolved`.

On the WebSocket ([DOCUMENTATION.md §11](../DOCUMENTATION.md#11-websocket-protocol)), subscribe to `me` to receive two frames:

```json
{ "type": "event", "topic": "me", "event": "notification.created", "at": "…",
  "data": { "notification": { "id": "…", "type": "staff.assigned", "ticket_id": "…", "ticket_number": 1042,
            "ticket_subject": "VPN down", "actor_name": "Mia", "params": {}, "read_at": null, "created_at": "…" },
            "unread_count": 3, "browser": true } }
{ "type": "event", "topic": "me", "event": "notification.read", "at": "…",
  "data": { "ids": ["…"], "unread_count": 2 } }
```

- `notification.read` is also sent after a delete. After a mark-all-read it carries `"all": true` instead of `ids`. Marking read sends it only when something changed.
- `browser` says whether the recipient enabled the browser channel for that type.
- These frames carry no `event_id` and are **not replayed** by `resume`. After a reconnect, fetch `GET /me/notifications/unread-count`.
