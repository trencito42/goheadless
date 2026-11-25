# ADR 0002: Relations as a dedicated table, keyed by entry row

## Status

Accepted.

## Context

Relation fields need referential integrity (a relation shouldn't be
able to point at a document that doesn't exist, or that lives in
another project) and batch loading (listing N entries with a relation
field must never cost N extra queries). JSONB storage (ADR 0001) gives
neither: there's no foreign key to a value buried inside a JSON blob,
and loading relations for a batch would mean parsing JSON per row.

A second question sits underneath the first: content types use a
draft/publish model where a document is really two independent rows
(`entries` rows sharing a `document_id`, distinguished by `status`).
Should a relation belong to the *document* (draft and published always
agree on what's related) or to the *row* (draft and published can
diverge until the next publish)?

## Decision

A dedicated `entry_relations` table (`migrations/0003_relations.sql`),
keyed by **`from_entry_id` — the entries row id, not the document id**.

This means relations are a property of a specific draft or published
row, not of the document as a whole: editing a draft's relations never
changes what the published variant exposes, exactly like editing a
draft's other fields doesn't. Publishing copies the draft's current
relation rows onto the published row as an explicit step in the same
transaction that promotes its data (`internal/content.Service.Publish`
→ `copyRelations`), producing an independent snapshot rather than a
live reference.

`from_entry_id` is a real foreign key to `entries(id)` with `ON DELETE
CASCADE` — unpublishing (which deletes the published row) automatically
cleans up that row's relations with no application code needed.
`to_document_id` has **no** foreign key: there is no single "documents"
table whose primary key it could reference (a document is just a value
shared by its draft/published entries rows, per ADR 0001). Existence of
the target, and that it belongs to the same project and the field's
configured target content type, are checked in application code
(`internal/content.Service.validateRelationTargets`) before the write.

> **Amended by ADR 0003**: the paragraph above was accurate when
> written but is no longer the current design. ADR 0003 adds a real
> `documents` table, giving `to_document_id` an actual foreign key, and
> moves `validateRelationTargets` to run inside the same transaction as
> the write it validates (it originally ran against the pool directly,
> outside that transaction — a gap an independent audit caught). Left
> here rather than edited away, same as the cardinality correction
> below.

Cross-project references are rejected structurally, not just by a
check that could be bypassed: the target content type is always
resolved through `schema.Registry.Get(projectID, targetApiID)` using
the *caller's own* project id, so there is no code path that could ever
look up another project's content type or rows.

## Consequences

- Draft and published relation snapshots are genuinely independent,
  matching how every other field already behaves across the
  draft/publish boundary. This costs one extra write (the snapshot
  copy) per publish, which is cheap relative to the correctness it
  buys.
- Batch loading is one query per read (`attachRelations`), keyed by a
  batch of entry row ids, never one query per entry. `?include=field`
  expansion is one additional query per *included field* (not per
  entry, not recursive), fetching the related rows' own JSONB data
  directly — an included entry's own relation fields are left as bare
  id stubs rather than being expanded again, which is what keeps
  expansion bounded to one level with no cycle detection needed.
- Included (expanded) relation data always has its source content
  type's `private` fields redacted, regardless of whether the outer
  read is a draft or delivery read — `internal/content` has no notion
  of the caller's permission level at that depth, so an expanded embed
  is always the redacted, public-safe projection.
- **Correction (see ADR 0003)**: this ADR originally claimed here that
  neither `one_to_one` nor `many_to_one` had a reverse-uniqueness
  constraint, as if that were one gap. That conflated two different
  things. `many_to_one` is *supposed* to let many source documents
  reference the same target — it never needed reverse uniqueness, and
  was never missing anything. `one_to_one` genuinely did need it; ADR
  0003 adds a real, race-free database constraint for it (a documents
  table makes that possible). Left here so the mistake is visible
  rather than quietly edited away.
- Components and dynamic zones (not yet implemented) will likely reuse
  this same "keyed by entry row id" pattern for their own nested
  relations, once they exist.
