-- Independent audit of the relations work (migrations/0003) found that
-- "no documents table" (ADR 0002) was costing more than it should:
-- entries.document_id and entry_relations.to_document_id were both
-- bare UUIDs with existence checked only in application code. This
-- migration introduces a real identity for a document and switches
-- both to real foreign keys. See docs/adr/0003-document-identity.md.

CREATE TABLE documents (
    id UUID PRIMARY KEY,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    content_type_id UUID NOT NULL REFERENCES content_types(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Backfill: one documents row per distinct document_id already present
-- in entries (every draft/published/locale variant of a document
-- collapses to the one document it belongs to).
INSERT INTO documents (id, project_id, content_type_id, created_at)
SELECT document_id, project_id, content_type_id, min(created_at)
FROM entries
GROUP BY document_id, project_id, content_type_id;

ALTER TABLE entries
    ADD CONSTRAINT entries_document_id_fkey
    FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE CASCADE;

-- entry_relations gains: field_id (the field's stable identity — see
-- docs/adr/0003 — used instead of the field's display name so a future
-- field rename can't silently orphan stored relations), from_document_id
-- (denormalized from the owning entries row, so relations for a
-- document can be queried without a join), status and relation_type
-- (both denormalized from the entries row / schema at write time,
-- needed so a single DB constraint below can enforce one_to_one's
-- reverse-uniqueness invariant without re-reading the schema).
--
-- This repository is pre-release with no real user data: any
-- entry_relations rows written before this migration (all created in
-- the same session migrations/0003 shipped in, during manual curl
-- verification) cannot be correctly backfilled — field_id in
-- particular has no derivable value from what was stored — so they are
-- discarded outright rather than backfilled with guessed values. Any
-- real deployment of this schema should not yet have relation data
-- that matters.
DELETE FROM entry_relations;

ALTER TABLE entry_relations
    ADD COLUMN field_id UUID NOT NULL,
    ADD COLUMN from_document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    ADD COLUMN status TEXT NOT NULL CHECK (status IN ('draft', 'published')),
    ADD COLUMN relation_type TEXT NOT NULL CHECK (relation_type IN ('one_to_one', 'one_to_many', 'many_to_one', 'many_to_many')),
    ADD CONSTRAINT entry_relations_to_document_fkey
        FOREIGN KEY (to_document_id) REFERENCES documents(id) ON DELETE CASCADE;

CREATE INDEX entry_relations_from_document_idx ON entry_relations (from_document_id, field_id);

-- The reverse-uniqueness invariant one_to_one actually needs ("at most
-- one document may claim this target through this field") is enforced
-- here, for real, by the database — not by an application-level
-- check-then-insert that would race under concurrent writes. It is
-- scoped to status = 'published' specifically: a document's own draft
-- and published rows legitimately share the same target (that's what
-- publishing is), and two different documents' *drafts* are allowed to
-- transiently both point at the same target — the conflict is only
-- real once one of them actually publishes, at which point this index
-- rejects the second one with a normal unique_violation that
-- internal/content maps to a friendly validation error.
CREATE UNIQUE INDEX entry_relations_one_to_one_published_target_idx
    ON entry_relations (field_id, to_document_id)
    WHERE status = 'published' AND relation_type = 'one_to_one';
