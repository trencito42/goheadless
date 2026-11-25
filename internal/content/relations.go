package content

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/stefan10xdd/strapi-killer/internal/schema"
)

// execer is satisfied by both *pgxpool.Pool and pgx.Tx, so the relation
// helpers below work identically whether called inside a transaction or
// not.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// splitRelationFields separates a submitted data payload into ordinary
// JSONB-stored fields and relation field values (as parsed document
// ids). Relation fields never reach the entries.data JSONB column —
// they live only in entry_relations.
func splitRelationFields(def schema.Definition, data map[string]any) (jsonFields map[string]any, relationValues map[string][]uuid.UUID, err error) {
	relFields := def.RelationFields()
	if len(relFields) == 0 {
		return data, nil, nil
	}
	relSet := make(map[string]schema.Field, len(relFields))
	for _, f := range relFields {
		relSet[f.Name] = f
	}

	jsonFields = make(map[string]any, len(data))
	relationValues = make(map[string][]uuid.UUID, len(relFields))
	for key, value := range data {
		f, isRelation := relSet[key]
		if !isRelation {
			jsonFields[key] = value
			continue
		}
		ids, err := parseRelationValue(f, value)
		if err != nil {
			return nil, nil, fmt.Errorf("field %q: %w", key, err)
		}
		relationValues[key] = ids
	}
	return jsonFields, relationValues, nil
}

func parseRelationValue(f schema.Field, value any) ([]uuid.UUID, error) {
	if value == nil {
		return nil, nil
	}
	if f.Relation.IsToMany() {
		items, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("expected a list of document ids")
		}
		ids := make([]uuid.UUID, 0, len(items))
		for _, item := range items {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("expected a document id string")
			}
			id, err := uuid.Parse(s)
			if err != nil {
				return nil, fmt.Errorf("invalid document id %q", s)
			}
			ids = append(ids, id)
		}
		return ids, nil
	}
	s, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("expected a document id string")
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("invalid document id %q", s)
	}
	return []uuid.UUID{id}, nil
}

// validateRelationTargets checks that every referenced document exists,
// belongs to the same project, and matches the relation's configured
// target content type. Resolving the target type through the registry
// with the caller's own projectID is what makes a cross-project
// reference structurally impossible, not just checked: there is no
// query path here that could ever look at another project's documents.
func (s *Service) validateRelationTargets(ctx context.Context, projectID uuid.UUID, def schema.Definition, relationValues map[string][]uuid.UUID) error {
	for _, f := range def.RelationFields() {
		ids, ok := relationValues[f.Name]
		if !ok || len(ids) == 0 {
			continue
		}
		targetCT, ok := s.registry.Get(projectID, f.Relation.TargetContentType)
		if !ok {
			return fmt.Errorf("field %q: target content type %q no longer exists", f.Name, f.Relation.TargetContentType)
		}

		var found int
		err := s.pool.QueryRow(ctx, `
			SELECT count(DISTINCT document_id) FROM entries
			WHERE project_id = $1 AND content_type_id = $2 AND document_id = ANY($3)`,
			projectID, targetCT.ID, ids,
		).Scan(&found)
		if err != nil {
			return fmt.Errorf("field %q: check relation targets: %w", f.Name, err)
		}
		if found != len(uniqueUUIDs(ids)) {
			return fmt.Errorf("field %q: one or more referenced documents do not exist in this project as %q", f.Name, f.Relation.TargetContentType)
		}
	}
	return nil
}

func uniqueUUIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	var out []uuid.UUID
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// setRelations replaces the stored relations for every field present in
// relationValues (fields absent from the map are left untouched —
// omitting a relation field from a request means "no change", not
// "clear it"; submit an empty list to clear one explicitly). It is
// keyed by from_entry_id, the specific draft or published *row*, not
// the document — so draft and published variants each hold their own
// independent relation snapshot.
func setRelations(ctx context.Context, tx execer, projectID, fromEntryID uuid.UUID, relationValues map[string][]uuid.UUID) error {
	for field, ids := range relationValues {
		if _, err := tx.Exec(ctx,
			`DELETE FROM entry_relations WHERE from_entry_id = $1 AND from_field = $2`,
			fromEntryID, field); err != nil {
			return fmt.Errorf("clear relations for field %q: %w", field, err)
		}
		for i, targetID := range ids {
			if _, err := tx.Exec(ctx, `
				INSERT INTO entry_relations (project_id, from_entry_id, from_field, to_document_id, position)
				VALUES ($1, $2, $3, $4, $5)`,
				projectID, fromEntryID, field, targetID, i); err != nil {
				return fmt.Errorf("insert relation for field %q: %w", field, err)
			}
		}
	}
	return nil
}

