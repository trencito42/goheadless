# CLAUDE.md

Project context for working on this repository. Read this, PROGRESS.md,
and docs/ARCHITECTURE.md before making changes.

## What this is

A self-hosted, API-first headless CMS (private/internal use, not a SaaS
product — see docs/ROADMAP.md section "strict boundaries"). Go backend,
React admin frontend built on the `trencito42/b4` design system (vendored
as a git submodule at `packages/b4`).

## Conventions

- Backend: Go, stdlib `net/http` + chi, pgx/pgxpool, explicit parameterized
  SQL (no ORM). `internal/httpx` holds shared response helpers and must
  never import a feature package (avoids import cycles with
  `internal/http`, which wires every feature's handlers into the router).
- Migrations are plain `.sql` files in `migrations/`, applied forward-only
  by `internal/storage.Migrate` on every process start. No down migrations.
- Frontend: Vite + React 19 + TypeScript strict mode, TanStack Query for
  data fetching. Components come from `@b4/react`; check actual prop
  interfaces in `packages/b4/packages/react/src/components/<Name>/<Name>.tsx`
  before using one — several (e.g. `Badge`) don't forward `onClick`/`style`.
- The design system packages (`@b4/tokens`, `@b4/motion`, `@b4/react`) must
  be built (`pnpm build:design-system`) before the admin app's dev server
  or build will resolve them — they ship no prebuilt `dist/` in the repo.
- Theme is applied via `data-b4-theme="graphite"` on `<html>` (set in
  `apps/admin/src/main.tsx`).

## Engineering workflow

Maintain PROGRESS.md as the source of truth across sessions: what's
IMPLEMENTED vs VERIFIED vs pending, known gaps, exact next steps. Don't
mark something VERIFIED without having actually run it. Don't restart
completed work — read PROGRESS.md and docs/ first.

Do not push to the remote or deploy without explicit authorization.
