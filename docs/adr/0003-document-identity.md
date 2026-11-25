# ADR 0003: A real `documents` table, and fixing the one_to_one gap properly

## Status

Accepted. Supersedes the "no documents table" part of ADR 0002 and
corrects a cardinality mistake ADR 0002 made.

## Context

An independent audit of the relations work (ADR 0002,
`migrations/0003_relations.sql`) raised several findings. Two of them
turned out to be the same underlying problem:

1. `entries.document_id` and `entry_relations.to_document_id` were both
   bare UUIDs with no foreign key, because — per ADR 0002 — there was
   no single table a document's many rows (draft, published, each
   locale) could all point at. Existence was checked in application
   code at write time instead, which works but isn't a real guarantee,
   and doesn't give the database anything to cascade-delete through.
2. ADR 0002 documented "no reverse-uniqueness for
   `one_to_one`/`many_to_one`" as a single known gap. That was wrong in
   a way worth naming plainly: `many_to_one` is *supposed* to allow
   many source documents to reference the same target (that's its
   entire definition — many posts, one author) — it was never missing
   a constraint; it was *correctly unconstrained* on that axis, and
   ADR 0002's text conflated it with `one_to_one`, which is the only
   relation type where reverse uniqueness is actually a real invariant.

Fixing (1) is also most of what's needed to fix (2) properly: with a
real `documents` table, a database-enforced reverse-uniqueness
constraint for `one_to_one` specifically becomes possible instead of
an application-level check-then-insert that would race under
concurrent writes.

## Decision

Add a `documents` table (`migrations/0004_...sql`): one row per
`document_id`, holding exactly the metadata every variant of that
document agrees on (`project_id`, `content_type_id`). `entries.
document_id` and `entry_relations.to_document_id` are now real foreign
keys to it, both `ON DELETE CASCADE` — deleting a document now
automatically deletes every draft/published/locale row and every
relation pointing at or from it, with no application code required
(there is still no "delete a document" API endpoint yet, so this is
mostly groundwork for when one exists, not something reachable today).

`entry_relations` also gains:
- `field_id` — the relation field's stable identity (a new
  `schema.Field.ID`, assigned once when a field is created and never
  changed), used instead of the field's display name. A field rename,
  once schema migrations support renaming, won't silently orphan
  stored relations the way keying by name would.
- `from_document_id`, `status`, `relation_type` — denormalized at write
  time, so relations for a document can be queried without a join
  back to `entries`, and so the constraint below can be expressed
  without re-reading the schema.

**`one_to_one` reverse-uniqueness is now a real, race-free database
constraint**: a partial unique index on `(field_id, to_document_id)
WHERE status = 'published' AND relation_type = 'one_to_one'`. It's
scoped to `published` specifically, which resolves a subtlety the naive
version of this constraint would get wrong: a document's own draft and
published rows legitimately share the same target (that's what
publishing *is* — copying the draft's relation snapshot onto the
published row, per ADR 0002), so a constraint with no status scoping
would reject every single publish of a `one_to_one` relation as a
self-conflict. Scoping to `published` also means two different
documents' drafts may transiently both point at the same `one_to_one`
target — correctly treated as not-yet-a-conflict — and the conflict
only becomes real at the point one of them actually publishes, where
it surfaces as an ordinary unique-violation that
`internal/content.Service.Publish` maps to a validation error rather
than a raw database error leaking through.

`many_to_one` and `many_to_many` get no such constraint, correctly —
many sources may point at the same target. `one_to_many` similarly
gets none on this axis (its "many" side is the list a to-many field
already enforces by shape; nothing stops many *different* one_to_many
fields, or the same field from different source documents, from
pointing into the same set of targets, which is correct for that
relation type).

## Consequences

- Relation target existence is now checked by two layers:
  `internal/content.Service.validateRelationTargets` (still needed —
  it also checks the target's content type matches the field's
  configured `targetContentType`, which a bare FK can't express) *and*
  a real foreign key as a backstop that can't be bypassed by a bug in
  application code.
- `validateRelationTargets` now runs inside the same transaction as the
  write it's validating for (it previously used the pool directly,
  outside the transaction — an independent audit finding fixed
  alongside this one), closing the gap between "checked" and
  "written".
- Pre-release, no-real-user-data tradeoff: existing `entry_relations`
  rows (all written in the same session ADR 0002 shipped in) could not
  be correctly backfilled with the new columns — `field_id` in
  particular has no derivable value from what was stored — so the
  migration discards them outright rather than guessing. Documented
  plainly in the migration itself; not something a real deployment
  with relation data worth keeping should run as-is.
- Components and dynamic zones, when they exist, should use `field_id`
  from the start rather than field names, for the same rename-safety
  reason.
