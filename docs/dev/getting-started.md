# Getting started (development)

From a clean clone to a signed-in agent panel on `http://localhost:5173/agent` in
**6 commands**. You need Go 1.27, Node.js 24 (with corepack) and GNU make. Docker is
optional (only for SSO, email and PostgreSQL).

## The 6 commands

```sh
# 1–2  get the code
git clone https://github.com/radioBros/SimpleTickets.git
cd SimpleTickets

# 3    pnpm through corepack (the version is pinned in web/package.json)
corepack enable

# 4    optional: demo customers, teams and tickets in data/dev
make seed

# 5    an admin for the dev database; prints a generated password
ST_DEV=true ST_DATA_DIR=data/dev go run ./cmd/simpletickets create-admin --email admin@dev.test --name "Dev Admin"

# 6    Go API on :8080 (ST_DEV=true, restarts on change) + Vite on :5173.
#      The first run installs the web dependencies and builds the server.
make dev
```

Open **http://localhost:5173/agent/login** and sign in as `admin@dev.test` with the password
that command 5 printed. That's it.

- Create the admin **before** `make dev` (as above). The API decides at start-up whether the
  first-run wizard is still needed, so an admin created while it runs only takes effect after
  the next restart (any `.go` change restarts it).
- Instead of command 5 you can skip straight to `make dev`, open http://localhost:5173/setup
  and paste the `setup token: …` line that `make dev` prints while no admin exists.
- `make seed` creates customers and staff without passwords. Give one a password with
  `ST_DEV=true ST_DATA_DIR=data/dev go run ./cmd/simpletickets reset-password --email <address>`.
  `make demo` (or `simpletickets seed --demo`, when available in your checkout) seeds every
  feature and prints logins for every role.

## How the dev setup works

| Piece | What it does |
|---|---|
| `make dev-api` (part of `make dev`) | Builds `./cmd/simpletickets` and runs `serve` with `ST_DEV=true`, SQLite in `data/dev`, staff password login on, `ST_PUBLIC_URL=http://localhost:5173`. A polling loop (1 s, no extra tool) rebuilds and restarts it when a `.go`, `.sql`, `.tmpl` or `.yaml` file under `cmd/`, `internal/` or `api/` changes. |
| `make dev-web` (part of `make dev`) | Vite on `:5173` with hot module replacement. It proxies `/api` (including the WebSocket), `/branding`, `/inbound`, `/kb/sitemap.xml`, `/healthz` and `/readyz` to the API (`ST_DEV_BACKEND`, default `http://localhost:8080`). |
| Cookies | The browser only talks to `:5173`, so the `st_session` cookie and the CSRF check are same-origin, exactly as in production. `ST_INSECURE_COOKIES=true` lets the cookie work over plain HTTP. |

**The edit loop.**
- A change to a `.vue` or `.ts` file under `web/src` is hot-reloaded in the open page,
  without a full reload and without losing the session.
- A change to a `.go` file restarts the API within a few seconds (one `go build`); the page
  keeps its session because the session lives in the database.

## Useful variations

```sh
DEV_PORT=18081 make dev     # API on another port (Vite follows); e.g. when 8080 is taken
make dev-deps               # Authentik (SSO) + Mailpit (email) in Docker; see docs/DOCUMENTATION.md §15.2
make clean                  # delete bin/ and data/dev (fresh database)
```

The whole stack in containers (the app built from source, Authentik with the blueprint,
Mailpit) is `docker compose -f deploy/compose.yml -f deploy/compose.dev.yml up -d --build`;
the dev credentials are in `deploy/.env.dev`.

## Next steps

- Tests and lint: `make test`, `make lint`, `make e2e` ([CONTRIBUTING.md](../../CONTRIBUTING.md)).
- Package map, wiring and conventions: [internals.md](internals.md).
- The user and operator manual: [DOCUMENTATION.md](../DOCUMENTATION.md).
