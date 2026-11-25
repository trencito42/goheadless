-- Independent audit of migrations/0004 found three more gaps:
--
-- 1. schema.Field.ID only gets assigned for fields written through
--    Registry.Create going forward; any content_types row inserted
--    before that code existed would have fields with no "id" key at
--    all (or the client could have supplied one, including a
--    duplicate). This backfills a fresh id for any field missing one,
--    non-destructively (it only ever adds the "id" key to an existing
--    field object, never removes or rewrites anything else).
--
-- 2. entries.document_id and entry_relations.{from,to}_document_id
--    only had to point at *some* row in documents — nothing stopped a
--    document that actually belongs to project A, content type "post"
--    from being written onto an entries row tagged project B, content
--    type "comment". Application code never does this, but it was
--    only application code stopping it. Composite foreign keys below
--    make Postgres itself reject it.
--
-- 3. The one_to_one reverse-uniqueness index from migrations/0004 was
--    keyed by (field_id, to_document_id) alone, with no locale. That's
--    too strict: a document with "en" and "fr" locale variants
--    publishes as two separate entries rows (and so two separate
--    entry_relations rows), both naturally carrying the same
--    from_document_id and the same to_document_id when both locales
--    relate to the same target — which the old index rejected as a
--    conflict between "two documents" even though it's one document
--    publishing in two locales. Scoping by locale fixes that: two
--    locale variants of the SAME document no longer collide, while
--    two DIFFERENT documents claiming the same target in the SAME
--    locale still correctly do. This also extends the constraint to
--    one_to_many, which needs the identical reverse-uniqueness
--    invariant (one parent per target) and was missing it entirely.
--
-- See docs/adr/0003-document-identity.md for the fuller rationale.

-- (1) Backfill missing/zero field ids. Deterministic to run again
-- (re-running is a no-op once every field has a real id), so this is
-- safe to ship even if it's ever run twice.
UPDATE content_types
SET schema = jsonb_set(
    schema,
    '{fields}',
    (
        SELECT jsonb_agg(
            CASE
                WHEN (f->>'id') IS NULL OR f->>'id' = '00000000-0000-0000-0000-000000000000'
                    THEN f || jsonb_build_object('id', gen_random_uuid())
                ELSE f
            END
        )
        FROM jsonb_array_elements(schema->'fields') AS f
    )
)
WHERE EXISTS (
    SELECT 1 FROM jsonb_array_elements(schema->'fields') AS f
    WHERE (f->>'id') IS NULL OR f->>'id' = '00000000-0000-0000-0000-000000000000'
);

-- (2) Composite project/content-type consistency, enforced by Postgres.
ALTER TABLE documents
    ADD CONSTRAINT documents_id_project_key UNIQUE (id, project_id),
    ADD CONSTRAINT documents_id_project_ct_key UNIQUE (id, project_id, content_type_id);

ALTER TABLE entries
    ADD CONSTRAINT entries_document_project_ct_fkey
        FOREIGN KEY (document_id, project_id, content_type_id)
        REFERENCES documents (id, project_id, content_type_id);

ALTER TABLE entry_relations
    ADD CONSTRAINT entry_relations_from_document_project_fkey
        FOREIGN KEY (from_document_id, project_id) REFERENCES documents (id, project_id),
    ADD CONSTRAINT entry_relations_to_document_project_fkey
        FOREIGN KEY (to_document_id, project_id) REFERENCES documents (id, project_id);

-- (3) locale-aware, one_to_many-inclusive reverse uniqueness.
ALTER TABLE entry_relations ADD COLUMN locale TEXT;

UPDATE entry_relations er
SET locale = e.locale
FROM entries e
WHERE e.id = er.from_entry_id;

ALTER TABLE entry_relations ALTER COLUMN locale SET NOT NULL;

DROP INDEX entry_relations_one_to_one_published_target_idx;

CREATE UNIQUE INDEX entry_relations_reverse_unique_published_target_idx
    ON entry_relations (field_id, to_document_id, locale)
    WHERE status = 'published' AND relation_type IN ('one_to_one', 'one_to_many');
