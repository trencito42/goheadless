# ADR 0004: Field-id backfill, composite consistency, locale-aware/one_to_many cardinality, explicit deletion policies

## Status

Accepted. Corrects two gaps in ADR 0003 (`migrations/0004`), and adds
new ground that ADR 0002/0003 never covered: explicit per-field
deletion policies and a read-consistency snapshot for list/get.

## Context

A further independent audit of `migrations/0004` and the relation
engine as a whole raised findings that split into four groups.

### 1. Field ids could still be missing or ambiguous

`schema.Field.ID` is only ever assigned by `Registry.Create` for a
field whose client-supplied id is the zero UUID. Two gaps followed:
nothing backfilled an id for a `content_types` row written before that
code existed (none in this repo's own history, but nothing stopped one
in a database that predates this feature), and nothing rejected a
client deliberately supplying the *same* id for two different fields —
which would make every stored-relation lookup keyed by field id (every
one of them, since ADR 0003) genuinely ambiguous between those two
fields.

### 2. Project/content-type consistency was application-code-only

`entries.document_id` and `entry_relations.{from,to}_document_id` only
had to point at *some* row in `documents` (ADR 0003's foreign keys).
Nothing stopped a document that actually belongs to project A /
content type "post" from being written onto an `entries` row tagged
project B / content type "comment" — application code never does this,
but only application code was stopping it.

### 3. The one_to_one/one_to_many reverse-uniqueness index had two bugs

Covered in detail in the correction note added to ADR 0003: it didn't
scope by locale (so two locale variants of the same document
publishing to the same target looked like two different documents
claiming it, and was wrongly rejected), and it only covered
`one_to_one`, leaving `one_to_many` — which needs the identical "at
most one parent per target" invariant — with no database enforcement
at all.

### 4. No way to delete a document, and no policy for what should happen to what references it

There was no "delete a document" endpoint at all (tracked as a known
gap since ADR 0003). Once one exists, deleting a document that's still
referenced by other documents' relation fields needs an actual policy:
silently cascading the DB foreign key would always just delete the
*relation row*, never giving a schema author the choice to reject the
deletion, or to delete the referencing document too.

### 5. List/Get read three things independently

`internal/content.Service.List`/`Get` ran the entries query,
`attachRelations`, `filterUndeliverableTargets`, and `expandIncludes`
as four separate statements against the pool, each seeing whatever had
committed by the time *it* ran. A publish landing in between two of
those four queries could produce a response that combines an entries
row from before that publish with relation/included data from after it
(or vice versa) — a snapshot that was never actually true at any single
instant.

## Decision

**(1) Field-id backfill and duplicate rejection** —
`migrations/0005_field_id_backfill_and_relation_consistency.sql`
backfills a fresh id for any field in any `content_types` row missing
one (non-destructively: it only ever adds the `id` key to a field
object that doesn't have one). `schema.Definition.Validate` now rejects
two fields sharing a non-zero id (`internal/schema/schema.go`), checked
before `Registry.Create` assigns ids to zero-valued fields, so a
client-caused collision is caught before it reaches storage.

**(2) Composite consistency constraints** — the same migration adds
`UNIQUE (id, project_id)` and `UNIQUE (id, project_id, content_type_id)`
on `documents`, and composite foreign keys from `entries` and
`entry_relations` against them. Postgres itself now rejects a write
that tags an entries/relations row with a document that doesn't
actually belong to the same project (and, for entries, the same
content type) — defense in depth beneath the application-level checks,
not a replacement for them.

**(3) Locale-aware, one_to_many-inclusive reverse uniqueness** — adds an
`entry_relations.locale` column (backfilled from the owning entries
row), drops the old `(field_id, to_document_id)` index, and replaces it
with:
```sql
CREATE UNIQUE INDEX entry_relations_reverse_unique_published_target_idx
    ON entry_relations (field_id, to_document_id, locale)
    WHERE status = 'published' AND relation_type IN ('one_to_one', 'one_to_many');
```
`setRelations`/`copyRelations` (`internal/content/relations.go`) now
take and store the owning entry's `locale` alongside everything else.

**(4) Explicit per-field deletion policies** — `RelationConfig` gains
`OnDelete` (`schema.DeletePolicy`: `restrict` — the default when unset,
`set_null`, or `cascade`). `internal/content/delete.go` adds
`Service.DeleteDocument`, exposed as `DELETE
/api/v1/manage/projects/{projectID}/content/{collection}/{documentID}`
(editor role or above, matching publish/unpublish). For the document
being deleted, it finds every relation field (across every content
type in the project) currently referencing it, groups by field, and
applies that field's policy:
- `restrict`: aborts the whole deletion with `ErrDocumentReferenced`
  (409) if any reference exists at all — draft or published.
- `set_null`: lets the deletion proceed; nothing else to do, since the
  foreign key's `ON DELETE CASCADE` on `to_document_id` removes those
  `entry_relations` rows the instant the target is gone.
- `cascade`: lets the deletion proceed and recursively deletes every
  document that references the target through that field, applying
  the same policy logic to each (a `visited` set makes this cycle-safe
  — a document already queued for deletion in this call is never
  processed a second time). All of it happens in one transaction: a
  `restrict` anywhere in the chain aborts everything already queued
  above it, not just that one branch.

**(5) Snapshot-consistent reads** — `List` and `Get` now run their
entries query and every relation-reading step
(`attachRelations`/`filterUndeliverableTargets`/`expandIncludes`)
inside one `REPEATABLE READ`, read-only transaction, instead of four
independent statements against the pool. Those three helpers now take
an `execer` (satisfied by both `*pgxpool.Pool` and `pgx.Tx`) instead of
reaching for `s.pool` directly, so write paths (which already run
inside their own transaction) and these read paths share the same
helper code either way.

## Consequences

- `internal/content.Service.DeleteDocument` is the repository's first
  actual document-deletion capability; the FK cascades ADR 0003 added
  were, until now, only reachable by hand against the database (as the
  earlier regression test did directly), never through the API.
- A relation field with no `onDelete` configured defaults to the
  strictest policy (`restrict`), not the most convenient one — a schema
  author has to opt into `set_null`/`cascade` deliberately.
- `one_to_one`/`one_to_many` reverse-uniqueness conflicts are still
  surfaced as the existing `ErrRelationConflict` (409); nothing about
  how a conflict is reported changed, only when one is correctly *not*
  raised (two locale variants of one document) and when it now
  correctly *is* (`one_to_many`'s reverse cardinality, previously
  unenforced).
- A deterministic regression test
  (`TestConcurrentPublishBlocksUpdateUntilCommit`,
  `tests/integration/relations_test.go`) now exists for Publish's `FOR
  UPDATE` lock: it holds that exact lock itself via a raw SQL
  transaction and asserts a concurrent Update blocks the entire time
  the lock is held, only completing once it's released — closing the
  one gap ADR 0003/PROGRESS.md had explicitly flagged as argued from
  code review alone rather than actually tested.
- Components and dynamic zones, when they exist, should reuse `field_id`
  the same way relations do, and should think about their own
  deletion-policy story (what happens to a component instance when its
  parent entry, or something it references, is deleted) rather than
  inheriting relations' answer by default.
