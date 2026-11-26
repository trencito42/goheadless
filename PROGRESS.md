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
- CI: `.github/workflows/ci.yml` — IMPLEMENTED and, as of commit
  `3ae25cc`, **confirmed green on an actual GitHub Actions run** (this
  environment still has no `gh` CLI and can't observe Actions directly;
  the pass was confirmed externally, not by anything run from this
  session — noted plainly since "the YAML is correct" and "the
  workflow passed on GitHub" were, until now, different claims). Two
  jobs: `backend` (Postgres 17 service container,
  `go build`/`vet`/`gofmt -l`/`test ./...` with `TEST_DATABASE_URL` set
  explicitly — which is exactly the case the test-skip-vs-fail fix
  above is for) and `frontend` (`pnpm install --frozen-lockfile`,
  design-system build, `tsc` typecheck, `vite build`). Every commit
  after `3ae25cc` should still get its own confirmation rather than
  assuming green stays green. Now also runs `go test -race ./...` on
  this Linux job specifically, since the Windows machine this project
  is otherwise developed on has no C compiler for cgo (see Tests
  below) — Linux runners ship gcc, so this is the one place `-race`
  actually runs, not a second skip.
- `docs/adr/` — stale note removed: five ADRs exist (0001 hybrid
  storage, 0002 relations, 0003 document identity/relation integrity,
  0004 field-id backfill/cardinality/deletion-policy, 0005
  components/dynamic zones), not zero.

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

## Relations — IMPLEMENTED and VERIFIED (migrations/0003, 0004, 0005)

A `relation` field type (`one_to_one`, `one_to_many`, `many_to_one`,
`many_to_many`), stored in a dedicated `entry_relations` table rather
than the JSONB `data` column, plus a real `documents` table backing
both `entries.document_id` and `entry_relations.to_document_id` with
foreign keys. Full design rationale in `docs/adr/0002-relations.md` and
`docs/adr/0003-document-identity.md`; API shape in `docs/API.md`.

- Schema: `schema.RelationConfig` (`targetContentType`, `relationType`,
  optional `onDelete`), validated structurally (`Definition.Validate`)
  and by value shape (`validateRelationValue` — a single document-id
  string for to-one, a list of them for to-many). Every field (not just
  relations) now also carries a stable `schema.Field.ID`, assigned once
  at creation and never changed — `entry_relations` is keyed by this,
  not by the field's display name, so a future field rename (schema
  migrations are still NOT_STARTED) won't orphan stored relations.
  migrations/0005 backfills an id for any field that somehow still
  lacks one, and `Definition.Validate` now rejects two fields sharing a
  client-supplied id (an independent audit finding — stored-relation
  lookups keyed by field id would otherwise be ambiguous between them).
- Registry: creating a content type with a relation field checks the
  target content type exists in the **same project** (or is the type
  being created itself, for self-reference) before accepting it.
- **Documents are a real identity, not just a shared UUID value**
  (fixed in an independent audit of the original relations work):
  `documents` is one row per `document_id`; `entries.document_id` and
  `entry_relations.to_document_id` are real foreign keys to it, both
  `ON DELETE CASCADE`. migrations/0005 adds composite `UNIQUE (id,
  project_id[, content_type_id])` on `documents` plus composite foreign
  keys from `entries`/`entry_relations` against them, so Postgres
  itself now rejects a write tagging an entries/relations row with a
  document from the wrong project or content type — not just
  application code. VERIFIED directly against the database by
  `TestDocumentDeletionCascadesToEntriesAndRelations` (delete a
  `documents` row → its own entries rows and any relation pointing at
  it both disappear, with no application code needed) and, through the
  actual HTTP API this time, by the `DELETE .../content/{collection}/
  {documentID}` endpoint below.
