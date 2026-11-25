# PROGRESS

Last updated: 2026-11-25 (RBAC + tests pass).

This tracks exactly what's real. "VERIFIED" means it was actually run and
observed to work in this environment — not inferred from the code
compiling.

## Environment

- Go 1.27.4 installed (winget).
- Node 26.13.1, pnpm 12.21.0 already present.
- PostgreSQL 17 installed (winget) and running as a Windows service
  (`postgresql-x64-17`). Dev role/db: `cms` / `cms_dev_password` on
  `localhost:5432`, database `cms`.
- Docker Desktop installed (winget) but **not usable**: needs WSL2, which
  needs a reboot that wasn't done in this session. `deployments/*.yml` and
  the `Dockerfile` are therefore UNVERIFIED — written to a reasonable spec
  but never actually built/run.
- `trencito42/b4` vendored as a git submodule at `packages/b4` (MIT
  licensed component library + design system, React 19).

## Phase 0 — Architecture and foundation: IMPLEMENTED, partially VERIFIED

- Repo structure matches the spec's layout (cmd/, internal/*, migrations/,
  apps/admin, packages/b4, packages/sdk placeholder).
- Go module builds clean: `go build ./...`, `go vet ./...`, `gofmt -l .`
  all pass. VERIFIED.
- pnpm workspace wired across `apps/*`, `packages/b4/packages/*`,
  `packages/sdk`. `pnpm install` and `pnpm build:design-system` both
  VERIFIED to succeed (required approving esbuild's build script via
  `pnpm approve-builds` — needs an interactive terminal, can't be
  automated from a non-TTY tool call).
- `apps/admin` typechecks clean (`tsc -b --noEmit`). VERIFIED.
- No CI pipeline yet. NOT_STARTED.
- No ADRs written yet (docs/adr/ is empty). NOT_STARTED.

## Phase 1 — First vertical slice: IMPLEMENTED and VERIFIED end-to-end

Verified both via curl and via the real browser (built-in browser pane
against the live Vite dev server + Go server + real Postgres):

- Owner bootstrap (`cms bootstrap-owner <email> <password>`, one-time,
  refuses if any user already exists).
- Login (Argon2id password hashing, server-side session in `sessions`
  table, HttpOnly cookie) / logout / `GET /auth/me`.
- Project create/list.
- Content Type Builder: create a collection type with a field list
  (subset of field types — see below), validated, cached in the in-memory
  schema registry (`internal/schema.Registry`), immediately usable with no
  restart.
- Content Manager: create draft entry, validate against active schema,
  publish (atomically promotes draft → published row, re-validates first),
  list drafts, list/get published entries via a separate delivery route
  tree.
- Admin UI: login page, project list/create, content-type builder form
  (dynamic field list), content manager (create draft, publish button),
  all built from real `@b4/react` components (Button, TextField,
  PasswordField, Card, EmptyState, Select, Checkbox, Tabs, Badge,
  AppShell), talking to the real API over `fetch` + TanStack Query.

Concretely verified in this session: logged in as the bootstrapped owner,
created project "Blog", created content type "Article" (title: required
short_text, body: long_text), created and published an entry via curl,
then confirmed the same project/content-type/entry round-trip through the
actual browser UI, including creating a second entry from the UI itself.

## RBAC — IMPLEMENTED and VERIFIED (migrations/0002_rbac.sql)

Added `project_members` (project_id, user_id, role), four roles ranked
viewer < author < editor < administrator (`internal/authorization`).
Global owners (`users.is_owner`) bypass membership entirely and are
always treated as administrator. There is still no public registration;
owner-only routes (`POST/GET /api/v1/manage/users`) are the whole "invite
a collaborator" flow — the owner creates the account directly with a
password, then adds them to a project with a role. No invite email, no
self-service signup.

Enforcement, by route:
- `POST /manage/projects` (create) and `/manage/users` — owner-only.
- `GET /manage/projects` — self-filters: owner sees every project,
  everyone else sees only projects they're a member of.
- `/manage/projects/{id}/members` — administrator.
- `/manage/projects/{id}/content-types` — read: viewer; write: administrator.
- `/manage/projects/{id}/content/{collection}` — read: viewer; create:
  author; update/publish/unpublish: editor.
- Delivery API (`/api/v1/projects/{id}/content/...`) — viewer.
- A non-member and a too-low-role member get the identical 403 — project
  membership itself isn't distinguishable from "action not allowed", so a
  project id can't be probed to learn whether it exists.

Verified in this session with curl: non-owner blocked from owner-only
routes (403); non-member blocked from a project's routes (403) and sees
an empty project list; after being added as viewer, can read but not
write (403 on create); after being upgraded to editor, can create and the
entry's `created_by` is correctly attributed to them.

Not done: no UI for member management yet (API only — the admin frontend
has no page for it); no field-level permissions; no "author can only
edit their own entries" check (author can create, but update/publish
require editor regardless of who created the entry — simpler for now,
tracked as a real gap, not an oversight).

