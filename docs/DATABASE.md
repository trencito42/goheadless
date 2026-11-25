# Database

PostgreSQL. Schema lives in `migrations/*.sql`, applied forward-only by
`internal/storage.Migrate` on every process start (idempotent: tracked in
a `schema_migrations` table).

## Current schema (migrations/0001_init.sql)

- `users` — email, argon2id password hash, `is_owner`.
- `sessions` — server-side sessions, `expires_at`.
- `projects` — tenant boundary. Every other table FKs into this,
  `ON DELETE CASCADE`.
- `content_types` — the schema registry. `schema` is JSONB (a
  `schema.Definition`); `(project_id, api_id)` is unique.
- `entries` — hybrid storage. Stable metadata (project, content type,
  document id, locale, status, version, timestamps) as real columns;
  runtime fields in `data JSONB`. `(document_id, locale, status)` is
  unique — this is what makes publish an atomic upsert rather than a
  separate table swap. GIN index on `data` for future filtering.

## Documents and relations (migrations/0003, 0004)

- `documents` — one row per `document_id`: `id` (the document id
  itself), `project_id`, `content_type_id`. Added in migrations/0004
  after the original relations work (migrations/0003) shipped with no
  such table — see `docs/adr/0003-document-identity.md` for why.
  `entries.document_id` and `entry_relations.to_document_id` are both
  real foreign keys to it, `ON DELETE CASCADE`.
- `entries` — same as above, plus: `document_id` now FKs to
  `documents(id)` `ON DELETE CASCADE` (previously just a bare UUID).
- `entry_relations` — one row per (source entry row, field, target
  document). `from_entry_id` FKs to `entries(id)` (the specific draft or
  published *row*, not the document) `ON DELETE CASCADE`, so deleting an
  entries row (e.g. `Unpublish`) automatically cleans up its relations.
  `to_document_id` FKs to `documents(id)` `ON DELETE CASCADE`. Also
  carries `field_id` (the relation field's stable id, not its display
  name — survives a future rename), `from_document_id` (denormalized
  from the owning entries row), and `status`/`relation_type`
  (denormalized from the entries row / schema at write time).
- Keying by row id rather than document id is what makes draft and
  published relations independent snapshots: editing a draft's
  relations never changes what's published until that draft is
  published again (`internal/content.Service.Publish` copies the
  draft's current relation rows onto the published row as part of the
  same transaction that promotes its data, after taking a `FOR UPDATE`
  lock on the draft row so a concurrent `Update` can't produce a
  snapshot that mixes old data with new relations or vice versa).
- `UNIQUE (from_entry_id, from_field, to_document_id)` prevents the same
  target being linked twice from the same field.
- `one_to_one`'s reverse-uniqueness ("at most one document may claim
  this target through this field") is a real, race-free constraint:
  `CREATE UNIQUE INDEX entry_relations_one_to_one_published_target_idx
  ON entry_relations (field_id, to_document_id) WHERE status =
  'published' AND relation_type = 'one_to_one'`. Scoped to `published`
  specifically — a document's own draft and published rows legitimately
  share a target (that's what publishing is); the conflict only becomes
  real once a *second* document's draft actually publishes. `many_to_one`
  and `many_to_many` correctly get no such constraint — many sources are
  supposed to share a target. Full rationale in
  `docs/adr/0003-document-identity.md`.

## What isn't here yet

No media tables, no revision history table (publish overwrites the
published row in place), no webhook/job tables, no API token table, no
row-level security. See PROGRESS.md "known gaps" for the full list —
this file will grow a section per migration as those land, rather than
trying to describe schema that doesn't exist yet.
