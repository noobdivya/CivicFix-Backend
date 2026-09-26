package handlers

import (
	"net/http"
	"strings"

	"github.com/civicfix/backend/internal/auth"
)

// Login checks staff credentials and starts a session (HttpOnly cookie).
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeErr(w, err, "login")
		return
	}
	if strings.TrimSpace(body.Email) == "" || body.Password == "" {
		writeErr(w, invalid(map[string]string{"email": "Enter your email and password."}), "login")
		return
	}
	u, err := h.Auth.Authenticate(r.Context(), body.Email, body.Password)
	if err != nil {
		writeErr(w, err, "authenticate")
		return
	}
	if u == nil {
		writeError(w, http.StatusUnauthorized, "incorrect email or password")
		return
	}
	if err := h.Auth.StartSession(r.Context(), w, r, u.ID); err != nil {
		writeErr(w, err, "start session")
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	h.Auth.EndSession(r.Context(), w, r)
	w.WriteHeader(http.StatusNoContent)
}

// Me returns the logged-in user, or null when nobody is logged in.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, auth.FromContext(r.Context()))
}
