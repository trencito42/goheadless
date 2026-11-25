# CMS (working name, repo: goheadless)

A self-hosted, API-first headless CMS. Go backend (chi + pgx + Postgres),
React/TypeScript admin frontend built on the
[b4 design system](https://github.com/trencito42/b4).

See [docs/ROADMAP.md](docs/ROADMAP.md) for scope and phases, and
[PROGRESS.md](PROGRESS.md) for exactly what is implemented, verified, and
pending right now.

## Prerequisites

- Go 1.23+
- Node.js 22+ and pnpm (`corepack enable`)
- PostgreSQL 16+ running locally (or via `deployments/docker-compose.dev.yml`
  once Docker is available)

## Running locally

1. Create a database and role:

   ```sql
   CREATE USER cms WITH PASSWORD 'cms_dev_password';
   CREATE DATABASE cms OWNER cms;
   ```

2. Bootstrap the first owner account (migrations run automatically):

   ```powershell
   $env:CMS_DATABASE_URL = "postgres://cms:cms_dev_password@localhost:5432/cms"
   go run ./cmd/cms bootstrap-owner you@example.com "your-password"
   ```

3. Start the API server:

   ```powershell
   $env:CMS_DATABASE_URL = "postgres://cms:cms_dev_password@localhost:5432/cms"
   $env:CMS_CORS_ORIGINS = "http://localhost:5174"
   go run ./cmd/cms
   ```

4. Build the design system once, then start the admin frontend:

   ```powershell
   pnpm install
   pnpm build:design-system
   pnpm --filter admin dev
   ```

   Open http://localhost:5174 and sign in with the account from step 2.

## Repository layout

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).
