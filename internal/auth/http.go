package auth

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	httpx "github.com/stefan10xdd/strapi-killer/internal/httpx"
)

const sessionCookieName = "cms_session"

type ctxKey string

const userCtxKey ctxKey = "auth.user"

// UserFromContext returns the authenticated user, if Middleware ran and
// found a valid session.
func UserFromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userCtxKey).(User)
	return u, ok
}

// Middleware resolves the session cookie into a User and attaches it to
// the request context. It never rejects the request by itself — handlers
// that require auth call RequireUser.
func Middleware(svc *Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(sessionCookieName)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			sessionID, err := uuid.Parse(cookie.Value)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			user, err := svc.Authenticate(r.Context(), sessionID)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			ctx := context.WithValue(r.Context(), userCtxKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireUser is a guard handlers call first; it writes 401 and returns
// false if no authenticated user is present.
func RequireUser(w http.ResponseWriter, r *http.Request) (User, bool) {
	u, ok := UserFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "authentication required")
		return User{}, false
	}
	return u, true
}

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user, sess, err := h.svc.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		httpx.WriteError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sess.ID.String(),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  sess.ExpiresAt,
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"id":      user.ID,
		"email":   user.Email,
		"isOwner": user.IsOwner,
	})
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil {
		if sessionID, err := uuid.Parse(cookie.Value); err == nil {
			_ = h.svc.Logout(r.Context(), sessionID)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	user, ok := RequireUser(w, r)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"id":      user.ID,
		"email":   user.Email,
		"isOwner": user.IsOwner,
	})
}

// CreateUser and ListUsers are owner-only; the router gates them, not
// this handler, so they assume that's already been enforced.

func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	user, err := h.svc.CreateUser(r.Context(), req.Email, req.Password)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"id":      user.ID,
		"email":   user.Email,
		"isOwner": user.IsOwner,
	})
}

func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.svc.ListUsers(r.Context())
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, users)
}
