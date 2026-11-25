package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/stefan10xdd/strapi-killer/internal/auth"
	"github.com/stefan10xdd/strapi-killer/internal/content"
	"github.com/stefan10xdd/strapi-killer/internal/httpx"
	"github.com/stefan10xdd/strapi-killer/internal/projects"
	"github.com/stefan10xdd/strapi-killer/internal/schema"
)

type Deps struct {
	Logger      *slog.Logger
	AuthSvc     *auth.Service
	ProjectsSvc *projects.Service
	Registry    *schema.Registry
	ContentSvc  *content.Service
	CORSOrigins []string
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(requestLogger(d.Logger))
	r.Use(chimw.Recoverer)
	r.Use(cors(d.CORSOrigins))
	r.Use(auth.Middleware(d.AuthSvc))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/ready", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	authHandler := auth.NewHandler(d.AuthSvc)
	r.Post("/api/v1/auth/login", authHandler.Login)
	r.Post("/api/v1/auth/logout", authHandler.Logout)
	r.Get("/api/v1/auth/me", authHandler.Me)

	projectsHandler := projects.NewHandler(d.ProjectsSvc)
	schemaHandler := schema.NewHandler(d.Registry)
	contentHandler := content.NewHandler(d.ContentSvc)

	// Management API: authenticated, operates on drafts, can publish.
	r.Route("/api/v1/manage/projects", func(r chi.Router) {
		r.Use(requireAuth)
		r.Post("/", projectsHandler.Create)
		r.Get("/", projectsHandler.List)

		r.Route("/{projectID}/content-types", func(r chi.Router) {
			r.Post("/", schemaHandler.Create)
			r.Get("/", schemaHandler.List)
		})

		r.Route("/{projectID}/content/{collection}", func(r chi.Router) {
			r.Post("/", contentHandler.Create)
			r.Get("/", contentHandler.ListDrafts)
			r.Route("/{documentID}", func(r chi.Router) {
				r.Patch("/", contentHandler.Update)
				r.Post("/publish", contentHandler.Publish)
				r.Post("/unpublish", contentHandler.Unpublish)
			})
		})
	})

	// Delivery API: public (anonymous access is opt-in per the roadmap;
	// for now every route in this tree still requires a session so we
	// never accidentally expose content before access control lands).
	r.Route("/api/v1/projects/{projectID}/content/{collection}", func(r chi.Router) {
		r.Use(requireAuth)
		r.Get("/", contentHandler.ListPublished)
		r.Get("/{documentID}", contentHandler.GetPublished)
	})

	return r
}

func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth.UserFromContext(r.Context()); !ok {
			httpx.WriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			logger.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", chimw.GetReqID(r.Context()),
			)
		})
	}
}

// cors applies an explicit allowlist. An empty list disables cross-origin
// requests entirely rather than falling back to a permissive default.
func cors(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && allowed[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
				w.Header().Set("Vary", "Origin")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
