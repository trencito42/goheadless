# Architecture

Modular monolith, Go backend + React admin frontend. See PROGRESS.md for
what of this is actually built vs. planned.

## Repository layout

```
cmd/cms            entry point: HTTP server + bootstrap-owner subcommand
cmd/worker         planned: background job worker (not started)
internal/auth      password hashing, sessions, login/logout/me
internal/projects  tenant boundary
internal/schema    content type registry: definitions, validation, cache
internal/content   entry CRUD, draft/publish lifecycle
internal/storage   pgxpool setup, forward-only .sql migration runner
internal/http      router wiring (imports every feature package)
internal/httpx     shared JSON response helpers (imports nothing feature-
                   specific, to avoid a cycle with internal/http)
internal/config    env-based configuration
migrations/        plain .sql files, applied in filename order
apps/admin         React admin frontend (Vite)
packages/b4        git submodule: trencito42/b4 design system
packages/sdk       placeholder for the future TypeScript SDK
```

## Why hybrid JSONB storage

`entries.data` is JSONB; everything that's true for every entry
regardless of its content type (project, content type, document id,
locale, status, version, timestamps) is a real column. This means
uniqueness, status, and ownership are enforced by Postgres constraints
and indexed normally, while the runtime-configurable fields live in
JSONB and don't require a migration every time someone adds a field in
the Content Type Builder. See docs/DATABASE.md.

## Draft/publish model

`document_id` is the stable identity of a logical document. A draft and
a published variant are two separate rows sharing the same
`(document_id, locale)` but different `status`, enforced by a unique
constraint on `(document_id, locale, status)`. Publishing re-validates
the draft against the *current* schema and upserts it into the published
row in one transaction — it never trusts that an old draft still matches
the schema.

## Request flow

`internal/http.NewRouter` is the single place every route is registered.
Session auth (`internal/auth.Middleware`) runs for every request and
attaches a user to context if a valid session cookie is present, but
doesn't reject anything by itself — `requireAuth` (router-level) or
`auth.RequireUser` (handler-level) do that. This keeps auth-optional
routes (currently none, but e.g. anonymous delivery access later)
possible without duplicating the session-resolution logic.
