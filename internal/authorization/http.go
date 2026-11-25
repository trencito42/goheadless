package authorization

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/stefan10xdd/strapi-killer/internal/auth"
	httpx "github.com/stefan10xdd/strapi-killer/internal/httpx"
)

type ctxKey string

const roleCtxKey ctxKey = "authorization.role"

func RoleFromContext(ctx context.Context) (Role, bool) {
	r, ok := ctx.Value(roleCtxKey).(Role)
	return r, ok
}

// RequireProjectRole reads {projectID} from the route, resolves the
// caller's role for that project, and rejects the request with 403 if
// it's below min. A user who isn't a member at all gets the same 403 a
// member with too-low a role gets — project membership itself (not just
// the requested action) is not something a non-member should be able to
// distinguish from "action not allowed".
func RequireProjectRole(svc *Service, min Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := auth.UserFromContext(r.Context())
			if !ok {
				httpx.WriteError(w, http.StatusUnauthorized, "authentication required")
				return
			}
			projectID, err := uuid.Parse(chi.URLParam(r, "projectID"))
			if err != nil {
				httpx.WriteError(w, http.StatusBadRequest, "invalid project id")
				return
			}
			role, err := svc.RoleInProject(r.Context(), user.ID, user.IsOwner, projectID)
			if err != nil {
				httpx.WriteError(w, http.StatusForbidden, "not authorized for this project")
				return
			}
			if !role.AtLeast(min) {
				httpx.WriteError(w, http.StatusForbidden, "insufficient role for this action")
				return
			}
			ctx := context.WithValue(r.Context(), roleCtxKey, role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) ListMembers(w http.ResponseWriter, r *http.Request) {
	projectID, err := uuid.Parse(chi.URLParam(r, "projectID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	members, err := h.svc.ListMembers(r.Context(), projectID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to list members")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, members)
}

func (h *Handler) AddMember(w http.ResponseWriter, r *http.Request) {
	projectID, err := uuid.Parse(chi.URLParam(r, "projectID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	var req struct {
		UserID string `json:"userId"`
		Role   Role   `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if err := h.svc.AddMember(r.Context(), projectID, userID, req.Role); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	projectID, err := uuid.Parse(chi.URLParam(r, "projectID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	userID, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if err := h.svc.RemoveMember(r.Context(), projectID, userID); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to remove member")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
