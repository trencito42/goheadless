-- Relations between entries, stored in a dedicated table rather than
-- inside the JSONB data column, so referential integrity checks and
-- batch loading don't require parsing JSON.
--
-- from_entry_id is the *row* id of a specific draft or published entry
-- (not a document-level id), so draft and published variants of the
-- same document each carry their own, independent set of relation rows
-- ("relation snapshots"): editing a draft's relations never changes
-- what the published variant exposes until that draft is published, and
-- the FK on entries(id) means a draft or published row being deleted
-- (e.g. by Unpublish) automatically cleans up its own relation rows
-- without application code having to do it.
--
-- to_document_id originally had no FK here: a document had no single
-- table of its own (it was just a value shared by its draft/published
-- rows), so there was nothing a foreign key could point at. Existence,
-- project and content-type matching for the target were enforced in
-- application code (internal/content) at write time instead — see
-- docs/adr/0002-relations.md. migrations/0004 introduces a real
-- `documents` table and adds that FK (plus field_id, from_document_id,
-- status and relation_type columns) — see docs/adr/0003-document-identity.md.

CREATE TABLE entry_relations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    from_entry_id UUID NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    from_field TEXT NOT NULL,
    to_document_id UUID NOT NULL,
    position INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (from_entry_id, from_field, to_document_id)
);

CREATE INDEX entry_relations_from_idx ON entry_relations (from_entry_id, from_field);
CREATE INDEX entry_relations_to_idx ON entry_relations (project_id, to_document_id);
