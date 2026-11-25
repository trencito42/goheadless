// Package httpx provides small HTTP response helpers shared by every
// feature package. It must not depend on any feature package (auth,
// content, ...) to avoid import cycles with internal/http, which wires
// those packages' handlers into the router.
package httpx

import (
	"encoding/json"
	"net/http"
)

func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// WriteError writes an RFC 9457-style problem+json body.
func WriteError(w http.ResponseWriter, status int, detail string) {
	WriteJSON(w, status, map[string]any{
		"status": status,
		"title":  http.StatusText(status),
		"detail": detail,
	})
}
