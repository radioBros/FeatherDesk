# Changing a contract

A *contract* is anything another part of the system, or an API client, relies on without reading your code:

- the REST API (`api/openapi.yaml`: paths, schemas, enums, problem slugs, field error codes)
- WebSocket frames and topics (the `Ws*` schemas and `x-websocket` on `GET /ws`)
- event types and their data (`internal/events`, the webhook envelope)
- the database schema (migrations, `CHECK` enums)
- the permission matrix and the ticket lifecycle (`internal/authz`, `internal/tickets/transitions.go`)

Contracts change in **one** pull request, in the order below. Each step has a test that fails if you skip it, so CI tells you what is missing.

## Checklist

1. **Spec first.** Write down the new behaviour where it is defined: the permission matrix, the lifecycle table, the event list or the API surface. Maintainers keep these in the planning tree under `development/contracts/`, which is not in the repository. Outside contributors describe the change in the pull request instead.
2. **`api/openapi.yaml`.** Add or change the operation, schema, enum, problem slug or field error code. Every route must be in the spec and every spec operation must be served.
   - `redocly lint api/openapi.yaml` passes.
   - `TestAllSpecOperationsImplemented` (`internal/app`) and `TestRoutesDocumented` check route ⇔ spec equality.
   - `TestProblemSlugsMatchRegistry` and `TestFieldErrorCodesDocumented` (`internal/apispec`) check problem slugs and field codes.
3. **Generated and mirrored code.**
   - TypeScript types: `cd web && pnpm gen:api`, then commit `web/src/api/schema.d.ts`. CI regenerates it and fails on a diff.
   - Go enums in `internal/domain`. `TestEnumsMatchOpenAPI` compares them with the spec.
   - New field error codes need a message in every locale (`codes.spec.ts`).
4. **Database.** Add the migration for **both** engines, with the same number and name, inside the area's number range (see [internals](internals.md#migration-number-ranges-d-18)).
   - `TestSchemaParity` compares the two engines.
   - `TestEnumsMatchSchema/sqlite` and `/postgres` compare the `CHECK` lists with the Go enums.
5. **Rules as data.** When permissions, transitions or events change, update the tables that encode them, together with their reviewed constants:
   - `internal/authz/matrix_test.go`: `specCells` and `matrixRows`.
   - `internal/tickets/transitions.go` and `TestTransitions`, which covers all 36 pairs.
   - `internal/events`: the constant, `All`, `NewData` and the customer-visibility tag. `TestEventsCustomerVisibility` and `TestEventDataStructs` check them.
   - WebSocket frames: `TestWSEnvelopeMatchesSchema` validates real hub frames against `WsServerFrame`.
6. **Handlers and tests.** Every handler test runs through the real router. Its responses are validated against the spec, error answers included. Store tests run on SQLite **and** PostgreSQL.
7. **Docs.** Update [internals.md](internals.md) when a package API or a wiring rule changes. Update `docs/DOCUMENTATION.md` when users or API clients see the change. Add a `CHANGELOG.md` entry.
8. **Breaking changes.** Within `/api/v1`, changes are additive only. If a change is breaking anyway, say so at the top of the pull request description and in the changelog.

The pull request template repeats this list as checkboxes.
