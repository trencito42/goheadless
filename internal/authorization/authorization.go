// Package authorization resolves per-project roles and enforces them.
// A global owner (users.is_owner) bypasses this entirely — the checks
// here only matter for non-owner users. There is no "Project
// Administrator can see every project" shortcut: membership is the only
// way a non-owner user can see a project at all, including its mere
// existence, which is what keeps cross-project access from leaking
// through an enumerable project id.
package authorization

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Role string

const (
	RoleViewer        Role = "viewer"
	RoleAuthor        Role = "author"
	RoleEditor        Role = "editor"
	RoleAdministrator Role = "administrator"
)

// rank gives roles a total order so "at least editor" comparisons are a
// single integer comparison instead of a set-membership check that's
// easy to get wrong when a new role is inserted in the middle later.
var rank = map[Role]int{
	RoleViewer:        1,
	RoleAuthor:        2,
	RoleEditor:        3,
	RoleAdministrator: 4,
}

func (r Role) AtLeast(min Role) bool {
	return rank[r] >= rank[min]
}

var ErrForbidden = errors.New("forbidden")

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// RoleInProject returns the caller's effective role for a project, or
// ErrForbidden if they have none (not a member, and not a global owner).
// isOwner callers get RoleAdministrator back — the highest rank — so
// callers never need a separate "or is owner" branch.
func (s *Service) RoleInProject(ctx context.Context, userID uuid.UUID, isOwner bool, projectID uuid.UUID) (Role, error) {
	if isOwner {
		return RoleAdministrator, nil
	}
	var role Role
	err := s.pool.QueryRow(ctx,
		`SELECT role FROM project_members WHERE project_id = $1 AND user_id = $2`,
		projectID, userID,
	).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrForbidden
	}
	if err != nil {
		return "", fmt.Errorf("lookup project role: %w", err)
	}
	return role, nil
}

type Member struct {
	UserID uuid.UUID `json:"userId"`
	Email  string    `json:"email"`
	Role   Role      `json:"role"`
}

func (s *Service) AddMember(ctx context.Context, projectID, userID uuid.UUID, role Role) error {
	if _, ok := rank[role]; !ok {
		return fmt.Errorf("unknown role %q", role)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO project_members (project_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (project_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
		projectID, userID, role)
	if err != nil {
		return fmt.Errorf("add member: %w", err)
	}
	return nil
}

func (s *Service) RemoveMember(ctx context.Context, projectID, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM project_members WHERE project_id = $1 AND user_id = $2`, projectID, userID)
	if err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	return nil
}

func (s *Service) ListMembers(ctx context.Context, projectID uuid.UUID) ([]Member, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT u.id, u.email, pm.role
		FROM project_members pm
		JOIN users u ON u.id = pm.user_id
		WHERE pm.project_id = $1
		ORDER BY u.email`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()

	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Email, &m.Role); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListProjectIDsForUser returns every project a non-owner user belongs
// to. Global owners don't call this — they list every project directly.
func (s *Service) ListProjectIDsForUser(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT project_id FROM project_members WHERE user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("list member projects: %w", err)
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan project id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
