# Deployment

**Status: UNVERIFIED.** Docker Desktop was installed in the environment
this was written in but could not actually be exercised (missing WSL2,
which needs a reboot). Everything below is written to a reasonable spec,
not confirmed to work. Verify before relying on it — see PROGRESS.md.

## Local development

No Docker needed. See README.md — run Postgres natively, `go run
./cmd/cms`, and `pnpm --filter admin dev`.

## Docker (once Docker is actually available)

```powershell
docker compose -f deployments/docker-compose.dev.yml up -d
```

This brings up Postgres and Redis only; run the Go server and Vite dev
server on the host against them for hot reload.

`Dockerfile` at the repo root is a multi-stage build intended to produce
a single container image (frontend built with pnpm, embedded into a
`distroless` Go binary). It has never been built in this environment —
treat it as a draft.

## Environment variables

| Variable             | Required | Default | Notes                                   |
|-----------------------|----------|---------|------------------------------------------|
| `CMS_DATABASE_URL`    | yes      | —       | `postgres://user:pass@host:5432/db`       |
| `CMS_HTTP_ADDR`       | no       | `:8080` |                                            |
| `CMS_CORS_ORIGINS`    | no       | (none)  | comma-separated allowlist                 |
| `CMS_SESSION_TTL`     | no       | `720h`  | Go duration string                        |
| `CMS_REDIS_URL`       | no       | (none)  | not wired up to anything yet              |

## Not started

Caddy config example, backup/restore scripts, production security
checklist, health-check wiring beyond the bare `/health` and `/ready`
endpoints, non-root container verification (the Dockerfile sets
`USER nonroot:nonroot` but this has not been tested).
