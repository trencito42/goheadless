# PROGRESS

Last updated: 2026-11-25 (relation integrity hardening: documents table,
transaction-scoped validation, correct cardinality, locale-aware
expansion, unpublished-target filtering, concurrency-safe publish).

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
- CI: `.github/workflows/ci.yml` — IMPLEMENTED, **unverified by an actual
  Actions run** (this environment has no `gh` CLI and no way to trigger
  or observe GitHub Actions directly; it's pushed and will run on GitHub
  on the next push/PR, but "the YAML is correct" and "the workflow
  passed on GitHub" are different claims — only the first is backed by
  anything done in this session). Two jobs: `backend` (Postgres 17
  service container, `go build`/`vet`/`gofmt -l`/`test ./...` with
  `TEST_DATABASE_URL` set explicitly — which is exactly the case the
  test-skip-vs-fail fix above is for) and `frontend` (`pnpm install
  --frozen-lockfile`, design-system build, `tsc` typecheck, `vite
  build`). Next person to push should confirm it actually goes green on
  GitHub and fix anything this local-only review couldn't catch. Now
  also runs `go test -race ./...` on this Linux job specifically,
  since the Windows machine this project is otherwise developed on has
  no C compiler for cgo (see Tests below) — Linux runners ship gcc, so
  this is the one place `-race` actually runs, not a second skip.
- `docs/adr/` — stale note removed: three ADRs exist (0001 hybrid
  storage, 0002 relations, 0003 document identity/relation integrity),
  not zero.

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
has no page for it); no field-level permissions.

**Fixed** (was previously tracked here as a known gap, found again in an
independent audit): Author-role callers could edit *any* draft in the
project, not just their own. `Service.Update` now takes the caller's id
and whether ownership should be required; the HTTP handler passes
`requireOwnership = role < Editor`. An Author editing someone else's
draft now gets 403 (`content.ErrNotOwner`); Editor and above still edit
anyone's, unchanged. VERIFIED by
`TestAuthorCannotEditAnotherAuthorsDraftButEditorCan` in
`tests/integration/relations_test.go` (real HTTP requests as two
different author-role users plus an editor-promoted one, against real
Postgres).

## Relations — IMPLEMENTED and VERIFIED (migrations/0003, 0004)

A `relation` field type (`one_to_one`, `one_to_many`, `many_to_one`,
`many_to_many`), stored in a dedicated `entry_relations` table rather
than the JSONB `data` column, plus a real `documents` table backing
both `entries.document_id` and `entry_relations.to_document_id` with
foreign keys. Full design rationale in `docs/adr/0002-relations.md` and
`docs/adr/0003-document-identity.md`; API shape in `docs/API.md`.

- Schema: `schema.RelationConfig` (`targetContentType`, `relationType`),
  validated structurally (`Definition.Validate`) and by value shape
  (`validateRelationValue` — a single document-id string for to-one, a
  list of them for to-many). Every field (not just relations) now also
  carries a stable `schema.Field.ID`, assigned once at creation and
  never changed — `entry_relations` is keyed by this, not by the
  field's display name, so a future field rename (schema migrations
  are still NOT_STARTED) won't orphan stored relations.
- Registry: creating a content type with a relation field checks the
  target content type exists in the **same project** (or is the type
  being created itself, for self-reference) before accepting it.
- **Documents are a real identity, not just a shared UUID value**
  (fixed in an independent audit of the original relations work):
  `documents` is one row per `document_id`; `entries.document_id` and
  `entry_relations.to_document_id` are real foreign keys to it, both
  `ON DELETE CASCADE`. There is still no "delete a document" API, so
  this cascade isn't reachable through the HTTP API yet, but it's
  VERIFIED directly against the database by
  `TestDocumentDeletionCascadesToEntriesAndRelations` (delete a
  `documents` row → its own entries rows and any relation pointing at
  it both disappear, with no application code needed).
- Content service: relation values are split out of the submitted
  `data` before it's stored as JSONB (`splitRelationFields`), validated
  against the real `entries` table — target must exist, belong to the
  same project, and match the configured target content type
  (`validateRelationTargets`) — and persisted as `entry_relations` rows,
  all **inside the same transaction** as the entries write
  (`setRelations`). Previously `validateRelationTargets` ran against the
  pool directly, outside that transaction — an audit finding, now fixed;
  a real FK backs the check up regardless.
- **Draft/published are independent snapshots**: `entry_relations` is
  keyed by the specific entries *row* id (`from_entry_id`), not the
  document id. Editing a draft's relations never changes what's
  published until `Publish` explicitly copies the draft's current
  relation rows onto the published row (`copyRelations`), inside the
  same transaction that promotes its data. That transaction now takes a
  `FOR UPDATE` lock on the draft row first (another audit finding, now
  fixed) — without it, a concurrent `Update()` could commit a new
  data+relation snapshot in the gap between `Publish` reading the
  draft's data and reading its relations, producing a published
  snapshot that was never actually true at any single instant.
  `Unpublish` deleting the published row cascades (FK `ON DELETE
  CASCADE`) to clean up its relation rows automatically.
