# Multi-stage build: compiles the admin frontend, embeds it into the Go
# binary via go:embed, and ships a single statically-linked executable.
#
# NOT YET VERIFIED: Docker Desktop could not be exercised in the
# environment this was written in (WSL2 prerequisite, needs a reboot).
# See PROGRESS.md.

FROM node:22-alpine AS frontend
WORKDIR /src
RUN corepack enable
COPY pnpm-workspace.yaml package.json ./
COPY apps/admin apps/admin
COPY packages/b4 packages/b4
COPY packages/sdk packages/sdk
RUN pnpm install --frozen-lockfile
RUN pnpm build:admin

FROM golang:1.27-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/apps/admin/dist ./apps/admin/dist
RUN CGO_ENABLED=0 go build -o /out/cms ./cmd/cms
RUN CGO_ENABLED=0 go build -o /out/worker ./cmd/worker 2>/dev/null || true

FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=backend /out/cms /app/cms
COPY --from=backend /src/migrations /app/migrations
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app/cms"]
