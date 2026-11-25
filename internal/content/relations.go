package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/stefan10xdd/strapi-killer/internal/schema"
)

// execer is satisfied by both *pgxpool.Pool and pgx.Tx, so the relation
// helpers below work identically whether called inside a transaction or
// not. Every call site that validates and then persists relations now
// uses the same tx for both (an independent audit finding: validation
// used to run against the pool directly, outside the write's
// transaction, leaving a gap between "checked" and "written").
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ErrRelationConflict is returned when a write would violate a
// database-enforced relation invariant — currently only one_to_one's
// reverse-uniqueness (migrations/0004) — rather than one this package
// can check for cheaply in advance.
var ErrRelationConflict = errors.New("relation conflict")

// splitRelationFields separates a submitted data payload into ordinary
// JSONB-stored fields and relation field values (as parsed document
// ids), both keyed by the field's Name (the JSON-facing API never deals
// in field ids). Relation fields never reach the entries.data JSONB
// column — they live only in entry_relations.
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

// validateRelationTargets checks, inside the caller's transaction, that
// every referenced document exists, belongs to the same project, and
// matches the relation's configured target content type. Resolving the
// target type through the registry with the caller's own projectID is
// what makes a cross-project reference structurally impossible, not
// just checked: there is no query path here that could ever look at
// another project's documents. A real foreign key (migrations/0004)
// backs this up at the database level regardless.
func validateRelationTargets(ctx context.Context, tx execer, registry *schema.Registry, projectID uuid.UUID, def schema.Definition, relationValues map[string][]uuid.UUID) error {
	for _, f := range def.RelationFields() {
		ids, ok := relationValues[f.Name]
		if !ok || len(ids) == 0 {
			continue
		}
		targetCT, ok := registry.Get(projectID, f.Relation.TargetContentType)
		if !ok {
			return fmt.Errorf("field %q: target content type %q no longer exists", f.Name, f.Relation.TargetContentType)
		}

		var found int
		err := tx.QueryRow(ctx, `
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
// independent relation snapshot. Rows are tagged with the field's
// stable id (not its name — survives a future rename), plus
// fromDocumentID/status/relationType, denormalized so reads and the
// one_to_one uniqueness constraint don't need a join back to entries.
func setRelations(ctx context.Context, tx execer, projectID, fromEntryID, fromDocumentID uuid.UUID, status string, relFields []schema.Field, relationValues map[string][]uuid.UUID) error {
	fieldsByName := make(map[string]schema.Field, len(relFields))
	for _, f := range relFields {
		fieldsByName[f.Name] = f
	}
	for name, ids := range relationValues {
		f, ok := fieldsByName[name]
		if !ok {
			continue // field no longer exists on this content type; nothing to persist.
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM entry_relations WHERE from_entry_id = $1 AND field_id = $2`,
			fromEntryID, f.ID); err != nil {
			return fmt.Errorf("clear relations for field %q: %w", f.Name, err)
		}
		for i, targetID := range ids {
			if _, err := tx.Exec(ctx, `
				INSERT INTO entry_relations
					(project_id, from_entry_id, from_field, field_id, from_document_id, to_document_id, status, relation_type, position)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
				projectID, fromEntryID, f.Name, f.ID, fromDocumentID, targetID, status, string(f.Relation.RelationType), i); err != nil {
				if isUniqueViolation(err) {
					return fmt.Errorf("field %q: %w: this document is already linked by another document's published %s relation", f.Name, ErrRelationConflict, f.Name)
				}
				return fmt.Errorf("insert relation for field %q: %w", f.Name, err)
			}
		}
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// loadRelationIDs returns the current relation values for a single
// entry row, keyed by field Name (translated from the stored field_id
// via relFields — a relation whose field was since removed from the
// schema is skipped rather than surfaced under a name that no longer
// means anything).
func loadRelationIDs(ctx context.Context, tx execer, fromEntryID uuid.UUID, relFields []schema.Field) (map[string][]uuid.UUID, error) {
	nameByFieldID := make(map[uuid.UUID]string, len(relFields))
	for _, f := range relFields {
		nameByFieldID[f.ID] = f.Name
	}

	rows, err := tx.Query(ctx, `
		SELECT field_id, to_document_id FROM entry_relations
		WHERE from_entry_id = $1 ORDER BY field_id, position`, fromEntryID)
	if err != nil {
		return nil, fmt.Errorf("load relations: %w", err)
	}
	defer rows.Close()

	out := map[string][]uuid.UUID{}
	for rows.Next() {
		var fieldID, toDoc uuid.UUID
		if err := rows.Scan(&fieldID, &toDoc); err != nil {
			return nil, fmt.Errorf("scan relation: %w", err)
		}
		name, ok := nameByFieldID[fieldID]
		if !ok {
			continue
		}
		out[name] = append(out[name], toDoc)
	}
	return out, rows.Err()
}

// copyRelations snapshots every relation field's current value on
// fromEntryID (typically a draft row) onto toEntryID/toDocumentID/
// toStatus (typically the published row being promoted), including
// fields with no value so a relation cleared on the draft is also
// cleared on the published copy.
func copyRelations(ctx context.Context, tx execer, projectID, fromEntryID, toEntryID, toDocumentID uuid.UUID, toStatus string, relFields []schema.Field) error {
	snapshot, err := loadRelationIDs(ctx, tx, fromEntryID, relFields)
	if err != nil {
		return fmt.Errorf("load source relations: %w", err)
	}
	for _, f := range relFields {
		if _, ok := snapshot[f.Name]; !ok {
			snapshot[f.Name] = nil // ensure a field cleared on the source is also cleared on the copy.
		}
	}
	return setRelations(ctx, tx, projectID, toEntryID, toDocumentID, toStatus, relFields, snapshot)
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
	nameByFieldID := make(map[uuid.UUID]string, len(relFields))
	for _, f := range relFields {
		nameByFieldID[f.ID] = f.Name
	}

	entryIDs := make([]uuid.UUID, len(entries))
	for i, e := range entries {
		entryIDs[i] = e.ID
	}

	rows, err := s.pool.Query(ctx, `
		SELECT from_entry_id, field_id, to_document_id
		FROM entry_relations
		WHERE project_id = $1 AND from_entry_id = ANY($2)
		ORDER BY from_entry_id, field_id, position`,
		projectID, entryIDs)
	if err != nil {
		return fmt.Errorf("load relations: %w", err)
	}
	defer rows.Close()

	loaded := map[uuid.UUID]map[string][]uuid.UUID{}
	for rows.Next() {
		var fromEntry, fieldID, toDoc uuid.UUID
		if err := rows.Scan(&fromEntry, &fieldID, &toDoc); err != nil {
			return fmt.Errorf("scan relation: %w", err)
		}
		name, ok := nameByFieldID[fieldID]
		if !ok {
			continue
		}
		if loaded[fromEntry] == nil {
			loaded[fromEntry] = map[string][]uuid.UUID{}
		}
		loaded[fromEntry][name] = append(loaded[fromEntry][name], toDoc)
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

// filterUndeliverableTargets drops (to-one: nulls out; to-many: removes
// from the list) any relation stub whose target has no PUBLISHED row in
// the same locale. Call this for delivery/public reads only, after
// attachRelations and before expandIncludes. Without it, a published
// entry could still point at a target that was unpublished (or never
// published in this locale) after the source's own last publish —
// delivering that id as if it resolved would be delivering an
// unpublished document's identity to a public consumer.
func (s *Service) filterUndeliverableTargets(ctx context.Context, projectID uuid.UUID, def schema.Definition, locale string, entries []Entry) error {
	relFields := def.RelationFields()
	if len(relFields) == 0 || len(entries) == 0 {
		return nil
	}

	idSet := map[uuid.UUID]bool{}
	for _, e := range entries {
		for _, f := range relFields {
			addRelationIDs(e.Data[f.Name], idSet)
		}
	}
	if len(idSet) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT document_id FROM entries
		WHERE project_id = $1 AND status = $2 AND locale = $3 AND document_id = ANY($4)`,
		projectID, StatusPublished, locale, ids)
	if err != nil {
		return fmt.Errorf("check deliverable relation targets: %w", err)
	}
	deliverable := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan deliverable target: %w", err)
		}
		deliverable[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for i := range entries {
		for _, f := range relFields {
			entries[i].Data[f.Name] = filterRelationValue(f, entries[i].Data[f.Name], deliverable)
		}
	}
	return nil
}

func filterRelationValue(f schema.Field, value any, allowed map[uuid.UUID]bool) any {
	switch v := value.(type) {
	case string:
		id, err := uuid.Parse(v)
		if err != nil || !allowed[id] {
			return nil
		}
		return v
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			id, err := uuid.Parse(s)
			if err != nil || !allowed[id] {
				continue
			}
			out = append(out, s)
		}
		return out
	default:
		return value
	}
}

// expandIncludes replaces the stub id(s) of each requested relation
// field with the related document's own data, batch-loaded one query
// per included field (not per entry, and not recursively — an included
// entry's own relation fields are left as stub ids, which is what keeps
// this bounded to one level of expansion instead of needing cycle
// detection). The target lookup is scoped to the same locale as the
// entries being expanded, matching entries' own (document_id, locale,
// status) identity — without this, a target with published rows in
// several locales could have an arbitrary one of them picked, since
// document_id alone doesn't disambiguate locale.
func (s *Service) expandIncludes(ctx context.Context, projectID uuid.UUID, def schema.Definition, status, locale string, entries []Entry, include []string) error {
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
			WHERE project_id = $1 AND content_type_id = $2 AND document_id = ANY($3) AND status = $4 AND locale = $5`,
			projectID, targetCT.ID, ids, status, locale)
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
