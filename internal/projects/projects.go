// Package projects implements the top-level tenant boundary: every
// content type, entry, and media asset belongs to exactly one project.
package projects

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Project struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (s *Service) Create(ctx context.Context, name, slug string) (Project, error) {
	if name == "" {
		return Project{}, fmt.Errorf("name is required")
	}
	if !slugPattern.MatchString(slug) {
		return Project{}, fmt.Errorf("slug must be lowercase alphanumeric with hyphens")
	}
	var p Project
	err := s.pool.QueryRow(ctx,
		`INSERT INTO projects (name, slug) VALUES ($1, $2)
		 RETURNING id, name, slug, created_at, updated_at`,
		name, slug,
	).Scan(&p.ID, &p.Name, &p.Slug, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return Project{}, fmt.Errorf("insert project: %w", err)
	}
	return p, nil
}

func (s *Service) List(ctx context.Context) ([]Project, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, slug, created_at, updated_at FROM projects ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Slug, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) GetBySlug(ctx context.Context, slug string) (Project, error) {
	var p Project
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, slug, created_at, updated_at FROM projects WHERE slug = $1`, slug,
	).Scan(&p.ID, &p.Name, &p.Slug, &p.CreatedAt, &p.UpdatedAt)
	if err == pgx.ErrNoRows {
		return Project{}, fmt.Errorf("project %q not found", slug)
	}
	if err != nil {
		return Project{}, fmt.Errorf("get project: %w", err)
	}
	return p, nil
}
