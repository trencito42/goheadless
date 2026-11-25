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

## What isn't here yet

No relation tables (relations aren't implemented), no media tables, no
revision history table (publish overwrites the published row in place),
no webhook/job tables, no API token table, no row-level security. See
PROGRESS.md "known gaps" for the full list — this file will grow a
section per migration as those land, rather than trying to describe
schema that doesn't exist yet.