- **Document deletion with explicit per-field policies**:
  `Service.DeleteDocument` (`internal/content/delete.go`), exposed as
  `DELETE /api/v1/manage/projects/{projectID}/content/{collection}/
  {documentID}` (editor role or above) — the "delete a document" API
  that was a known gap through the last two sessions. A relation
  field's `onDelete` (`schema.DeletePolicy`: `restrict` — the default
  when unset, `set_null`, or `cascade`) governs what happens to
  documents still referencing the one being deleted: `restrict` aborts
  the whole deletion (409 `ErrDocumentReferenced`) if any reference
  exists at all; `set_null` lets it proceed (the FK cascade removes the
  dangling `entry_relations` rows); `cascade` lets it proceed and also
  deletes every referencing document, recursively, cycle-safely, all in
  one transaction. VERIFIED by `TestDeleteDocumentRestrictPolicyBlocksDeletion`,
  `TestDeleteDocumentSetNullPolicyClearsReference`, and
  `TestDeleteDocumentCascadePolicyDeletesReferencingDocument`.
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
  **Snapshot-consistent**: the entries query and every relation-reading
  step (`attachRelations`/`filterUndeliverableTargets`/`expandIncludes`)
  now run inside one `REPEATABLE READ`, read-only transaction, not as
  four independent statements against the pool — an audit finding:
  without it, a publish landing in the gap between two of those four
  queries could produce a response combining an entries row from
  before that publish with relation/included data from after it (or
  vice versa), a state that was never actually true at any single
  instant.
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
  `one_to_one` **and `one_to_many`** reverse-uniqueness ("at most one
  document may claim this target through this field") is a **real,
  race-free, locale-scoped database constraint** — a partial unique
  index on `(field_id, to_document_id, locale)` scoped to `status =
  'published'` (so a document's own draft+published rows legitimately
  sharing a target, which is what publishing *is*, doesn't self-conflict;
  two different documents' *drafts* may transiently both point at the
  same target, with the conflict becoming real only once one of them
  publishes) and scoped by `locale` (migrations/0005 — without it, two
  locale variants of the *same* document both publishing to the same
  target looked identical to two *different* documents claiming it, and
  was wrongly rejected; `one_to_many` originally had no reverse-uniqueness
  constraint at all — migrations/0004 only covered `one_to_one`). A
  violation surfaces as a 409 (`ErrRelationConflict`), not a raw
  database error. VERIFIED by
  `TestOneToOneRejectsSecondPublishedReferenceButAllowsConcurrentDrafts`,
  `TestOneToOneLocaleVariantsOfSameDocumentDontConflict`, and
  `TestOneToManyReverseCardinality`. Full rationale, including the
  corrections to earlier ADRs, is in `docs/adr/0003-document-identity.md`
  and `docs/adr/0004-field-id-backfill-cardinality-deletion-policy.md`.
- Cross-project relation targets are rejected — not just checked, but
  structurally impossible, since the target content type is always
  resolved through `registry.Get(callerProjectID, ...)`, so there is no
  code path that could ever look at another project's rows. VERIFIED
  by `TestRelationCrossProjectReferenceRejected`.
- Self-referencing relations (a content type relating to itself) work.
  VERIFIED by `TestRelationSelfReferencingAndToMany`.

VERIFIED end-to-end against real Postgres in
`tests/integration/relations_test.go` (17 tests covering everything
above — create/fetch with a stub and an expanded relation,
cross-project rejection, self-reference + to-many, draft/published
snapshot independence, author ownership, private-field redaction,
many_to_one multi-source, one_to_one conflict + concurrent-drafts
allowance, one_to_one locale-variant non-conflict, one_to_many reverse
cardinality, unpublished-target filtering, locale-aware expansion,
document-deletion cascade, all three deletion policies, and the
concurrent-publish-vs-update lock) plus `internal/schema/schema_test.go`
(duplicate field id and onDelete-policy validation).

**Former known gap, now closed**: a genuine *concurrency* regression
test for `Publish`'s `FOR UPDATE` lock —
`TestConcurrentPublishBlocksUpdateUntilCommit` holds that exact lock
itself via a raw SQL transaction and asserts a concurrent `Update`
blocks the entire time the lock is held, only completing once it's
released, deterministically (no sleep-and-hope timing: it asserts
non-completion within a bounded wait, then waits unboundedly for
completion after releasing the lock). The lock's correctness is no
longer argued from code review alone.

## Components and dynamic zones — IMPLEMENTED and VERIFIED (no migration needed)

`component` (including `repeatable`) and `dynamic_zone` field types.
Full design rationale in `docs/adr/0005-components.md`; API shape in
`docs/API.md`.

- Stored inline in the owning entry's `data` JSONB — no new table, no
  new migration, unlike relations. A component's value is a nested
  JSON object (or list of them, if `repeatable`); a dynamic zone's
  value is always a list, each item tagged `"__component": "<name>"`
  saying which of the field's named component types it is.
- `schema.ComponentConfig`/`DynamicZoneConfig`, validated recursively:
  `Definition.Validate` walks into a component's/each dynamic-zone
  component's own `Fields` the same way it validates a top-level
  schema, and `ValidateEntry` validates nested data the same way
  (`validateComponentValue`/`validateDynamicZoneValue`), so a missing
  required nested field or a dynamic-zone item naming an unknown
  component is rejected with a message naming exactly where in the
  nesting it failed.
- `schema.Field.ID` is assigned recursively (`Registry.Create`'s
  `assignFieldIDs` walks into nested fields too), and duplicate-id
  rejection is checked against one id set shared across the whole
  recursion — a nested field's id can't collide with a sibling
  anywhere else in the schema, component boundaries included.
- **Deliberate scope cuts, not oversights** (see ADR 0005 for why):
  a component/dynamic zone's own fields may not themselves be a
  relation (no from-entry identity for a nested JSON value to key
  `entry_relations` by) or another component/dynamic zone (bounds
  nesting to one level for this first pass). Both are rejected with a
  400 at content-type creation, verified by
  `TestComponentRejectsNestedRelationField`.

VERIFIED end-to-end against real Postgres in
`tests/integration/components_test.go` (a single content type with a
component, a repeatable component, and a dynamic zone with two allowed
component types; create + round-trip through the real HTTP API;
missing-required-nested-field and unknown-dynamic-zone-component
rejection) plus `internal/schema/schema_test.go`'s structural and
`ValidateEntry` cases for both field types.

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
  `created_by` attribution), the full relations suite (17 tests — see
  the "Relations" section above for what each one covers), and the
  components/dynamic-zones suite (2 tests — see that section above).
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

