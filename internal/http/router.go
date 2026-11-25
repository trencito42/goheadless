package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/stefan10xdd/strapi-killer/internal/auth"
	"github.com/stefan10xdd/strapi-killer/internal/authorization"
	"github.com/stefan10xdd/strapi-killer/internal/content"
	"github.com/stefan10xdd/strapi-killer/internal/httpx"
	"github.com/stefan10xdd/strapi-killer/internal/projects"
	"github.com/stefan10xdd/strapi-killer/internal/schema"
)

type Deps struct {
	Logger      *slog.Logger
	AuthSvc     *auth.Service
	AuthzSvc    *authorization.Service
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

	projectsHandler := projects.NewHandler(d.ProjectsSvc, d.AuthzSvc)
	schemaHandler := schema.NewHandler(d.Registry)
	contentHandler := content.NewHandler(d.ContentSvc)
	membersHandler := authorization.NewHandler(d.AuthzSvc)

	requireEditor := authorization.RequireProjectRole(d.AuthzSvc, authorization.RoleEditor)
	requireAuthor := authorization.RequireProjectRole(d.AuthzSvc, authorization.RoleAuthor)
	requireViewer := authorization.RequireProjectRole(d.AuthzSvc, authorization.RoleViewer)
	requireAdministrator := authorization.RequireProjectRole(d.AuthzSvc, authorization.RoleAdministrator)

	// Owner-only: no public registration, no self-service project
	// creation or user invitation. This is the whole "invite a
	// collaborator" flow for now (see PROGRESS.md).
	r.Route("/api/v1/manage/users", func(r chi.Router) {
		r.Use(requireAuth, requireOwner)
		r.Post("/", authHandler.CreateUser)
		r.Get("/", authHandler.ListUsers)
	})

	// Management API: authenticated, operates on drafts, can publish.
	// Every project-scoped route is additionally gated by role; a
	// non-member gets the same 403 a too-low-role member gets.
	r.Route("/api/v1/manage/projects", func(r chi.Router) {
		r.Use(requireAuth)

		r.With(requireOwner).Post("/", projectsHandler.Create)
		r.Get("/", projectsHandler.List) // self-filters by membership

		r.Route("/{projectID}/members", func(r chi.Router) {
			r.Use(requireAdministrator)
			r.Get("/", membersHandler.ListMembers)
			r.Post("/", membersHandler.AddMember)
			r.Delete("/{userID}", membersHandler.RemoveMember)
		})

		r.Route("/{projectID}/content-types", func(r chi.Router) {
			r.With(requireAdministrator).Post("/", schemaHandler.Create)
			r.With(requireViewer).Get("/", schemaHandler.List)
		})

		r.Route("/{projectID}/content/{collection}", func(r chi.Router) {
			r.With(requireAuthor).Post("/", contentHandler.Create)
			r.With(requireViewer).Get("/", contentHandler.ListDrafts)
			r.Route("/{documentID}", func(r chi.Router) {
				r.With(requireViewer).Get("/", contentHandler.GetDraft)
				r.With(requireAuthor).Patch("/", contentHandler.Update)
				r.With(requireEditor).Post("/publish", contentHandler.Publish)
				r.With(requireEditor).Post("/unpublish", contentHandler.Unpublish)
				r.With(requireEditor).Delete("/", contentHandler.Delete)
			})
		})
	})

	// Delivery API: public (anonymous access is opt-in per the roadmap;
	// for now every route in this tree still requires at least viewer
	// membership so we never accidentally expose content before
	// anonymous access control is actually designed).
	r.Route("/api/v1/projects/{projectID}/content/{collection}", func(r chi.Router) {
		r.Use(requireAuth, requireViewer)
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

// requireOwner gates routes with no notion of project membership at all
// (creating projects, creating/listing users). Must run after requireAuth.
func requireOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFromContext(r.Context())
		if !ok || !user.IsOwner {
			httpx.WriteError(w, http.StatusForbidden, "owner access required")
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
