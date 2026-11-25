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

## RBAC

Per-project roles (viewer < author < editor < administrator) in
`internal/authorization`, enforced per-route in `internal/http/router.go`.
Global owners bypass membership checks. No public registration — the
owner creates accounts directly (`POST /api/v1/manage/users`, owner-only)
and adds them to projects with a role. A non-member and a too-low-role
member get an identical 403, so project existence can't be probed by a
member who isn't authorized for it. See PROGRESS.md for exactly what was
verified and what's still missing (field-level permissions, "edit only
your own entries" for authors, automated tests).

## Known gaps (not yet addressed)

- No field-level permissions, and authors can create entries but editing/
  publishing requires editor regardless of who created the entry (no
  "own entries only" restriction yet).
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
