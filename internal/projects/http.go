package projects

import (
	"encoding/json"
	"net/http"

	"github.com/stefan10xdd/strapi-killer/internal/auth"
	"github.com/stefan10xdd/strapi-killer/internal/authorization"
	httpx "github.com/stefan10xdd/strapi-killer/internal/httpx"
)

type Handler struct {
	svc   *Service
	authz *authorization.Service
}

func NewHandler(svc *Service, authz *authorization.Service) *Handler {
	return &Handler{svc: svc, authz: authz}
}

// Create is owner-only (gated in the router): there is no self-service
// project creation, matching "no public registration" for the platform
// as a whole.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	p, err := h.svc.Create(r.Context(), req.Name, req.Slug)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, p)
}

// List returns every project for a global owner, or only the projects a
// non-owner user is a member of.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.RequireUser(w, r)
	if !ok {
		return
	}

	if user.IsOwner {
		list, err := h.svc.List(r.Context())
		if err != nil {
			httpx.WriteError(w, http.StatusInternalServerError, "failed to list projects")
			return
		}
		httpx.WriteJSON(w, http.StatusOK, list)
		return
	}

	ids, err := h.authz.ListProjectIDsForUser(r.Context(), user.ID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to list projects")
		return
	}
	list, err := h.svc.ListByIDs(r.Context(), ids)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to list projects")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}
