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
  `{locale?, data}` → creates a draft. Author role or above.
- `GET /api/v1/manage/projects/{projectID}/content/{collection}/` —
  lists drafts (query: `locale`, `limit`, `offset`, `include`).
- `GET .../{collection}/{documentID}` — a single draft (query: `locale`,
  `include`).
- `PATCH .../{collection}/{documentID}` — `{locale?, expectedVersion,
  data}`. 409 if `expectedVersion` is stale (optimistic concurrency). An
  **Author**-role caller may only edit a draft they created themselves
  (403 `ErrNotOwner` otherwise); **Editor** and above may edit anyone's.
- `POST .../{collection}/{documentID}/publish` — `?locale=` optional.
  Re-validates the draft (including its current relation values) and
  copies its relation snapshot onto the published row — see
  `docs/adr/0002-relations.md` and `docs/adr/0003-document-identity.md`.
  409 (`ErrRelationConflict`) if a `one_to_one` relation field's target
  is already claimed by another document's published row through the
  same field.
- `POST .../{collection}/{documentID}/unpublish`.

`POST`/`PATCH` on content also return 409 `ErrRelationConflict` for the
same `one_to_one` reason.

## Delivery API (published only)

- `GET /api/v1/projects/{projectID}/content/{collection}/` — query:
  `locale`, `limit`, `offset`, `include`.
- `GET /api/v1/projects/{projectID}/content/{collection}/{documentID}` —
  query: `locale`, `include`.

Currently still gated behind viewer-or-above project membership
(anonymous published-content access is planned but not implemented — see
PROGRESS.md). No field selection, filter operators, or cursor pagination
yet; only `limit`/`offset`. Fields marked `"private": true` on the
content type are stripped from every delivery-API response (both list
and get) but remain visible on the management (draft) API, since
editors need to see and edit them.

A delivery read also never hands back a relation stub id that doesn't
resolve to an actual published document in the requested locale (a
to-one stub becomes `null`, a to-many stub drops that entry): a source
document's own last publish can go stale if its *target* gets
unpublished afterward, and the delivery API treats that as "no
relation" rather than delivering an id a client can't do anything with.
The management (draft) API shows the raw stub regardless, since editors
need to see and fix it.

### Relation fields and `?include=`

A `relation` field's value in `data` is, by default, a **stub**: a bare
document id string for `one_to_one`/`many_to_one`, or an array of id
strings for `one_to_many`/`many_to_many`. Pass `?include=fieldName` (comma-
separated for several fields) to expand one or more relation fields
into their full related-entry data instead of bare ids:

```
GET /api/v1/projects/{projectID}/content/post/{documentID}?include=author
```

```json
{ "data": { "title": "Hello", "author": { "documentId": "...", "data": { "name": "Ada" } } } }
```

Expansion is always one query per included field (never one per entry,
never recursive — an expanded entry's own relation fields are left as
stubs), is scoped to the same `locale` as the outer request (a target
with published rows in several locales only ever resolves to the one
matching the request), and its `data` always has that content type's
private fields redacted, regardless of whether the outer request is a
draft or delivery read.

Draft and published variants of the same document hold **independent**
relation snapshots: editing a draft's relation field never changes what
the published variant shows until that draft is published again.

## Schema field types

`short_text`, `long_text`, `rich_text`, `integer`, `decimal`, `boolean`,
`email`, `url`, `date` (`YYYY-MM-DD`), `datetime` (RFC3339),
`enumeration` (requires `enum: [...]`), `json`, `relation` (requires
`relation: {targetContentType, relationType}`, relationType one of
`one_to_one|one_to_many|many_to_one|many_to_many`; the target must be a
content type in the same project — self-reference, i.e. the content
type referencing itself, is allowed). See PROGRESS.md for the full list
of field types from the spec not yet implemented.
