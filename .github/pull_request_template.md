## What and why

<!-- What does this change, and why? Link the issue or the plan task (e.g. TKT-012). -->

## How it was tested

<!-- Commands run, new tests (names), screenshots for UI changes. -->

## Checklist

- [ ] `make lint` and `make test` pass (tests run on **SQLite and Postgres**)
- [ ] New `.go` / `.ts` / `.vue` files carry `SPDX-License-Identifier: AGPL-3.0-only`
- [ ] New `ST_*` variables are documented in `.env.example`
- [ ] No new Go module (runtime cap 15) and no new runtime container
- [ ] Footprint budgets still hold (image ≤ 30 MB, idle RSS ≤ 50 MB, load RSS ≤ 120 MB, cold start ≤ 3 s, initial JS ≤ 300 KB gzip). For footprint-sensitive changes, paste the `make footprint` output
- [ ] Frontend inputs use rules from `validations.ts`; submits call `validate()`

### Contract change (API, events, WebSocket frames, DB schema, permissions, lifecycle)

See `docs/dev/contract-changes.md` for the order and the tests behind each step.

- [ ] Not a contract change, **or** all of the following:
  - [ ] Spec updated first (permission matrix, lifecycle, events or API surface), or the change is described above
  - [ ] `api/openapi.yaml` updated, and `redocly lint` passes
  - [ ] Generated and mirrored code refreshed: `cd web && pnpm gen:api`, and the Go enums in `internal/domain`
  - [ ] Migrations added for **both** engines with the same number and name (`TestSchemaParity`, `TestEnumsMatchSchema`)
  - [ ] Rules kept as data: `internal/authz/matrix_test.go` (`matrixRows`), `TestTransitions`, `internal/events` (`All`, `NewData`, customer visibility), WebSocket frames (`TestWSEnvelopeMatchesSchema`)
  - [ ] `docs/dev/internals.md` and `docs/DOCUMENTATION.md` updated where affected
  - [ ] Plans and orchestrators updated (development/CONVENTIONS.md §6)
  - [ ] Breaking change called out in the description and the changelog
