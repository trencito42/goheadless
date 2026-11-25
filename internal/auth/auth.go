// Package auth implements owner/user authentication: password login,
// server-side sessions, and the first-run owner bootstrap.
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

type User struct {
	ID        uuid.UUID
	Email     string
	IsOwner   bool
	CreatedAt time.Time
}

type Session struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	ExpiresAt time.Time
}

type Service struct {
	pool *pgxpool.Pool
	ttl  time.Duration
}

func NewService(pool *pgxpool.Pool, ttl time.Duration) *Service {
	return &Service{pool: pool, ttl: ttl}
}

// BootstrapOwner creates the first owner account. It fails if any user
// already exists, so it can only ever run once per installation.
func (s *Service) BootstrapOwner(ctx context.Context, email, password string) (User, error) {
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		return User{}, fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		return User{}, fmt.Errorf("an owner account already exists; refusing to bootstrap again")
	}
	return s.createUser(ctx, email, password, true)
}

// CreateUser creates a non-owner account. There is no public
// registration; this is only reachable through an owner-gated route
// (see internal/http/router.go), which is the entire invitation flow
// for now — see PROGRESS.md for the real invite flow this stands in for.
func (s *Service) CreateUser(ctx context.Context, email, password string) (User, error) {
	return s.createUser(ctx, email, password, false)
}

func (s *Service) createUser(ctx context.Context, email, password string, isOwner bool) (User, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	var u User
	err = s.pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, is_owner) VALUES ($1, $2, $3)
		 RETURNING id, email, is_owner, created_at`,
		email, hash, isOwner,
	).Scan(&u.ID, &u.Email, &u.IsOwner, &u.CreatedAt)
	if err != nil {
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	return u, nil
}

// Login verifies credentials and issues a new session.
func (s *Service) Login(ctx context.Context, email, password string) (User, Session, error) {
	var u User
	var hash string
	err := s.pool.QueryRow(ctx,
		`SELECT id, email, password_hash, is_owner, created_at FROM users WHERE email = $1`, email,
	).Scan(&u.ID, &u.Email, &hash, &u.IsOwner, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, Session{}, fmt.Errorf("lookup user: %w", err)
	}

	ok, err := VerifyPassword(hash, password)
	if err != nil {
		return User{}, Session{}, fmt.Errorf("verify password: %w", err)
	}
	if !ok {
		return User{}, Session{}, ErrInvalidCredentials
	}

	sess, err := s.createSession(ctx, u.ID)
	if err != nil {
		return User{}, Session{}, err
	}
	return u, sess, nil
}

func (s *Service) createSession(ctx context.Context, userID uuid.UUID) (Session, error) {
	expiresAt := time.Now().Add(s.ttl)
	var sess Session
	err := s.pool.QueryRow(ctx,
		`INSERT INTO sessions (user_id, expires_at) VALUES ($1, $2) RETURNING id, user_id, expires_at`,
		userID, expiresAt,
	).Scan(&sess.ID, &sess.UserID, &sess.ExpiresAt)
	if err != nil {
		return Session{}, fmt.Errorf("create session: %w", err)
	}
	return sess, nil
}

// Authenticate resolves a session cookie value to its owning user. A
// missing or expired session is reported as ErrInvalidCredentials rather
// than a distinct error, so callers can't distinguish "no session" from
// "bad session" and leak that detail to a client.
func (s *Service) Authenticate(ctx context.Context, sessionID uuid.UUID) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT u.id, u.email, u.is_owner, u.created_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.id = $1 AND s.expires_at > now()`,
		sessionID,
	).Scan(&u.ID, &u.Email, &u.IsOwner, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrInvalidCredentials
	}
	if err != nil {
		return User{}, fmt.Errorf("lookup session: %w", err)
	}
	return u, nil
}

// ListUsers is owner-only (gated in the router) — it exists so an owner
// can look up a user's id by email before adding them to a project.
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, email, is_owner, created_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.IsOwner, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Service) Logout(ctx context.Context, sessionID uuid.UUID) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, sessionID); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
