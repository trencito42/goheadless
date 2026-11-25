package content

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/stefan10xdd/strapi-killer/internal/auth"
	"github.com/stefan10xdd/strapi-killer/internal/authorization"
	httpx "github.com/stefan10xdd/strapi-killer/internal/httpx"
)

func parseInclude(r *http.Request) []string {
	raw := r.URL.Query().Get("include")
	if raw == "" {
		return nil
	}
	var out []string
	for _, f := range strings.Split(raw, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

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
		if errors.Is(err, ErrRelationConflict) {
			httpx.WriteError(w, http.StatusConflict, err.Error())
			return
		}
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
	user, ok := auth.RequireUser(w, r)
	if !ok {
		return
	}
	role, _ := authorization.RoleFromContext(r.Context())
	requireOwnership := !role.AtLeast(authorization.RoleEditor)

	e, err := h.svc.Update(r.Context(), projectID, apiID, documentID, req.Locale, req.ExpectedVersion, req.Data, user.ID, requireOwnership)
	if err != nil {
		if errors.Is(err, ErrNotOwner) {
			httpx.WriteError(w, http.StatusForbidden, err.Error())
			return
		}
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
		if errors.Is(err, ErrRelationConflict) {
			httpx.WriteError(w, http.StatusConflict, err.Error())
			return
		}
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
	h.list(w, r, StatusDraft, false)
}

func (h *Handler) GetDraft(w http.ResponseWriter, r *http.Request) {
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
	include := parseInclude(r)
	e, err := h.svc.Get(r.Context(), projectID, apiID, documentID, StatusDraft, locale, include, false)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, e)
}

// --- Delivery API (public, explicitly authorized; published only) ---

func (h *Handler) ListPublished(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, StatusPublished, true)
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
	include := parseInclude(r)
	e, err := h.svc.Get(r.Context(), projectID, apiID, documentID, StatusPublished, locale, include, true)
	if err != nil {
		httpx.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, e)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, status string, redactPrivateFields bool) {
	projectID, apiID, err := params(r)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	locale := r.URL.Query().Get("locale")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	include := parseInclude(r)

	entries, err := h.svc.List(r.Context(), projectID, apiID, status, locale, limit, offset, include, redactPrivateFields)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": entries})
}
