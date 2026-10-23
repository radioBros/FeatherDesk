# Contributing to FeatherDesk

Thanks for helping. FeatherDesk is a lightweight help desk. The whole product is one Go binary with an embedded Vue SPA, running in one container. The project is maintained by [RadioBros](https://radiobros.com/). Most review rules follow from that goal and from the footprint budgets below.

By contributing, you agree that your contribution is licensed under the **GNU AGPL-3.0-only** (see `LICENSE`).

## Prerequisites

- Go 1.27.3 or later
- Node 24 with corepack (`corepack enable`; the repo uses pnpm 11)
- Docker (for Postgres tests, the image, and the dev Authentik/Mailpit stack)

No other tools are needed. The Makefile runs golangci-lint, helm and kubeconform through Docker when they are not installed.

## Run it locally

From a clean clone to a signed-in agent panel in 6 commands: [docs/dev/getting-started.md](docs/dev/getting-started.md).

```sh
make dev           # Go API on :8080 (restarts on change, FD_DEV=true) + Vite on :5173
                   # open http://localhost:5173/agent
make seed          # demo data in data/dev (TICKETS=50 by default)
make dev-deps      # optional: Authentik (SSO) + Mailpit (email) in Docker
```

The full stack in containers runs the app built from source, Authentik 2026.8.4 with the FeatherDesk blueprint, and Mailpit:

```sh
docker compose -f deploy/compose.yml -f deploy/compose.dev.yml up -d --build
```

The dev credentials are listed in `deploy/.env.dev` and are public.

## Before you open a pull request

```sh
make lint          # gofmt, go vet, golangci-lint, eslint, vue-tsc, SPDX, dependency gate
make test          # go test -race on SQLite AND Postgres 18, plus the web unit tests
make e2e           # Playwright (chromium) against a fresh binary
```

If your change affects size, memory or startup, also run:

```sh
make image footprint   # image size, idle RSS, idle CPU, cold start
make loadtest          # 50 agents, 200 WebSockets, 10 req/s under mem_limit 256m
```

## Conventions

The binding rules live in `docs/dev/internals.md`. The essentials:

- **Both database engines.** Every store-touching test runs on SQLite and Postgres through `storetest.Engines`. Each migration exists for both engines, with the same number and name.
- **No new Go modules** without a decision. The runtime dependency cap is 15, checked by `scripts/depgate.sh`. Do not run `go get`.
- **Every route is in `api/openapi.yaml`.** Tests validate responses against the spec, and `AssertRoutes` fails on any route the spec lacks.
- **Errors** returned to HTTP clients use `problems.*` (RFC 9457).
- **Footprint budgets** are enforced in CI:

  | Budget | Limit |
  |---|---|
  | Image size, compressed | ≤ 30 MB |
  | Idle RSS | ≤ 50 MB |
  | RSS under load | ≤ 120 MB |
  | Cold start | ≤ 3 s |
  | Initial JS per area | ≤ 300 KB gzip |

  Polling loops must not run more often than once per second, and every buffer must be bounded.
- **Frontend forms.** Every required or formatted input takes `:rules` from `web/src/constants/validations.ts`, and every submit calls `validate()` first.
- **Licence header.** Every `.go`, `.ts` and `.vue` file starts with `SPDX-License-Identifier: AGPL-3.0-only`. Run `scripts/spdx.sh --fix` to add it.
- **Third-party notices.** After changing Go modules or `web/package.json` dependencies, run `go run ./tools/notices` (with `web/node_modules` installed) and commit `THIRD_PARTY_NOTICES`. CI fails when it is stale or a licence is not AGPL-3.0-compatible.
- **Configuration.** A new `FD_*` variable must be added to `.env.example`. `TestEnvExampleMatchesConfig` fails otherwise.

## Changing the API contract

1. Edit `api/openapi.yaml` first.
2. Regenerate the frontend types with `cd web && pnpm gen:api`.
3. Update handlers and tests. Responses must validate against the spec.
4. Mention the contract change in the pull request (see the PR template checklist).

## Commits and pull requests

- Keep each pull request focused on one change, with tests.
- Use conventional commit messages (`feat:`, `fix:`, `docs:`, `chore:` …), since release notes are generated from them.
- Add user-visible changes to the `[Unreleased]` section of [CHANGELOG.md](CHANGELOG.md) (Keep a Changelog). A change under `internal/store/migrations/` **must** add a `### Migrations` entry there; CI checks it (`scripts/changelog-check.sh`).
- CI must be green. A gate that blocks a legitimate change is relaxed only in a reviewed PR that records the reason.
- CI runs only what a change needs (`scripts/ci-changes.sh`): Go jobs for Go code, migrations or the API spec (and `go test` only on the packages the change can break, `tools/ciaffected`), web jobs for `web/`, the docs check for docs or Go, e2e for Go or web (on SQLite; Postgres too when `internal/store/` changed). The image, smoke, kind and proxy jobs run on a pull request only when `Dockerfile`, `deploy/` or their scripts change, and after merging when Go or web changed. The nightly run, a manual run and any change to CI itself run everything. So run the full local checks above before you push.

## Reporting bugs and security issues

- Bugs and feature ideas: GitHub issues, using the templates.
- Security problems: **never** in a public issue. See `SECURITY.md`.

Everyone taking part follows the `CODE_OF_CONDUCT.md`.