// loadRelationIDs returns the current relation values for a single
// entry row, keyed by field name.
func loadRelationIDs(ctx context.Context, tx execer, fromEntryID uuid.UUID) (map[string][]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `
		SELECT from_field, to_document_id FROM entry_relations
		WHERE from_entry_id = $1 ORDER BY from_field, position`, fromEntryID)
	if err != nil {
		return nil, fmt.Errorf("load relations: %w", err)
	}
	defer rows.Close()

	out := map[string][]uuid.UUID{}
	for rows.Next() {
		var field string
		var toDoc uuid.UUID
		if err := rows.Scan(&field, &toDoc); err != nil {
			return nil, fmt.Errorf("scan relation: %w", err)
		}
		out[field] = append(out[field], toDoc)
	}
	return out, rows.Err()
}

// copyRelations snapshots every relation field's current value on
// fromEntryID (typically a draft row) onto toEntryID (typically the
// published row being promoted), including fields with no value so a
// relation cleared on the draft is also cleared on the published copy.
func copyRelations(ctx context.Context, tx execer, projectID, fromEntryID, toEntryID uuid.UUID, relFields []schema.Field) error {
	snapshot, err := loadRelationIDs(ctx, tx, fromEntryID)
	if err != nil {
		return fmt.Errorf("load source relations: %w", err)
	}
	for _, f := range relFields {
		if _, ok := snapshot[f.Name]; !ok {
			snapshot[f.Name] = nil // ensure a field cleared on the source is also cleared on the copy.
		}
	}
	return setRelations(ctx, tx, projectID, toEntryID, snapshot)
}

// shapeRelationValue converts a stored/parsed id list into the JSON
// shape returned to API callers: a bare document id string (or null)
// for to-one relations, a list of strings for to-many. The to-many
// result is []any (not []string) so it round-trips, unchanged, back
// through schema.ValidateEntry's relation shape check — needed because
// Publish re-validates a reconstructed entry that merges this shaped
// value back into the data map before checking it against the schema.
func shapeRelationValue(f schema.Field, ids []uuid.UUID) any {
	if f.Relation.IsToMany() {
		out := make([]any, len(ids))
		for i, id := range ids {
			out[i] = id.String()
		}
		return out
	}
	if len(ids) == 0 {
		return nil
	}
	return ids[0].String()
}

