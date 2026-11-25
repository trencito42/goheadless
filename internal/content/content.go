// Package content is the content management and delivery engine: CRUD
// over entries belonging to a runtime-defined content type, and the
// draft/publish lifecycle.
package content

import (
	"context"
	"encoding/json"
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

type Entry struct {
	ID            uuid.UUID      `json:"id"`
	ProjectID     uuid.UUID      `json:"projectId"`
	ContentTypeID uuid.UUID      `json:"contentTypeId"`
	DocumentID    uuid.UUID      `json:"documentId"`
	Locale        string         `json:"locale"`
	Status        string         `json:"status"`
	Data          map[string]any `json:"data"`
	Version       int            `json:"version"`
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

// Create validates data against the content type's active schema and
// inserts a new draft document.
func (s *Service) Create(ctx context.Context, projectID uuid.UUID, apiID, locale string, data map[string]any) (Entry, error) {
	ct, err := s.resolveContentType(projectID, apiID)
	if err != nil {
		return Entry{}, err
	}
	if err := ct.Schema.ValidateEntry(data); err != nil {
		return Entry{}, fmt.Errorf("validation failed: %w", err)
	}
	if locale == "" {
		locale = "en"
	}

	documentID := uuid.New()
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return Entry{}, fmt.Errorf("marshal data: %w", err)
	}

	var e Entry
	err = s.pool.QueryRow(ctx, `
		INSERT INTO entries (project_id, content_type_id, document_id, locale, status, data)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, project_id, content_type_id, document_id, locale, status, data, version, created_at, updated_at`,
		projectID, ct.ID, documentID, locale, StatusDraft, dataBytes,
	).Scan(&e.ID, &e.ProjectID, &e.ContentTypeID, &e.DocumentID, &e.Locale, &e.Status, &dataBytes, &e.Version, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return Entry{}, fmt.Errorf("insert entry: %w", err)
	}
	if err := json.Unmarshal(dataBytes, &e.Data); err != nil {
		return Entry{}, fmt.Errorf("unmarshal data: %w", err)
	}
	return e, nil
}

// Update overwrites the draft variant's data, validating against the
// active schema and enforcing optimistic concurrency on expectedVersion.
func (s *Service) Update(ctx context.Context, projectID uuid.UUID, apiID string, documentID uuid.UUID, locale string, expectedVersion int, data map[string]any) (Entry, error) {
	ct, err := s.resolveContentType(projectID, apiID)
	if err != nil {
		return Entry{}, err
	}
	if err := ct.Schema.ValidateEntry(data); err != nil {
		return Entry{}, fmt.Errorf("validation failed: %w", err)
	}
	if locale == "" {
		locale = "en"
	}
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return Entry{}, fmt.Errorf("marshal data: %w", err)
	}

	var e Entry
	var scannedDataBytes []byte
	err = s.pool.QueryRow(ctx, `
		UPDATE entries
		SET data = $1, version = version + 1, updated_at = now()
		WHERE project_id = $2 AND content_type_id = $3 AND document_id = $4
		  AND locale = $5 AND status = $6 AND version = $7
		RETURNING id, project_id, content_type_id, document_id, locale, status, data, version, created_at, updated_at`,
		dataBytes, projectID, ct.ID, documentID, locale, StatusDraft, expectedVersion,
	).Scan(&e.ID, &e.ProjectID, &e.ContentTypeID, &e.DocumentID, &e.Locale, &e.Status, &scannedDataBytes, &e.Version, &e.CreatedAt, &e.UpdatedAt)
	if err == pgx.ErrNoRows {
		return Entry{}, fmt.Errorf("entry not found or version %d is stale: another edit may have happened concurrently", expectedVersion)
	}
	if err != nil {
		return Entry{}, fmt.Errorf("update entry: %w", err)
	}
	if err := json.Unmarshal(scannedDataBytes, &e.Data); err != nil {
		return Entry{}, fmt.Errorf("unmarshal data: %w", err)
	}
	return e, nil
}

// Publish validates the current draft against the active schema and
// atomically promotes it to the published variant.
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

	var draftData []byte
	err = tx.QueryRow(ctx, `
		SELECT data FROM entries
		WHERE project_id = $1 AND content_type_id = $2 AND document_id = $3 AND locale = $4 AND status = $5`,
		projectID, ct.ID, documentID, locale, StatusDraft,
	).Scan(&draftData)
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

	if err := tx.Commit(ctx); err != nil {
		return Entry{}, fmt.Errorf("commit publish: %w", err)
	}
	if err := json.Unmarshal(scannedDataBytes, &e.Data); err != nil {
		return Entry{}, fmt.Errorf("unmarshal data: %w", err)
	}
	return e, nil
}

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
// field are tracked as pending in PROGRESS.md.
func (s *Service) List(ctx context.Context, projectID uuid.UUID, apiID, status, locale string, limit, offset int) ([]Entry, error) {
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
	defer rows.Close()

	var out []Entry
	for rows.Next() {
		var e Entry
		var dataBytes []byte
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.ContentTypeID, &e.DocumentID, &e.Locale, &e.Status, &dataBytes, &e.Version, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan entry: %w", err)
		}
		if err := json.Unmarshal(dataBytes, &e.Data); err != nil {
			return nil, fmt.Errorf("unmarshal data: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) Get(ctx context.Context, projectID uuid.UUID, apiID string, documentID uuid.UUID, status, locale string) (Entry, error) {
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
	return e, nil
}
