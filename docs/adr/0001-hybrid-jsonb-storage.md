# ADR 0001: Hybrid relational + JSONB storage for entries

## Status

Accepted.

## Context

Content types are defined at runtime by administrators. The storage
model needs to support arbitrary, frequently-changing field sets without
a schema migration (ALTER TABLE) for every content model change, while
still giving strong guarantees (uniqueness, indexing, referential
integrity) for the metadata that's true of every entry regardless of its
content type.

Options considered: one physical table per content type (fast, correct,
but requires DDL on every schema change); fully generic EAV tables (one
row per field, maximum flexibility, poor query performance and
unreadable SQL); hybrid (relational columns for stable metadata, JSONB
for runtime fields).

## Decision

Hybrid. `entries` has real columns for `project_id`, `content_type_id`,
`document_id`, `locale`, `status`, `version`, timestamps — these are
never runtime-configurable and benefit from real constraints/indexes
(notably the `(document_id, locale, status)` unique constraint the
draft/publish model depends on). Everything an administrator defines in
the Content Type Builder lives in `data JSONB`.

## Consequences

- Adding/removing a field in the Content Type Builder never requires a
  database migration.
- Validation against the active schema happens in application code
  (`internal/schema.Definition.ValidateEntry`), not the database — the
  database can't enforce "this JSON matches this dynamic schema".
- Filtering/sorting on JSONB fields will need expression or GIN indexes
  added deliberately as the query engine grows (a GIN index on `data`
  already exists; targeted expression indexes for specific
  filterable/sortable fields are future work, not yet needed at current
  scale).
- Relations, when implemented, will need dedicated relational tables
  (not JSONB references) to get referential integrity and avoid an
  extra query per related entry — this is already anticipated in
  PROGRESS.md's next steps.
