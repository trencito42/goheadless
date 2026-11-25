# Security

Current state, plainly. See PROGRESS.md for what's planned vs. built.

## In place

- Passwords hashed with Argon2id (`internal/auth/password.go`), random
  16-byte salt per password, constant-time comparison on verify.
- Sessions are server-side (a row in `sessions`, not a signed client
  token), random UUID, HttpOnly + SameSite=Lax cookie, expire server-side.
- All SQL is parameterized (`$1, $2, ...` via pgx) — no string
  concatenation of untrusted input into queries anywhere.
- Content writes are validated against the active schema and reject
  unexpected fields outright (no silent drop, no mass assignment).
- CORS is an explicit allowlist (`CMS_CORS_ORIGINS` env var); empty by
  default, which disables cross-origin requests rather than defaulting
  open.
- No public registration; the only way to create the first user is the
  `bootstrap-owner` CLI subcommand, which refuses to run if any user
  already exists.

## Known gaps (not yet addressed)

- No RBAC: every authenticated user can read/write every project right
  now. No per-project roles, no field-level permissions.
- No rate limiting or brute-force protection on login.
- No MFA, no password reset flow, no login history / audit log.
- No API tokens yet (everything is session-cookie-only).
- No CSRF protection beyond SameSite=Lax (fine for now since there's no
  cross-origin cookie-based mutation path yet, but needs revisiting once
  there's a public delivery API or third-party integrations).
- No row-level security / database-level tenant isolation — isolation is
  currently enforced only in application code (every query is scoped by
  `project_id`, but there's no defense in depth if that's ever missed).
- Secrets: `CMS_DATABASE_URL` is read from env; nothing is logged.
  Nothing has been audited for secret leakage into logs/responses yet
  because there isn't much surface area yet — revisit as auth/tokens
  grow.