## Admin UI polish — IMPLEMENTED and VERIFIED

- **b4 dropdown wrapping bug (global, user-reported)**: `Select`'s popup
  CSS (`packages/b4/packages/react/src/components/Select/Select.css`)
  fixed its width to `var(--anchor-width)` (the trigger's own width),
  which is sized to the trigger's *selected* value, not the widest
  option label — long labels (e.g. "Dynamic zone") wrapped letter by
  letter. Fixed to `min-inline-size: var(--anchor-width)` +
  `width: max-content` + `max-inline-size: var(--available-width)`.
  `Combobox` and `MultiSelect` reuse the same `.b4-select__popup` class,
  so this one fix covers all three. Committed and pushed in the `b4`
  submodule (`6c2a72f`), submodule pointer bumped in this repo
  (`368f095`). VERIFIED visually in the browser pane — option labels
  now render on one line.
- **Admin app was under-using the b4 component library**: it only used
  `Card`, `Button`, `TextField`, `Badge`, `Select`, `Tabs`, `Checkbox`,
  `EmptyState` — all raw-HTML/inline-styled for page headers, nav, and
  tables (`<h1>`, `<nav><button>`, `<table>`, `<div onClick>`), which is
  why it looked rudimentary next to what b4 actually offers. Swapped in:
  - `PageHeader` for every page title (`Projects`, `ProjectDetail`)
    instead of a bare inline-styled `<h1>`.
  - `Sidebar` for the app nav (`App.tsx`) instead of a raw `<nav>` of
    `<button>`s — real `aria-current="page"` on the active item.
  - `InteractiveCard` for the clickable project list instead of `Card`
    + manual `onClick`/`cursor:pointer`.
  - `DataTable` for the content entries list instead of a raw
    `<table>`.
  - Added `apps/admin/src/app.css` (`.page-content` utility) instead of
    repeating the same inline padding/gap/max-width on every page.
  Files: `apps/admin/src/{App.tsx,main.tsx,app.css,pages/Projects.tsx,
  pages/ProjectDetail.tsx}`. VERIFIED: `pnpm typecheck` clean, and both
  the Projects page and a project's detail page (Blog) visually
  confirmed in the browser pane against the real backend. Committed and
  pushed (`35ec711`).
  Still rudimentary / explicitly not touched by this pass: no new UI
  surface area was added — relations picker, component/dynamic-zone
  editing, and member management are still plain forms or missing
  entirely (see "Exact next steps" below, item 4). This pass only
  improved the visual quality of pages that already existed.

## Known gaps / explicitly NOT_STARTED (tracked, not forgotten)

Everything below is real scope from the original spec that this session
did not implement. None of it is silently dropped — it's the next work.

- **Field types**: short_text, long_text, rich_text, integer, decimal,
  boolean, email, url, date, datetime, enumeration, json, relation,
  and now component (including repeatable) and dynamic_zone — see the
  dedicated "Components and dynamic zones" section below. Still
  missing: big_integer, float (separate from decimal), color, slug/UID,
  multi-select, single/multiple media, field groups, schema presets.
- **Row-level security**: isolation is enforced in application code (every
  query scoped by project_id + the new role middleware), not at the
  database level. No Postgres RLS policies yet — see PROGRESS.md's
  "exact next steps" below.
- **Schema migrations**: content types can be created but not edited
  (rename/remove field, change type, etc.) or migrated. No migration plan
  generation, no destructive-change confirmation flow.
- **Relations**: implemented, including document identity/FK integrity
  (now with composite project/content-type consistency constraints),
  correct and locale-aware cardinality (DB-enforced reverse-uniqueness
  for both `one_to_one` and `one_to_many`), snapshot-consistent reads,
  unpublished-target filtering, document deletion with explicit
  restrict/set_null/cascade policies, and a deterministic concurrency
  regression test for the publish lock (see the dedicated section
  above). No known gaps remain in this area beyond the field-type and
  localization scope tracked elsewhere on this list.
- **Components, dynamic zones**: implemented — see the dedicated
  section below. Known gaps: one level of nesting only (a component
  can't contain another component/dynamic zone), and neither may
  contain a relation field (no from-entry identity for a nested JSON
  value to key `entry_relations` by). Both are explicit scope cuts,
  not oversights — see `docs/adr/0005-components.md`.
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

1. Components and dynamic zones are now done (no migration needed —
   see the dedicated section above and ADR 0005). Media next
   (single/multiple media fields need the media library to exist
   first).
2. A schema migration engine: content types can be created but not
   edited (rename/remove field, change type) or migrated — no
   migration plan generation, no destructive-change confirmation flow.
   Should build on `schema.Field.ID` (fields already survive a rename
   at the storage layer, including nested component fields now; there's
   just no API to perform one yet).
3. A second pass at components/dynamic zones: lift the one-level
   nesting limit, and design how a relation field could live inside a
   component (needs component instances to have their own identity —
   deferred, not solved, in ADR 0005).
4. Admin UI: no relations UI yet (picking a related entry, seeing
   expanded relations), no component/dynamic-zone editing UI, no
   member-management UI (currently API-only). All three real gaps.
5. Install a C compiler on this dev machine so `go test -race` can run
   locally too, not just in CI.
6. Get Docker Desktop actually usable (reboot to finish WSL2 setup), then
   verify `Dockerfile` and `deployments/docker-compose.dev.yml` actually
   build and run.
