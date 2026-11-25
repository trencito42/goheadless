# API

Base URL: `http://localhost:8080` in dev. All management routes require a
session cookie (`cms_session`, HttpOnly). No OpenAPI spec is generated
yet — this is the hand-written source of truth until that lands.

## Auth

- `POST /api/v1/auth/login` — `{email, password}` → sets session cookie,
  returns `{id, email, isOwner}`.
- `POST /api/v1/auth/logout` — clears the session.
- `GET /api/v1/auth/me` — 401 if not authenticated, else the current user.

## Users (owner-only — no public registration)

- `POST /api/v1/manage/users/` — `{email, password}` → creates a
  non-owner account. The whole "invite a collaborator" flow for now: no
  invite email, the owner sets the password directly.
- `GET /api/v1/manage/users/` — list users (to find an id for the members
  endpoint below).

## Management API (authenticated + project-role-gated; see docs/SECURITY.md)

- `POST /api/v1/manage/projects/` — `{name, slug}`. Owner-only.
- `GET /api/v1/manage/projects/` — owner sees all; everyone else sees
  only projects they're a member of.
- `GET/POST /api/v1/manage/projects/{projectID}/members/` —
  `{userId, role}`, role one of `viewer|author|editor|administrator`.
  Administrator required.
- `DELETE /api/v1/manage/projects/{projectID}/members/{userID}` —
  administrator required.
- `POST /api/v1/manage/projects/{projectID}/content-types/` —
  `{apiId, displayName, kind, schema: {fields: [...]}}`.
- `GET /api/v1/manage/projects/{projectID}/content-types/`.
- `POST /api/v1/manage/projects/{projectID}/content/{collection}/` —
  `{locale?, data}` → creates a draft.
- `GET /api/v1/manage/projects/{projectID}/content/{collection}/` —
  lists drafts (query: `locale`, `limit`, `offset`).
- `PATCH .../{collection}/{documentID}` — `{locale?, expectedVersion,
  data}`. 409 if `expectedVersion` is stale (optimistic concurrency).
- `POST .../{collection}/{documentID}/publish` — `?locale=` optional.
- `POST .../{collection}/{documentID}/unpublish`.

## Delivery API (published only)

- `GET /api/v1/projects/{projectID}/content/{collection}/` — query:
  `locale`, `limit`, `offset`.
- `GET /api/v1/projects/{projectID}/content/{collection}/{documentID}` —
  query: `locale`.

Currently still gated behind viewer-or-above project membership
(anonymous published-content access is planned but not implemented — see
PROGRESS.md). No field selection, relation expansion, filter operators,
or cursor pagination yet; only `limit`/`offset`.

## Schema field types

`short_text`, `long_text`, `rich_text`, `integer`, `decimal`, `boolean`,
`email`, `url`, `date` (`YYYY-MM-DD`), `datetime` (RFC3339),
`enumeration` (requires `enum: [...]`), `json`. See PROGRESS.md for the
full list of field types from the spec not yet implemented.
