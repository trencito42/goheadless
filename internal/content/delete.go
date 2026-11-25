package content

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/stefan10xdd/strapi-killer/internal/schema"
)

// ErrDocumentReferenced is returned by DeleteDocument when the document
// is still referenced through a relation field whose onDelete policy is
// "restrict" (the default for any relation field that doesn't configure
// one explicitly).
var ErrDocumentReferenced = errors.New("document is still referenced by other documents")

// DeleteDocument permanently removes a document (every locale and
// draft/published variant of it), honoring each referencing relation
// field's configured deletion policy (schema.DeletePolicy):
//
//   - restrict (the default): refuses the deletion outright if any
//     other document still references this one through that field,
//     draft or published.
//   - set_null: lets the deletion proceed; referencing documents simply
//     stop showing this relation (entry_relations rows pointing at a
//     deleted document are cleaned up by the FK's ON DELETE CASCADE —
//     there is nothing else to do for this policy).
//   - cascade: lets the deletion proceed and also deletes every
//     document that references this one through that field, applying
//     the same policy check recursively (cycle-safe: a document already
//     visited in this call is never processed twice).
//
// All deletes in a cascade chain happen in one transaction: either the
// whole chain is removed, or none of it is (e.g. a restrict further
// down the chain aborts everything already queued above it).
func (s *Service) DeleteDocument(ctx context.Context, projectID uuid.UUID, documentID uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := s.deleteDocumentLocked(ctx, tx, projectID, documentID, map[uuid.UUID]bool{}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete: %w", err)
	}
	return nil
}

func (s *Service) deleteDocumentLocked(ctx context.Context, tx execer, projectID, documentID uuid.UUID, visited map[uuid.UUID]bool) error {
	if visited[documentID] {
		return nil
	}
	visited[documentID] = true

	type reference struct {
		fieldID        uuid.UUID
		fromDocumentID uuid.UUID
		fromCTID       uuid.UUID
	}
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT er.field_id, er.from_document_id, d.content_type_id
		FROM entry_relations er
		JOIN documents d ON d.id = er.from_document_id
		WHERE er.project_id = $1 AND er.to_document_id = $2`,
		projectID, documentID)
	if err != nil {
		return fmt.Errorf("find referencing relations: %w", err)
	}
	var refs []reference
	for rows.Next() {
		var ref reference
		if err := rows.Scan(&ref.fieldID, &ref.fromDocumentID, &ref.fromCTID); err != nil {
			rows.Close()
			return fmt.Errorf("scan referencing relation: %w", err)
		}
		refs = append(refs, ref)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// Group by field so each field's own onDelete policy (looked up
	// once per field, not per referencing document) governs every
	// document referencing the target through it.
	byField := map[uuid.UUID][]reference{}
	for _, ref := range refs {
		byField[ref.fieldID] = append(byField[ref.fieldID], ref)
	}

	for fieldID, group := range byField {
		policy := s.relationFieldDeletePolicy(projectID, group[0].fromCTID, fieldID)
		switch policy {
		case schema.DeleteSetNull:
			// Nothing to do: the FK's ON DELETE CASCADE on to_document_id
			// removes these entry_relations rows the moment the target
			// document is deleted below.
		case schema.DeleteCascade:
			for _, ref := range group {
				if err := s.deleteDocumentLocked(ctx, tx, projectID, ref.fromDocumentID, visited); err != nil {
					return err
				}
			}
		default: // schema.DeleteRestrict, or an unresolvable field (fail safe)
			return fmt.Errorf("%w: %d document(s) still reference it through a relation field with no cascade/set_null policy", ErrDocumentReferenced, len(group))
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM documents WHERE id = $1 AND project_id = $2`, documentID, projectID); err != nil {
		return fmt.Errorf("delete document: %w", err)
	}
	return nil
}

// relationFieldDeletePolicy resolves a relation field's configured
// onDelete policy by the id of the content type that owns it (not its
// api id, which deleteDocumentLocked's SQL join doesn't have handy) and
// the field's own stable id. Falls back to the safe default
// (DeleteRestrict) if the content type or field can no longer be found
// — a schema that's since changed should never make deletion *more*
// permissive than it used to be.
func (s *Service) relationFieldDeletePolicy(projectID, contentTypeID, fieldID uuid.UUID) schema.DeletePolicy {
	for _, ct := range s.registry.ListForProject(projectID) {
		if ct.ID != contentTypeID {
			continue
		}
		for _, f := range ct.Schema.Fields {
			if f.ID == fieldID && f.Relation != nil {
				return f.Relation.DeletePolicyOrDefault()
			}
		}
	}
	return schema.DeleteRestrict
}