// attachRelations loads every relation field's value for a batch of
// entries in a single query (never one query per entry) and merges the
// result into each entry's Data. Call this after building entries whose
// Data was unmarshaled only from the JSONB column, which never contains
// relation fields. Lookup is keyed by each entry's own row id, which is
// what keeps a draft's relations from ever being attached to its
// published sibling or vice versa.
func (s *Service) attachRelations(ctx context.Context, projectID uuid.UUID, def schema.Definition, entries []Entry) error {
	relFields := def.RelationFields()
	if len(relFields) == 0 || len(entries) == 0 {
		return nil
	}

	entryIDs := make([]uuid.UUID, len(entries))
	for i, e := range entries {
		entryIDs[i] = e.ID
	}

	rows, err := s.pool.Query(ctx, `
		SELECT from_entry_id, from_field, to_document_id
		FROM entry_relations
		WHERE project_id = $1 AND from_entry_id = ANY($2)
		ORDER BY from_entry_id, from_field, position`,
		projectID, entryIDs)
	if err != nil {
		return fmt.Errorf("load relations: %w", err)
	}
	defer rows.Close()

	loaded := map[uuid.UUID]map[string][]uuid.UUID{}
	for rows.Next() {
		var fromEntry, toDoc uuid.UUID
		var field string
		if err := rows.Scan(&fromEntry, &field, &toDoc); err != nil {
			return fmt.Errorf("scan relation: %w", err)
		}
		if loaded[fromEntry] == nil {
			loaded[fromEntry] = map[string][]uuid.UUID{}
		}
		loaded[fromEntry][field] = append(loaded[fromEntry][field], toDoc)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for i := range entries {
		if entries[i].Data == nil {
			entries[i].Data = map[string]any{}
		}
		for _, f := range relFields {
			entries[i].Data[f.Name] = shapeRelationValue(f, loaded[entries[i].ID][f.Name])
		}
	}
	return nil
}

// expandIncludes replaces the stub id(s) of each requested relation
// field with the related document's own data, batch-loaded one query
// per included field (not per entry, and not recursively — an included
// entry's own relation fields are left as stub ids, which is what keeps
// this bounded to one level of expansion instead of needing cycle
// detection).
func (s *Service) expandIncludes(ctx context.Context, projectID uuid.UUID, def schema.Definition, status string, entries []Entry, include []string) error {
	if len(include) == 0 || len(entries) == 0 {
		return nil
	}
	includeSet := make(map[string]bool, len(include))
	for _, f := range include {
		includeSet[f] = true
	}

	for _, f := range def.RelationFields() {
		if !includeSet[f.Name] {
			continue
		}
		targetCT, ok := s.registry.Get(projectID, f.Relation.TargetContentType)
		if !ok {
			continue // target type no longer exists; leave stub ids as-is.
		}

		idSet := map[uuid.UUID]bool{}
		for _, e := range entries {
			addRelationIDs(e.Data[f.Name], idSet)
		}
		if len(idSet) == 0 {
			continue
		}
		ids := make([]uuid.UUID, 0, len(idSet))
		for id := range idSet {
			ids = append(ids, id)
		}

		rows, err := s.pool.Query(ctx, `
			SELECT document_id, data FROM entries
			WHERE project_id = $1 AND content_type_id = $2 AND document_id = ANY($3) AND status = $4`,
			projectID, targetCT.ID, ids, status)
		if err != nil {
			return fmt.Errorf("load included %q: %w", f.Name, err)
		}

		related := map[uuid.UUID]map[string]any{}
		for rows.Next() {
			var docID uuid.UUID
			var dataBytes []byte
			if err := rows.Scan(&docID, &dataBytes); err != nil {
				rows.Close()
				return fmt.Errorf("scan included %q: %w", f.Name, err)
			}
			var d map[string]any
			if err := json.Unmarshal(dataBytes, &d); err != nil {
				rows.Close()
				return fmt.Errorf("unmarshal included %q: %w", f.Name, err)
			}
			redactPrivate(targetCT.Schema, d)
			related[docID] = d
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for i := range entries {
			entries[i].Data[f.Name] = expandRelationValue(entries[i].Data[f.Name], related)
		}
	}
	return nil
}

// redactPrivate deletes any field marked Private from data in place.
// Applied unconditionally to relation-expanded (included) data, since
// content.Service has no notion of the caller's permission level at
// this depth — an included document is always shown as its redacted,
// public-safe projection, never its full admin view.
func redactPrivate(def schema.Definition, data map[string]any) {
	for _, f := range def.Fields {
		if f.Private {
			delete(data, f.Name)
		}
	}
}

func addRelationIDs(value any, into map[uuid.UUID]bool) {
	switch v := value.(type) {
	case string:
		if id, err := uuid.Parse(v); err == nil {
			into[id] = true
		}
	case []any:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			if id, err := uuid.Parse(s); err == nil {
				into[id] = true
			}
		}
	}
}

func expandRelationValue(value any, related map[uuid.UUID]map[string]any) any {
	switch v := value.(type) {
	case string:
		id, err := uuid.Parse(v)
		if err != nil {
			return value
		}
		data, ok := related[id]
		if !ok {
			return nil
		}
		return map[string]any{"documentId": v, "data": data}
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			id, err := uuid.Parse(s)
			if err != nil {
				continue
			}
			data, ok := related[id]
			if !ok {
				continue
			}
			out = append(out, map[string]any{"documentId": s, "data": data})
		}
		return out
	default:
		return value
	}
}