- Reads (`List`/`Get`) batch-load every entry's relations in one query
  (`attachRelations`, keyed by a batch of row ids — never one query per
  entry), returning bare document-id stub(s) by default.
- **Delivery reads never deliver an unpublished target's id**: a
  published entry's relation stub is checked against which of its
  referenced documents actually have a published row *in the requested
  locale* right now (`filterUndeliverableTargets`) — nulling a to-one
  stub or dropping entries from a to-many list if the target was
  unpublished (or never published in that locale) after the source's
  own last publish. The management/draft API still shows the raw stub,
  since editors need to see and fix a dangling reference, not have it
  hidden. VERIFIED by `TestDeliveryFiltersUnpublishedRelationTargets`.
- `?include=fieldName` expands a relation field into the full related
  entry's data, one query per included field (not per entry, not
  recursive — an included entry's own relations stay as stubs, which is
  what keeps this bounded to one level without needing cycle
  detection). The target lookup is now scoped to the same locale as the
  entries being expanded (previously it wasn't — an audit finding: since
  `document_id` alone doesn't disambiguate locale, a target with
  published rows in several locales could have had an arbitrary one
  returned). VERIFIED by `TestLocaleAwareRelationExpansion`.
- **Private fields never leak through relation expansion or the
  delivery API**: `schema.Field.Private` (existed before but was never
  actually enforced anywhere — also found in an earlier audit) is
  stripped from (a) every delivery-API response
  (`ListPublished`/`GetPublished`) and (b) every relation-expansion
  embed, unconditionally, regardless of whether the outer read is a
  draft or published one. Private fields remain visible on the
  authenticated management/draft API, since editors need to see and
  edit them.
