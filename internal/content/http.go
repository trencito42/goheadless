package content

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/stefan10xdd/strapi-killer/internal/auth"
	httpx "github.com/stefan10xdd/strapi-killer/internal/httpx"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func params(r *http.Request) (projectID uuid.UUID, apiID string, err error) {
	projectID, err = uuid.Parse(chi.URLParam(r, "projectID"))
	apiID = chi.URLParam(r, "collection")
	return
}

// --- Management API (requires authentication; sees drafts) ---

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	projectID, apiID, err := params(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	var req struct {
		Locale string         `json:"locale"`
		Data   map[string]any `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	user, ok := auth.RequireUser(w, r)
	if !ok {
		return
	}
	e, err := h.svc.Create(r.Context(), projectID, apiID, req.Locale, user.ID, req.Data)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, e)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	projectID, apiID, err := params(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	documentID, err := uuid.Parse(chi.URLParam(r, "documentID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid document id")
		return
	}
	var req struct {
		Locale          string         `json:"locale"`
		ExpectedVersion int            `json:"expectedVersion"`
		Data            map[string]any `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	e, err := h.svc.Update(r.Context(), projectID, apiID, documentID, req.Locale, req.ExpectedVersion, req.Data)
	if err != nil {
		httpx.WriteError(w, http.StatusConflict, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, e)
}

func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	projectID, apiID, err := params(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	documentID, err := uuid.Parse(chi.URLParam(r, "documentID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid document id")
		return
	}
	locale := r.URL.Query().Get("locale")
	e, err := h.svc.Publish(r.Context(), projectID, apiID, documentID, locale)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, e)
}

func (h *Handler) Unpublish(w http.ResponseWriter, r *http.Request) {
	projectID, apiID, err := params(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	documentID, err := uuid.Parse(chi.URLParam(r, "documentID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid document id")
		return
	}
	locale := r.URL.Query().Get("locale")
	if err := h.svc.Unpublish(r.Context(), projectID, apiID, documentID, locale); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListDrafts(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, StatusDraft)
}

// --- Delivery API (public, explicitly authorized; published only) ---

func (h *Handler) ListPublished(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, StatusPublished)
}

func (h *Handler) GetPublished(w http.ResponseWriter, r *http.Request) {
	projectID, apiID, err := params(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	documentID, err := uuid.Parse(chi.URLParam(r, "documentID"))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid document id")
		return
	}
	locale := r.URL.Query().Get("locale")
	e, err := h.svc.Get(r.Context(), projectID, apiID, documentID, StatusPublished, locale)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, e)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, status string) {
	projectID, apiID, err := params(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	locale := r.URL.Query().Get("locale")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	entries, err := h.svc.List(r.Context(), projectID, apiID, status, locale, limit, offset)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": entries})
}
