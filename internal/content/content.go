// Package content is the content management and delivery engine: CRUD
// over entries belonging to a runtime-defined content type, and the
// draft/publish lifecycle.
package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/stefan10xdd/strapi-killer/internal/schema"
)

const (
	StatusDraft     = "draft"
	StatusPublished = "published"
)

// ErrNotOwner is returned by Update when the caller is not the entry's
// creator and the request requires ownership (an Author-role caller;
// Editor and above bypass this check).
var ErrNotOwner = errors.New("you did not create this draft and do not have editor access")

type Entry struct {
	ID            uuid.UUID      `json:"id"`
	ProjectID     uuid.UUID      `json:"projectId"`
	ContentTypeID uuid.UUID      `json:"contentTypeId"`
	DocumentID    uuid.UUID      `json:"documentId"`
	Locale        string         `json:"locale"`
	Status        string         `json:"status"`
	Data          map[string]any `json:"data"`
	Version       int            `json:"version"`
	CreatedBy     *uuid.UUID     `json:"createdBy,omitempty"`
	CreatedAt     time.Time      `json:"createdAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
}

type Service struct {
	pool     *pgxpool.Pool
	registry *schema.Registry
}

func NewService(pool *pgxpool.Pool, registry *schema.Registry) *Service {
	return &Service{pool: pool, registry: registry}
}

func (s *Service) resolveContentType(projectID uuid.UUID, apiID string) (schema.ContentType, error) {
	ct, ok := s.registry.Get(projectID, apiID)
	if !ok {
		return schema.ContentType{}, fmt.Errorf("content type %q not found", apiID)
	}
	return ct, nil
}

// Create validates data (including relation fields) against the content
// type's active schema, inserts a new draft document attributed to
// createdBy, and persists any relation fields as their own snapshot
// tied to this draft row.
func (s *Service) Create(ctx context.Context, projectID uuid.UUID, apiID, locale string, createdBy uuid.UUID, data map[string]any) (Entry, error) {
	ct, err := s.resolveContentType(projectID, apiID)
	if err != nil {
		return Entry{}, err
	}
	if err := ct.Schema.ValidateEntry(data); err != nil {
		return Entry{}, fmt.Errorf("validation failed: %w", err)
	}
	jsonFields, relationValues, err := splitRelationFields(ct.Schema, data)
	if err != nil {
		return Entry{}, fmt.Errorf("validation failed: %w", err)
	}
	if err := s.validateRelationTargets(ctx, projectID, ct.Schema, relationValues); err != nil {
		return Entry{}, err
	}
	if locale == "" {
		locale = "en"
	}

	documentID := uuid.New()
	dataBytes, err := json.Marshal(jsonFields)
	if err != nil {
		return Entry{}, fmt.Errorf("marshal data: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Entry{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var e Entry
	err = tx.QueryRow(ctx, `
		INSERT INTO entries (project_id, content_type_id, document_id, locale, status, data, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, project_id, content_type_id, document_id, locale, status, data, version, created_by, created_at, updated_at`,
		projectID, ct.ID, documentID, locale, StatusDraft, dataBytes, createdBy,
	).Scan(&e.ID, &e.ProjectID, &e.ContentTypeID, &e.DocumentID, &e.Locale, &e.Status, &dataBytes, &e.Version, &e.CreatedBy, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return Entry{}, fmt.Errorf("insert entry: %w", err)
	}
	if err := setRelations(ctx, tx, projectID, e.ID, relationValues); err != nil {
		return Entry{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Entry{}, fmt.Errorf("commit create: %w", err)
	}

	if err := json.Unmarshal(dataBytes, &e.Data); err != nil {
		return Entry{}, fmt.Errorf("unmarshal data: %w", err)
	}
	if err := s.attachRelations(ctx, projectID, ct.Schema, []Entry{e}); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// Update overwrites the draft variant's data (and any relation fields
// present in the request), validating against the active schema and
// enforcing optimistic concurrency on expectedVersion. If
// requireOwnership is true, the caller must be the draft's original
// creator (used to gate Author-role callers; Editor and above pass
// requireOwnership=false and may edit anyone's draft).
func (s *Service) Update(ctx context.Context, projectID uuid.UUID, apiID string, documentID uuid.UUID, locale string, expectedVersion int, data map[string]any, callerID uuid.UUID, requireOwnership bool) (Entry, error) {
	ct, err := s.resolveContentType(projectID, apiID)
	if err != nil {
		return Entry{}, err
	}
	if err := ct.Schema.ValidateEntry(data); err != nil {
		return Entry{}, fmt.Errorf("validation failed: %w", err)
	}
	jsonFields, relationValues, err := splitRelationFields(ct.Schema, data)
	if err != nil {
		return Entry{}, fmt.Errorf("validation failed: %w", err)
	}
	if err := s.validateRelationTargets(ctx, projectID, ct.Schema, relationValues); err != nil {
		return Entry{}, err
	}
	if locale == "" {
		locale = "en"
	}
	dataBytes, err := json.Marshal(jsonFields)
	if err != nil {
		return Entry{}, fmt.Errorf("marshal data: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Entry{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var existingID uuid.UUID
	var existingCreatedBy *uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id, created_by FROM entries
		WHERE project_id = $1 AND content_type_id = $2 AND document_id = $3 AND locale = $4 AND status = $5
		FOR UPDATE`,
		projectID, ct.ID, documentID, locale, StatusDraft,
	).Scan(&existingID, &existingCreatedBy)
	if err == pgx.ErrNoRows {
		return Entry{}, fmt.Errorf("draft not found")
	}
	if err != nil {
		return Entry{}, fmt.Errorf("load draft: %w", err)
	}
	if requireOwnership && (existingCreatedBy == nil || *existingCreatedBy != callerID) {
		return Entry{}, ErrNotOwner
	}

	var e Entry
	var scannedDataBytes []byte
	err = tx.QueryRow(ctx, `
		UPDATE entries
		SET data = $1, version = version + 1, updated_at = now()
		WHERE id = $2 AND version = $3
		RETURNING id, project_id, content_type_id, document_id, locale, status, data, version, created_at, updated_at`,
		dataBytes, existingID, expectedVersion,
	).Scan(&e.ID, &e.ProjectID, &e.ContentTypeID, &e.DocumentID, &e.Locale, &e.Status, &scannedDataBytes, &e.Version, &e.CreatedAt, &e.UpdatedAt)
	if err == pgx.ErrNoRows {
		return Entry{}, fmt.Errorf("entry not found or version %d is stale: another edit may have happened concurrently", expectedVersion)
	}
	if err != nil {
		return Entry{}, fmt.Errorf("update entry: %w", err)
	}
	if err := setRelations(ctx, tx, projectID, e.ID, relationValues); err != nil {
		return Entry{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Entry{}, fmt.Errorf("commit update: %w", err)
	}

	if err := json.Unmarshal(scannedDataBytes, &e.Data); err != nil {
		return Entry{}, fmt.Errorf("unmarshal data: %w", err)
	}
	if err := s.attachRelations(ctx, projectID, ct.Schema, []Entry{e}); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// Publish validates the current draft (its stored fields plus its
// current relation snapshot) against the active schema and atomically
// promotes it to the published variant, copying the draft's relations
// onto the published row as an independent snapshot of their own —
// subsequent draft edits never retroactively change what's published.
func (s *Service) Publish(ctx context.Context, projectID uuid.UUID, apiID string, documentID uuid.UUID, locale string) (Entry, error) {
	ct, err := s.resolveContentType(projectID, apiID)
	if err != nil {
		return Entry{}, err
	}
	if locale == "" {
		locale = "en"
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Entry{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var draftID uuid.UUID
	var draftData []byte
	err = tx.QueryRow(ctx, `
		SELECT id, data FROM entries
		WHERE project_id = $1 AND content_type_id = $2 AND document_id = $3 AND locale = $4 AND status = $5`,
		projectID, ct.ID, documentID, locale, StatusDraft,
	).Scan(&draftID, &draftData)
	if err == pgx.ErrNoRows {
		return Entry{}, fmt.Errorf("no draft found for this document")
	}
	if err != nil {
		return Entry{}, fmt.Errorf("load draft: %w", err)
	}

	var data map[string]any
	if err := json.Unmarshal(draftData, &data); err != nil {
		return Entry{}, fmt.Errorf("unmarshal draft data: %w", err)
	}

	relFields := ct.Schema.RelationFields()
	if len(relFields) > 0 {
		draftRelations, err := loadRelationIDs(ctx, tx, draftID)
		if err != nil {
			return Entry{}, err
		}
		for _, f := range relFields {
			data[f.Name] = shapeRelationValue(f, draftRelations[f.Name])
		}
	}
	if err := ct.Schema.ValidateEntry(data); err != nil {
		return Entry{}, fmt.Errorf("draft fails validation, cannot publish: %w", err)
	}

	var e Entry
	var scannedDataBytes []byte
	err = tx.QueryRow(ctx, `
		INSERT INTO entries (project_id, content_type_id, document_id, locale, status, data)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (document_id, locale, status)
		DO UPDATE SET data = EXCLUDED.data, version = entries.version + 1, updated_at = now()
		RETURNING id, project_id, content_type_id, document_id, locale, status, data, version, created_at, updated_at`,
		projectID, ct.ID, documentID, locale, StatusPublished, draftData,
	).Scan(&e.ID, &e.ProjectID, &e.ContentTypeID, &e.DocumentID, &e.Locale, &e.Status, &scannedDataBytes, &e.Version, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return Entry{}, fmt.Errorf("promote to published: %w", err)
	}
	if err := copyRelations(ctx, tx, projectID, draftID, e.ID, relFields); err != nil {
		return Entry{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Entry{}, fmt.Errorf("commit publish: %w", err)
	}
	if err := json.Unmarshal(scannedDataBytes, &e.Data); err != nil {
		return Entry{}, fmt.Errorf("unmarshal data: %w", err)
	}
	if err := s.attachRelations(ctx, projectID, ct.Schema, []Entry{e}); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// Unpublish deletes the published variant. Its relation snapshot is
// cleaned up automatically by the entry_relations FK on entries(id).
func (s *Service) Unpublish(ctx context.Context, projectID uuid.UUID, apiID string, documentID uuid.UUID, locale string) error {
	ct, err := s.resolveContentType(projectID, apiID)
	if err != nil {
		return err
	}
	if locale == "" {
		locale = "en"
	}
	_, err = s.pool.Exec(ctx, `
		DELETE FROM entries
		WHERE project_id = $1 AND content_type_id = $2 AND document_id = $3 AND locale = $4 AND status = $5`,
		projectID, ct.ID, documentID, locale, StatusPublished)
	if err != nil {
		return fmt.Errorf("unpublish: %w", err)
	}
	return nil
}

// List returns entries of the given status, newest first, with simple
// limit/offset pagination. Cursor pagination and filtering/sorting by
// field are tracked as pending in PROGRESS.md. redactPrivate strips any
// field marked Private from the response (pass true for public/delivery
// callers, false for the authenticated management API). include names
// relation fields to expand into full related-entry data instead of
// bare document id stubs.
func (s *Service) List(ctx context.Context, projectID uuid.UUID, apiID, status, locale string, limit, offset int, include []string, redactPrivateFields bool) ([]Entry, error) {
	ct, err := s.resolveContentType(projectID, apiID)
	if err != nil {
		return nil, err
	}
	if locale == "" {
		locale = "en"
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, project_id, content_type_id, document_id, locale, status, data, version, created_at, updated_at
		FROM entries
		WHERE project_id = $1 AND content_type_id = $2 AND status = $3 AND locale = $4
		ORDER BY created_at DESC
		LIMIT $5 OFFSET $6`,
		projectID, ct.ID, status, locale, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list entries: %w", err)
	}

	var out []Entry
	for rows.Next() {
		var e Entry
		var dataBytes []byte
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.ContentTypeID, &e.DocumentID, &e.Locale, &e.Status, &dataBytes, &e.Version, &e.CreatedAt, &e.UpdatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan entry: %w", err)
		}
		if err := json.Unmarshal(dataBytes, &e.Data); err != nil {
			rows.Close()
			return nil, fmt.Errorf("unmarshal data: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	if err := s.attachRelations(ctx, projectID, ct.Schema, out); err != nil {
		return nil, err
	}
	if err := s.expandIncludes(ctx, projectID, ct.Schema, status, out, include); err != nil {
		return nil, err
	}
	if redactPrivateFields {
		for i := range out {
			redactPrivate(ct.Schema, out[i].Data)
		}
	}
	return out, nil
}

func (s *Service) Get(ctx context.Context, projectID uuid.UUID, apiID string, documentID uuid.UUID, status, locale string, include []string, redactPrivateFields bool) (Entry, error) {
	ct, err := s.resolveContentType(projectID, apiID)
	if err != nil {
		return Entry{}, err
	}
	if locale == "" {
		locale = "en"
	}
	var e Entry
	var dataBytes []byte
	err = s.pool.QueryRow(ctx, `
		SELECT id, project_id, content_type_id, document_id, locale, status, data, version, created_at, updated_at
		FROM entries
		WHERE project_id = $1 AND content_type_id = $2 AND document_id = $3 AND status = $4 AND locale = $5`,
		projectID, ct.ID, documentID, status, locale,
	).Scan(&e.ID, &e.ProjectID, &e.ContentTypeID, &e.DocumentID, &e.Locale, &e.Status, &dataBytes, &e.Version, &e.CreatedAt, &e.UpdatedAt)
	if err == pgx.ErrNoRows {
		return Entry{}, fmt.Errorf("entry not found")
	}
	if err != nil {
		return Entry{}, fmt.Errorf("get entry: %w", err)
	}
	if err := json.Unmarshal(dataBytes, &e.Data); err != nil {
		return Entry{}, fmt.Errorf("unmarshal data: %w", err)
	}

	entries := []Entry{e}
	if err := s.attachRelations(ctx, projectID, ct.Schema, entries); err != nil {
		return Entry{}, err
	}
	if err := s.expandIncludes(ctx, projectID, ct.Schema, status, entries, include); err != nil {
		return Entry{}, err
	}
	if redactPrivateFields {
		redactPrivate(ct.Schema, entries[0].Data)
	}
	return entries[0], nil
}
