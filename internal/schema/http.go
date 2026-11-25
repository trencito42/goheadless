package schema

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	httpx "github.com/stefan10xdd/strapi-killer/internal/httpx"
)

type Handler struct {
	registry *Registry
}

func NewHandler(registry *Registry) *Handler {
	return &Handler{registry: registry}
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	projectID, err := uuid.Parse(chi.URLParam(r, "projectID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid project id")
		return
	}

	var req struct {
		APIID       string     `json:"apiId"`
		DisplayName string     `json:"displayName"`
		Kind        string     `json:"kind"`
		Schema      Definition `json:"schema"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ct, err := h.registry.Create(r.Context(), projectID, req.APIID, req.DisplayName, req.Kind, req.Schema)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, ct)
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	projectID, err := uuid.Parse(chi.URLParam(r, "projectID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.registry.ListForProject(projectID))
}