- **Cardinality, corrected**: `many_to_one` and `many_to_many` let many
  source documents reference the same target — that was always correct
  and is now explicitly regression-tested
  (`TestManyToOneAllowsMultipleSourcesToSameTarget`), not just assumed.
  `one_to_one` reverse-uniqueness ("at most one document may claim this
  target through this field") is now a **real, race-free database
  constraint** — a partial unique index scoped to `status = 'published'`
  (so a document's own draft+published rows legitimately sharing a
  target, which is what publishing *is*, doesn't self-conflict; two
  different documents' *drafts* may transiently both point at the same
  target, with the conflict becoming real only once one of them
  publishes). A violation surfaces as a 409 (`ErrRelationConflict`), not
  a raw database error. VERIFIED by
  `TestOneToOneRejectsSecondPublishedReferenceButAllowsConcurrentDrafts`.
  Full rationale, including why the earlier version of this paragraph
  was wrong to lump `many_to_one` in with this gap, is in
  `docs/adr/0003-document-identity.md`.
- Cross-project relation targets are rejected — not just checked, but
  structurally impossible, since the target content type is always
  resolved through `registry.Get(callerProjectID, ...)`, so there is no
  code path that could ever look at another project's rows. VERIFIED
  by `TestRelationCrossProjectReferenceRejected`.
- Self-referencing relations (a content type relating to itself) work.
  VERIFIED by `TestRelationSelfReferencingAndToMany`.

VERIFIED end-to-end against real Postgres in
`tests/integration/relations_test.go` (15 tests covering everything
above — create/fetch with a stub and an expanded relation,
cross-project rejection, self-reference + to-many, draft/published
snapshot independence, author ownership, private-field redaction,
many_to_one multi-source, one_to_one conflict + concurrent-drafts
allowance, unpublished-target filtering, locale-aware expansion, and
document-deletion cascade).

**Known gap, still not solved**: a genuine *concurrency* regression
test (two goroutines racing `Update`/`Publish` against the same
document to prove the `FOR UPDATE` lock actually serializes them, not
just that the code calls it) was not written in this pass — writing a
deterministic one needs controlled timing/goroutine orchestration that
didn't fit this session's scope. The lock's correctness is argued from
reading the code and from Postgres's documented row-lock semantics, not
from a race test. Also still not done: components, dynamic zones
(relations were the prerequisite for both; now unblocked); "delete a
document" has no API yet, so the cascade FKs above are verified at the
database level but not reachable through HTTP.

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
  plain HTTP requests with manual cookie handling. Covers: the full
  vertical slice (project → content type → draft → publish → delivery
  visibility), schema validation rejecting missing/unexpected fields
  through the real HTTP path, owner-only routes rejecting a non-owner,
  the RBAC suite (cross-project isolation, viewer/editor role floor,
  `created_by` attribution), and the full relations suite (15 tests —
  see the "Relations" section above for what each one covers).
- **Fixed (independent audit finding)**: the test harness used to
  `t.Skip` whenever it couldn't reach the test database, with no
  distinction between "no `TEST_DATABASE_URL` set, probably a laptop
  with no Postgres running" and "CI explicitly set `TEST_DATABASE_URL`
  and the connection still failed" — the latter is a real environment
  problem that was silently swallowed as a pass. `newTestEnv` now
  distinguishes the two: it still skips when the var is unset (so
  `go test ./...` passes on a machine with no Postgres), but calls
  `t.Fatalf` when the var **is** set and the connection (including an
  explicit `pool.Ping`) fails. VERIFIED by manually pointing
  `TEST_DATABASE_URL` at a wrong password / nonexistent database and
  confirming the suite fails loudly instead of reporting skips.
- All of `go test ./...`, `go vet ./...`, and `gofmt -l .` are clean.
- Caught one real bug writing these: the test client's cookie-jar logic
  checked `resp.Cookies() != nil`, but `http.Response.Cookies()` returns
  a non-nil *empty* slice when a response sets no cookies — so every
  request after login was silently dropping the session cookie. Fixed to
  check `len(cookies) > 0`. Noted here because it's a easy mistake to
  make again in any other hand-rolled Go HTTP client/test code in this
  repo.

Still missing locally: `go test -race` can't run on this Windows dev
machine (`-race` needs cgo, which needs a C compiler; none is
installed) — but it now runs in CI on the Linux `backend` job (see
Phase 0 above), so it isn't permanently stuck, just not runnable from
this specific environment. No Playwright/Vitest for the frontend, no
fuzz tests for the query parser (the query parser itself doesn't exist
yet).

## Known gaps / explicitly NOT_STARTED (tracked, not forgotten)

Everything below is real scope from the original spec that this session
did not implement. None of it is silently dropped — it's the next work.

- **Field types**: short_text, long_text, rich_text, integer, decimal,
  boolean, email, url, date, datetime, enumeration, json, and now
  relation. Still missing: big_integer, float (separate from decimal),
  color, slug/UID, multi-select, single/multiple media, component,
  repeatable component, dynamic zone, field groups, schema presets.
- **Row-level security**: isolation is enforced in application code (every
  query scoped by project_id + the new role middleware), not at the
  database level. No Postgres RLS policies yet — see PROGRESS.md's
  "exact next steps" below.
- **Schema migrations**: content types can be created but not edited
  (rename/remove field, change type, etc.) or migrated. No migration plan
  generation, no destructive-change confirmation flow.
- **Relations**: implemented, including document identity/FK integrity,
  correct cardinality (including DB-enforced one_to_one
  reverse-uniqueness), locale-aware expansion, and unpublished-target
  filtering (see the dedicated section above). The one remaining gap
  there is a real concurrency regression test, not a missing feature.
  **Components, dynamic zones**: still not implemented at all
  (relations were a prerequisite for both; now unblocked).
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
- **Tests**: stale line removed — see the dedicated "Tests" section
  above; Go unit and integration tests exist and run in CI. Still
  missing: Vitest/Playwright for the frontend (the frontend itself was
  only ever verified manually via the browser pane).
- **CORS/production config**: CORS origins are read from an env var and
  default to none (safe default) but aren't documented beyond the README.
- **go:embed / single-binary deploy, Caddy config, backup/restore
  scripts**: not started.
- **Docker/Compose**: written but UNVERIFIED (see Environment above).

## Exact next steps

1. Components and dynamic zones next (relations, their prerequisite, are
   now done — including the integrity hardening an independent audit
   asked for), then media (single/multiple media fields need the media
   library to exist first). Use `schema.Field.ID` for their own nested
   field identity from the start, not display names, for the same
   rename-safety reason relations now do. Add integration tests as
   they're built, following the pattern in `tests/integration/`, not
   after.
2. Admin UI: no relations UI yet (picking a related entry, seeing
   expanded relations), no member-management UI (currently API-only).
   Both API-only today, both real gaps.
3. A real concurrency regression test for `Publish`'s `FOR UPDATE` lock
   (two goroutines racing `Update`/`Publish` against the same document)
   — the known gap called out at the end of the Relations section.
4. A "delete a document" API — the FK cascades added in
   migrations/0004 are verified at the database level
   (`TestDocumentDeletionCascadesToEntriesAndRelations`) but there's
   nothing in the HTTP API that reaches them yet.
5. Install a C compiler on this dev machine so `go test -race` can run
   locally too, not just in CI.
6. Get Docker Desktop actually usable (reboot to finish WSL2 setup), then
   verify `Dockerfile` and `deployments/docker-compose.dev.yml` actually
   build and run.
7. `.github/workflows/ci.yml` now exists (see Phase 0 above) but its
   first real run on GitHub hasn't been observed from this environment
   — confirm it's actually green, not just plausible-looking YAML.
