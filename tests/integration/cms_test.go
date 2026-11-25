// Integration tests against a real PostgreSQL database, per the
// project's own rule that mocks must not substitute for integration
// verification of anything security- or correctness-critical (RBAC,
// publish lifecycle, cross-project isolation).
//
// Requires TEST_DATABASE_URL (defaults to a local "cms_test" database
// matching the dev setup in README.md). Skips if it can't connect only
// when TEST_DATABASE_URL was left unset — a developer machine with no
// Postgres running — so `go test ./...` still passes there, but these
// tests not running is not the same as them passing; see PROGRESS.md.
// When TEST_DATABASE_URL IS set (as CI always sets it) and the
// connection still fails, that's a real environment problem and the
// test fails loudly instead of silently skipping.
package integration

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stefan10xdd/strapi-killer/internal/auth"
	"github.com/stefan10xdd/strapi-killer/internal/authorization"
	"github.com/stefan10xdd/strapi-killer/internal/content"
	httpapi "github.com/stefan10xdd/strapi-killer/internal/http"
	"github.com/stefan10xdd/strapi-killer/internal/projects"
	"github.com/stefan10xdd/strapi-killer/internal/schema"
	"github.com/stefan10xdd/strapi-killer/internal/storage"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testDatabaseURL() (url string, explicit bool) {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v, true
	}
	return "postgres://cms:cms_dev_password@localhost:5432/cms_test", false
}

// testEnv is a fully wired backend against a real, freshly-truncated
// database. srv is the HTTP surface; authSvc is exposed directly because
// owner bootstrap has no HTTP route (CLI-only by design).
type testEnv struct {
	srv     *httptest.Server
	authSvc *auth.Service
	pool    *pgxpool.Pool
}

// newTestEnv spins up the full router against a real database, wiped
// clean first so tests don't see each other's data.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()

	dbURL, explicit := testDatabaseURL()
	pool, err := storage.NewPool(ctx, dbURL)
	if err != nil {
		if explicit {
			t.Fatalf("TEST_DATABASE_URL was set but the database is unreachable: %v", err)
		}
		t.Skipf("skipping integration test: cannot reach test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		if explicit {
			t.Fatalf("TEST_DATABASE_URL was set but the database is unreachable: %v", err)
		}
		t.Skipf("skipping integration test: cannot reach test database: %v", err)
	}

	if err := storage.Migrate(ctx, pool, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	resetDB(t, pool)

	registry := schema.NewRegistry(pool)
	if err := registry.Warm(ctx); err != nil {
		t.Fatalf("warm registry: %v", err)
	}

	authSvc := auth.NewService(pool, 24*time.Hour)
	authzSvc := authorization.NewService(pool)
	projectsSvc := projects.NewService(pool)
	contentSvc := content.NewService(pool, registry)

	router := httpapi.NewRouter(httpapi.Deps{
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		AuthSvc:     authSvc,
		AuthzSvc:    authzSvc,
		ProjectsSvc: projectsSvc,
		Registry:    registry,
		ContentSvc:  contentSvc,
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return &testEnv{srv: srv, authSvc: authSvc, pool: pool}
}

// bootstrapOwner creates the owner account directly through the service
// layer (there is deliberately no HTTP route for this) and returns a
// client already logged in as that owner.
func (e *testEnv) bootstrapOwner(t *testing.T, email, password string) *client {
	t.Helper()
	if _, err := e.authSvc.BootstrapOwner(context.Background(), email, password); err != nil {
		t.Fatalf("bootstrap owner: %v", err)
	}
	c := newClient(t, e.srv)
	resp, body := c.do(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": email, "password": password})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner login failed: %d %v", resp.StatusCode, body)
	}
	return c
}

func resetDB(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`TRUNCATE entries, content_types, project_members, projects, sessions, users CASCADE`)
	if err != nil {
		t.Fatalf("reset database: %v", err)
	}
}

// --- minimal test client: cookie jar + JSON helpers ---

type client struct {
	t       *testing.T
	baseURL string
	cookies []*http.Cookie
}

func newClient(t *testing.T, srv *httptest.Server) *client {
	return &client{t: t, baseURL: srv.URL}
}

func (c *client) do(method, path string, body any) (*http.Response, map[string]any) {
	c.t.Helper()
	var reader *strings.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("marshal request body: %v", err)
		}
		reader = strings.NewReader(string(b))
	} else {
		reader = strings.NewReader("")
	}
	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		c.t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for _, ck := range c.cookies {
		req.AddCookie(ck)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	// http.Response.Cookies() returns a non-nil empty slice (not nil)
	// when the response sets no cookies at all — checking != nil here
	// would silently wipe out an already-stored session cookie on every
	// later request that doesn't itself set one.
	if cookies := resp.Cookies(); len(cookies) > 0 {
		c.cookies = cookies
	}

	var parsed map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&parsed)
	return resp, parsed
}
