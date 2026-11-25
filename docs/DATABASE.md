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

## Relations (migrations/0003_relations.sql)

- `entry_relations` — one row per (source entry row, field, target
  document). `from_entry_id` FKs to `entries(id)` (the specific draft or
  published *row*, not the document) `ON DELETE CASCADE`, so deleting an
  entries row (e.g. `Unpublish`) automatically cleans up its relations.
  `to_document_id` has no FK (there is no single "documents" table to
  point at — see `docs/adr/0002-relations.md`); existence, project, and
  content-type matching are enforced in `internal/content` at write
  time instead.
- Keying by row id rather than document id is what makes draft and
  published relations independent snapshots: editing a draft's
  relations never changes what's published until that draft is
  published again (`internal/content.Service.Publish` copies the
  draft's current relation rows onto the published row as part of the
  same transaction that promotes its data).
- `UNIQUE (from_entry_id, from_field, to_document_id)` prevents the same
  target being linked twice from the same field. There is **no**
  reverse-uniqueness constraint for `one_to_one`/`many_to_one` (nothing
  stops two different source entries from both pointing at the same
  target) — tracked as a known gap in PROGRESS.md, not silently assumed
  solved.

## What isn't here yet

No media tables, no revision history table (publish overwrites the
published row in place), no webhook/job tables, no API token table, no
row-level security. See PROGRESS.md "known gaps" for the full list —
this file will grow a section per migration as those land, rather than
trying to describe schema that doesn't exist yet.