## Tests — now real, IMPLEMENTED and VERIFIED

- `internal/schema/schema_test.go` — table-driven unit tests for
  `Definition.Validate` and `ValidateEntry` (schema validation, no
  database needed).
- `internal/auth/password_test.go` — Argon2id hash/verify roundtrip,
  distinct-salt check, malformed-hash rejection.
- `tests/integration/*_test.go` — real integration tests against a real
  PostgreSQL database (`cms_test`, created alongside the dev `cms`
  database; `TEST_DATABASE_URL` overrides it), wiring the actual router
  and every real service (no mocks), driven through `httptest.Server` +
  plain HTTP requests with manual cookie handling. Skips (not fails) if
  the test database can't be reached. Covers: the full vertical slice
  (project → content type → draft → publish → delivery visibility),
  schema validation rejecting missing/unexpected fields through the real
  HTTP path, owner-only routes rejecting a non-owner, and the RBAC suite
  specifically asked for — cross-project isolation (non-member 403s and
  sees an empty project list), the viewer/editor role floor, and
  `created_by` attribution.
- All of `go test ./...`, `go vet ./...`, and `gofmt -l .` are clean.
- Caught one real bug writing these: the test client's cookie-jar logic
  checked `resp.Cookies() != nil`, but `http.Response.Cookies()` returns
  a non-nil *empty* slice when a response sets no cookies — so every
  request after login was silently dropping the session cookie. Fixed to
  check `len(cookies) > 0`. Noted here because it's a easy mistake to
  make again in any other hand-rolled Go HTTP client/test code in this
  repo.

Still missing: `go test -race` was attempted but can't run in this
environment (`-race` needs cgo, which needs a C compiler; none is
installed) — needs a machine with gcc/clang, or that installed here,
before it can run. No Playwright/Vitest for the frontend, no fuzz tests
for the query parser (the query parser itself doesn't exist yet), no CI
pipeline wiring any of this to run automatically on push.

## Known gaps / explicitly NOT_STARTED (tracked, not forgotten)

Everything below is real scope from the original spec that this session
did not implement. None of it is silently dropped — it's the next work.

- **Field types**: only short_text, long_text, rich_text, integer,
  decimal, boolean, email, url, date, datetime, enumeration, json.
  Missing: big_integer, float (separate from decimal), color, slug/UID,
  multi-select, single/multiple media, relation, component, repeatable
  component, dynamic zone, field groups, schema presets.
- **Row-level security**: isolation is enforced in application code (every
  query scoped by project_id + the new role middleware), not at the
  database level. No Postgres RLS policies yet — see PROGRESS.md's
  "exact next steps" below.
- **Schema migrations**: content types can be created but not edited
  (rename/remove field, change type, etc.) or migrated. No migration plan
  generation, no destructive-change confirmation flow.
- **Relations, components, dynamic zones**: not implemented at all.
- **Localization**: `locale` column exists and is threaded through, but
  there's no locale configuration, fallback, or per-locale publish UI.
- **Revisions**: publish overwrites the single published row; there is no
  revision history, diff, or restore.
- **Media library**: not started.
- **Auth hardening**: no MFA, no password reset flow, no login history, no
  API tokens (scoped/expiring), no rate limiting / brute-force protection.
- **Webhooks, background jobs, search**: not started.
- **TypeScript SDK / type generation**: `packages/sdk` is an empty
  placeholder.
- **Query engine**: delivery API has basic limit/offset pagination only —
  no field selection, relation expansion, filter operators, sort, or
  cursor pagination yet.
- **Caching (Redis)**: not wired up anywhere yet.
- **Benchmarks, k6 suite, Strapi comparison**: not started.
- **Tests**: zero automated tests exist yet (no Go unit/integration
  tests, no Vitest, no Playwright). Everything above was verified manually
  via curl and the browser pane in this session, not via a test suite.
- **CORS/production config**: CORS origins are read from an env var and
  default to none (safe default) but aren't documented beyond the README.
- **go:embed / single-binary deploy, Caddy config, backup/restore
  scripts**: not started.
- **Docker/Compose**: written but UNVERIFIED (see Environment above).

## Exact next steps

1. Extend the schema engine: relations next (needed by almost everything
   else — components, dynamic zones, and the query engine's expansion all
   depend on it), then media (single/multiple media fields need the media
   library to exist first). Add integration tests for relations as they're
   built, following the pattern in `tests/integration/`, not after.
2. Admin UI for member management (currently API-only).
3. Install a C compiler so `go test -race` can actually run, then run it.
4. Get Docker Desktop actually usable (reboot to finish WSL2 setup), then
   verify `Dockerfile` and `deployments/docker-compose.dev.yml` actually
   build and run.
5. Wire CI (even a minimal GitHub Actions workflow running `go test ./...`
   against a Postgres service container, plus the frontend typecheck) —
   there isn't one yet, so none of the above is enforced automatically.
