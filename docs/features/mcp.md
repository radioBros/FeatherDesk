# AI assistants (MCP)

> Part of the [SimpleTickets manual](../DOCUMENTATION.md). The overview is in [Admin guide → AI assistants](../DOCUMENTATION.md#720-ai-assistants-mcp-system--support-settings-admin) and [REST API → AI assistants (MCP)](../DOCUMENTATION.md#1010-ai-assistants-mcp); this page has every detail.

SimpleTickets has a built-in [Model Context Protocol](https://modelcontextprotocol.io) (MCP) server. AI assistants such as
**Claude Code**, **Claude Desktop** and **Cursor** connect to it with a user's personal access token and can then search,
read, open and work tickets and read the knowledge base, in the conversation.

- **One endpoint:** `POST https://<your host>/mcp` (the *Streamable HTTP* transport). Nothing extra to run or install on the server.
- **Off by default.** An admin turns it on under *System → Support settings → Integrations*.
- **The assistant is the token owner.** Every tool call runs as that user, limited by the token's scopes, through the same
  permission checks as the REST API. An assistant can never see or do more than the user could with the same token, and a
  customer's assistant never sees internal notes or staff-only fields.

## 1. Turning it on

*System → Support settings → Integrations → AI assistants (MCP)* (admins). The switch saves immediately; the change is
written to the audit log as `integrations.updated`. While it is off, every method on `/mcp` answers **404**, including
sessions that were open before.

The API equivalent is `PATCH /api/v1/admin/integrations` with `{"mcp_enabled": true}` (a token needs the `admin` scope).

## 2. Connecting an assistant

1. Create a personal access token: *Profile → API tokens* (agents) or *My profile → API tokens* (customers). Choose the scopes
   from the table below and an expiry date.
2. Paste one of the configurations below into the assistant, with the token in place of `st_pat_…`.

The *API tokens* page shows a **Connect an AI assistant** panel with the endpoint of your instance and these snippets ready to
copy, while MCP is on.

### Recommended scopes

| Who | To read and work tickets | Read only |
|---|---|---|
| Agents, supervisors, admins | `tickets:read`, `tickets:write`, `directory:read`, `kb:read` | `tickets:read`, `directory:read`, `kb:read` |
| Customers | `tickets:read`, `tickets:write`, `kb:read` | `tickets:read`, `kb:read` |

- `directory:read` lets staff tools list teams and categories; `tickets:read` already includes `kb:read`.
- Do not give an assistant the `admin` scope: no tool needs it.
- A tool the token's scopes do not allow is left out of the assistant's tool list, and refused if it is called anyway.
- Give the token an expiry date, and revoke it (*API tokens → Revoke*) when you stop using the assistant.

### Claude Code

```sh
claude mcp add --transport http simpletickets https://help.example.com/mcp \
  --header "Authorization: Bearer st_pat_…"
```

Add `--scope user` to make it available in every project. `claude mcp list` shows whether it connects.

### Claude Desktop

Claude Desktop starts local MCP servers listed in its `claude_desktop_config.json` (opened from the app's developer settings);
the [`mcp-remote`](https://www.npmjs.com/package/mcp-remote) bridge connects it to the HTTP endpoint and adds the token. It needs
Node.js.

```json
{
  "mcpServers": {
    "simpletickets": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "https://help.example.com/mcp", "--header", "Authorization:Bearer ${ST_TOKEN}"],
      "env": { "ST_TOKEN": "st_pat_…" }
    }
  }
}
```

Restart Claude Desktop afterwards. Keep `Authorization:Bearer` without a space after the colon: `mcp-remote` reads the
header from one argument, and some platforms split arguments at spaces. The token stays in `env`, out of the argument list.
`mcp-remote` refuses plain `http://` URLs unless you add `--allow-http` (only for a local test instance).

SimpleTickets does not offer OAuth sign-in for MCP, so use this bridge rather than a custom connector that expects OAuth.

### Cursor

`~/.cursor/mcp.json` (every project) or `.cursor/mcp.json` (one project), using Cursor's remote-server form (`url` plus
`headers`; check Cursor's MCP documentation if your version differs):

```json
{
  "mcpServers": {
    "simpletickets": {
      "url": "https://help.example.com/mcp",
      "headers": { "Authorization": "Bearer st_pat_…" }
    }
  }
}
```

### Other clients

Any MCP client with the Streamable HTTP transport and custom headers works: point it at `https://<host>/mcp` and send
`Authorization: Bearer st_pat_…`. For a quick test, the official inspector:

```sh
npx @modelcontextprotocol/inspector --cli https://help.example.com/mcp --transport http \
  --header "Authorization: Bearer st_pat_…" --method tools/list
```

## 3. Tools

Staff and customers get different tool sets. Every tool that takes `ticket` accepts a reference (`"WEB-12"`, [projects](projects.md)), a number (`1042` or `"#1042"`) or a ticket id.

| Tool | Staff | Customers | What it does | Token scope |
|---|---|---|---|---|
| `search_tickets` | ✓ | ✓ | Full-text search plus filters (staff: status, priority, assignee, team, sort; customers: status). Staff search includes internal notes; customer search only public text | `tickets:read` |
| `list_my_tickets` | ✓ | ✓ | Staff: tickets assigned to you, open work by default. Customers: tickets you opened | `tickets:read` |
| `get_ticket` | ✓ | ✓ | One ticket with its conversation (newest `max_messages`, default 50) and `allowed_actions`. Staff see internal notes; customers see public replies only | `tickets:read` |
| `create_ticket` | ✓ | ✓ | Staff: for a customer (`requester_email` or `requester_id`), with team, category, assignee, priority, tags. Customers: their own ticket | `tickets:write` |
| `reply_to_ticket` | ✓ | ✓ | Staff: a **public** reply (the customer gets it by email), optionally with `set_status`. Customers: a reply; on a closed ticket it opens a follow-up and returns `followup_ticket_id` | `tickets:write` |
| `update_ticket` | ✓ | ✓ | Staff: status, priority, category, team, assignee (`"me"`, `"none"`), subject, tags. Customers: `status` `resolved` or `open` (reopen) | `tickets:write` |
| `add_internal_note` | ✓ | | Staff-only note; never reaches the customer | `tickets:write` |
| `escalate_ticket` / `deescalate_ticket` | ✓ | | One tier up or down, with a reason, optionally to another team | `tickets:write` |
| `apply_macro` | ✓ | | A macro by id or exact name (case-insensitive) | `tickets:write` |
| `search_kb` | ✓ | ✓ | Knowledge base search; staff also find internal articles | none |
| `get_kb_article` | ✓ | ✓ | One article by slug, as Markdown | none |
| `list_teams` | ✓ | | Team ids and names | `directory:read` |
| `list_categories` | ✓ | ✓ | Category ids and names (customers: active ones) | staff `directory:read`, customers `tickets:read` |

Each tool runs the REST operation of the same action (for example `update_ticket` is `PATCH /api/v1/agent/tickets/{id}`), so
every rule of the [ticket lifecycle](../DOCUMENTATION.md#14-ticket-lifecycle) and of `allowed_actions` applies: a status change
the lifecycle forbids fails with `invalid-transition` and the allowed targets, a change the user may not make fails with
`forbidden`, and an escalation that lifts a ticket above the agent's tier returns `visible: false`.

**Results.** A tool result has the REST response as `structuredContent` and the same JSON as text. Lists are wrapped in an object
(`{"teams": […]}`); search results keep `next_cursor` for the next page (`cursor` argument).

**Errors.** A failed call is a normal result with `isError: true`, so the assistant can read it and correct itself. The text is
`Error [<slug>] (HTTP <status>): <title>: <detail>`, and `structuredContent.error` has the
[problem](../DOCUMENTATION.md#104-errors) fields: `slug`, `status`, `title`, `detail`, field `errors`, `allowed` for
`invalid-transition`. Invalid arguments (an unknown field, a missing ticket) have the slug `validation`. A tool that does not
exist for the caller, such as a staff-only tool called with a customer token, is a JSON-RPC error `-32602` (*Unknown tool*).

## 4. Resources

| URI | Content |
|---|---|
| `ticket://{ref}` | The ticket as `get_ticket` returns it (JSON), all messages, e.g. `ticket://WEB-12`. `ticket://{number}` still resolves |
| `kb://{slug}` | The article as Markdown, title first |

`resources/list` returns the 20 most recently updated tickets the caller can see and the 20 newest articles they may read;
`resources/templates/list` returns the two templates. A ticket or article the caller cannot see is *Resource not found*
(`-32002`, or `-32602` with protocol 2026-07-28).

## 5. Security

- **Bearer tokens only.** `/mcp` ignores the session cookie and needs no CSRF token. Without a valid token it answers `401` with
  `WWW-Authenticate: Bearer`. A token is checked on every request, so revoking it, disabling the user or changing their role
  takes effect on the next call.
- **Same permissions as the API.** Tools call the REST handlers in-process with the token, so they get exactly the REST
  operation's scope check (I-6), visibility rules and customer views: internal notes, tiers, teams, assignees and SLA fields
  never reach a customer (I-2, I-7, I-9). A customer's token cannot reach a staff tool at all. While the customer portal is
  switched off, customer tokens are refused with `401`.
- **Origin check.** A request with an `Origin` header other than the origin of `ST_PUBLIC_URL` (or one listed in
  `ST_ALLOWED_ORIGINS`) is refused with `403`, which stops web pages (DNS rebinding) from using a token stored in a local
  client. Desktop and command-line clients send no `Origin`.
- **Rate limits.** 20 requests per second per client IP before authentication, then 10 per second (burst 40) per token
  owner; above that `429` with `Retry-After`. The REST limits of each operation apply as well.
- **Audit.** Every change an assistant makes is an ordinary ticket event with the token owner as the actor; tickets and messages
  it creates have the channel `api`. Each tool call is also logged (`mcp tool call`, with the tool, user id, outcome and the
  request id shared with the REST calls it made).
- **Sessions** (older protocol revisions) are kept in memory for 24 hours of inactivity, at most 10 000, and only work with the
  token that created them. A restart ends them; clients start a new one by themselves.

## 6. Protocol details

For client developers. The server speaks JSON-RPC 2.0 over Streamable HTTP and answers every request with a single
`application/json` body (no SSE streams).

| Revision | How a client uses it |
|---|---|
| **2026-07-28** (current) | Stateless. Every request carries `_meta` `io.modelcontextprotocol/protocolVersion` and `io.modelcontextprotocol/clientCapabilities`, mirrored in the headers `MCP-Protocol-Version`, `Mcp-Method` and `Mcp-Name` (`tools/call`, `resources/read`; `=?base64?…?=` values are decoded). `server/discover` lists the versions. Results carry `resultType` and `_meta.io.modelcontextprotocol/serverInfo`; list results carry `ttlMs` and `cacheScope` |
| **2025-11-25**, **2025-06-18**, **2025-03-26** | `initialize` negotiates the revision (an unknown one gets `2025-11-25`) and returns `Mcp-Session-Id`; later requests send it and `MCP-Protocol-Version`. `ping` works. `DELETE /mcp` ends the session. JSON-RPC batches are accepted only in a `2025-03-26` session |

Methods: `initialize`, `notifications/initialized`, `ping`, `server/discover`, `tools/list`, `tools/call`,
`resources/list`, `resources/templates/list`, `resources/read`. There are no prompts, no subscriptions and no
server-to-client requests.

| Situation | HTTP | JSON-RPC error |
|---|---|---|
| Body is not JSON | 400 | `-32700` |
| Not a valid JSON-RPC request (`jsonrpc` ≠ `"2.0"`, `id` null or fractional, `params` not an object), batch outside 2025-03-26 | 400 | `-32600` |
| Legacy request without `Mcp-Session-Id` / with a `MCP-Protocol-Version` that contradicts the session | 400 | `-32600` |
| Unknown or expired session | 404 | `-32600` |
| 2026-07-28: unsupported version (`data.supported`, `data.requested`) | 400 | `-32022` |
| 2026-07-28: header missing or different from the body | 400 | `-32020` |
| 2026-07-28: `_meta` protocol version or client capabilities missing | 400 | `-32602` |
| Unknown method | 404 (2026-07-28) / 200 | `-32601` |
| Unknown tool, invalid params | 200 | `-32602` |
| Notification or client response | 202, no body | |
| `GET /mcp` | 405 | |
| MCP switched off | 404 (problem document) | |

The operation is documented as `postMcp`, `getMcp` and `deleteMcpSession` in `api/openapi.yaml`. Code: `internal/mcp`.
