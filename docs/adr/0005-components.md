# ADR 0005: Components and dynamic zones as nested JSONB, not new tables

## Status

Accepted.

## Context

The spec calls for two more field types: `component` (a named,
reusable group of fields — e.g. an "address" or "SEO" block) and
`dynamic_zone` (an ordered list of component instances, each one of a
named set of allowed component types — e.g. a page builder's list of
content blocks). Both were the last blocker noted in PROGRESS.md for
starting this work; relations (ADR 0002/0003/0004) were the
prerequisite, and are now done.

The relations work gives two patterns worth reusing: a stable
`schema.Field.ID` so storage doesn't depend on a field's display name,
and a hybrid storage split (ADR 0001) between JSONB for plain data and
a dedicated table for anything that needs referential integrity or
batch loading.

## Decision

Components and dynamic zones are **not** a new table. A component
field's value is just a nested JSON object (or, if `repeatable`, a
list of them) stored inline in the owning entry's `data` JSONB column,
validated recursively the same way a top-level `Definition` is. A
dynamic zone's value is a list of such objects, each tagged
`"__component": "<name>"` saying which of the field's named component
types it's an instance of.

This needs **no migration** — `entries.data` already stores arbitrary
JSON, and there's nothing here a foreign key or an index could usefully
constrain, unlike relations.

The one restriction this buys simplicity for: **a component or dynamic
zone's own fields may not themselves be a relation, a component, or a
dynamic zone** (`schema.rejectNestedContainers`, checked at content-type
creation time). Two different reasons, bundled into one rule for now:

- A relation needs a `from_entry_id` to key `entry_relations` by (ADR
  0002/0003). A value nested inside JSONB was never written as its own
  `entries` row, so it has no id a relation could be attached to.
  Supporting relations inside components would mean giving component
  *instances* their own identity — a bigger design question deferred
  rather than answered badly under this ADR.
- Nesting a component inside a component, or a dynamic zone inside
  either, is allowed by nothing in the spec that demands it yet, and
  bounds the recursion in `validateFieldList`/`validateComponentValue`
  to exactly one level, which is simpler to reason about and test.

Both restrictions are enforced where the schema is created
(`Definition.Validate`), not left as a silent gap found only once
someone tries it: a content type whose component nests a relation is
rejected outright (400), with a message naming which nested field was
the problem.

`schema.Field.ID` is assigned recursively — `Registry.Create`'s
`assignFieldIDs` now walks into `Component.Fields` and every named
component's `Fields` inside a `DynamicZoneConfig`, not just the
top-level field list — and duplicate-id rejection
(`Definition.Validate`) is checked against one id set shared across
the whole recursion, so a component's field can't collide with a
sibling field's id anywhere else in the schema either.

## Consequences

- No new migration, no new table, no new column. The entire feature is
  additive to `internal/schema` (new field types, `ComponentConfig`/
  `DynamicZoneConfig`, recursive validation) with zero changes to
  `internal/content` beyond what already handles arbitrary JSONB data
  — a component/dynamic-zone field never reaches
  `splitRelationFields`/`setRelations`/etc. because it's never a
  relation field.
- A validation error inside a deeply nested field (e.g. a required
  field missing inside a dynamic zone's third item) is reported with
  enough context to find it (`field %q: component %q: item %d: ...`),
  not just "validation failed."
- The one-level nesting limit and the no-relations-inside restriction
  are both real scope cuts, not oversights — tracked explicitly in
  PROGRESS.md as what a second pass at this feature would need to
  revisit, rather than quietly left unmentioned.
- Schema migrations (renaming/removing a field, changing its type),
  when they exist, need to decide what happens to a component's nested
  fields the same way they will for top-level ones — this ADR doesn't
  attempt that; it only makes sure nested fields have the same stable
  `ID` top-level ones do, which is the one piece a migration engine
  would need regardless.
