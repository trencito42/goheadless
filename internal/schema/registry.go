package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ContentType struct {
	ID          uuid.UUID  `json:"id"`
	ProjectID   uuid.UUID  `json:"projectId"`
	APIID       string     `json:"apiId"`
	DisplayName string     `json:"displayName"`
	Kind        string     `json:"kind"` // "collection" | "single"
	Schema      Definition `json:"schema"`
	Version     int        `json:"schemaVersion"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// Registry is the in-memory compiled schema cache. It is read on every
// content request, so lookups never touch the database on the hot path;
// writes go straight to Postgres and then refresh the cache entry for
// that single content type.
type Registry struct {
	pool *pgxpool.Pool

	mu    sync.RWMutex
	byKey map[string]ContentType // key: projectID.String() + "/" + apiID
}

func NewRegistry(pool *pgxpool.Pool) *Registry {
	return &Registry{pool: pool, byKey: map[string]ContentType{}}
}

func key(projectID uuid.UUID, apiID string) string {
	return projectID.String() + "/" + apiID
}

// Warm loads every content type from the database into the cache. Call
// it once at startup; after that, writes keep the cache coherent.
func (r *Registry) Warm(ctx context.Context) error {
	rows, err := r.pool.Query(ctx, `
		SELECT id, project_id, api_id, display_name, kind, schema, schema_version, created_at, updated_at
		FROM content_types`)
	if err != nil {
		return fmt.Errorf("warm registry: %w", err)
	}
	defer rows.Close()

	loaded := map[string]ContentType{}
	for rows.Next() {
		ct, err := scanContentType(rows)
		if err != nil {
			return err
		}
		loaded[key(ct.ProjectID, ct.APIID)] = ct
	}
	if err := rows.Err(); err != nil {
		return err
	}

	r.mu.Lock()
	r.byKey = loaded
	r.mu.Unlock()
	return nil
}

func (r *Registry) Get(projectID uuid.UUID, apiID string) (ContentType, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ct, ok := r.byKey[key(projectID, apiID)]
	return ct, ok
}

func (r *Registry) ListForProject(projectID uuid.UUID) []ContentType {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []ContentType
	for _, ct := range r.byKey {
		if ct.ProjectID == projectID {
			out = append(out, ct)
		}
	}
	return out
}

// Create validates and persists a new content type, then refreshes the
// cache. Schema activation is therefore immediate and requires no
// restart.
func (r *Registry) Create(ctx context.Context, projectID uuid.UUID, apiID, displayName, kind string, def Definition) (ContentType, error) {
	if kind != "collection" && kind != "single" {
		return ContentType{}, fmt.Errorf("kind must be \"collection\" or \"single\"")
	}
	if err := def.Validate(); err != nil {
		return ContentType{}, fmt.Errorf("invalid schema: %w", err)
	}
	assignFieldIDs(def.Fields)
	for _, f := range def.RelationFields() {
		if f.Relation.TargetContentType == apiID {
			continue // self-referencing: the target is this content type itself.
		}
		var exists bool
		err := r.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM content_types WHERE project_id = $1 AND api_id = $2)`,
			projectID, f.Relation.TargetContentType,
		).Scan(&exists)
		if err != nil {
			return ContentType{}, fmt.Errorf("check relation target: %w", err)
		}
		if !exists {
			return ContentType{}, fmt.Errorf("field %q: relation target content type %q does not exist in this project", f.Name, f.Relation.TargetContentType)
		}
	}

	schemaBytes, err := json.Marshal(def)
	if err != nil {
		return ContentType{}, fmt.Errorf("marshal schema: %w", err)
	}

	row := r.pool.QueryRow(ctx, `
		INSERT INTO content_types (project_id, api_id, display_name, kind, schema)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, project_id, api_id, display_name, kind, schema, schema_version, created_at, updated_at`,
		projectID, apiID, displayName, kind, schemaBytes,
	)
	ct, err := scanContentType(row)
	if err != nil {
		return ContentType{}, fmt.Errorf("insert content type: %w", err)
	}

	r.mu.Lock()
	r.byKey[key(ct.ProjectID, ct.APIID)] = ct
	r.mu.Unlock()
	return ct, nil
}

// assignFieldIDs fills in a fresh id for every field in the tree
// (including nested component/dynamic-zone fields) whose id is still
// the zero UUID, in place. Called once, by Registry.Create, before a
// new content type's schema is first persisted.
func assignFieldIDs(fields []Field) {
	for i := range fields {
		if fields[i].ID == uuid.Nil {
			fields[i].ID = uuid.New()
		}
		if fields[i].Component != nil {
			assignFieldIDs(fields[i].Component.Fields)
		}
		if fields[i].DynamicZone != nil {
			for name, cc := range fields[i].DynamicZone.Components {
				assignFieldIDs(cc.Fields)
				fields[i].DynamicZone.Components[name] = cc
			}
		}
	}
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanContentType(row rowScanner) (ContentType, error) {
	var ct ContentType
	var schemaBytes []byte
	if err := row.Scan(&ct.ID, &ct.ProjectID, &ct.APIID, &ct.DisplayName, &ct.Kind,
		&schemaBytes, &ct.Version, &ct.CreatedAt, &ct.UpdatedAt); err != nil {
		if err == pgx.ErrNoRows {
			return ContentType{}, err
		}
		return ContentType{}, fmt.Errorf("scan content type: %w", err)
	}
	if err := json.Unmarshal(schemaBytes, &ct.Schema); err != nil {
		return ContentType{}, fmt.Errorf("unmarshal schema: %w", err)
	}
	return ct, nil
}
