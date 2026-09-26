package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/civicfix/backend/internal/auth"
	"github.com/civicfix/backend/internal/config"
	"github.com/civicfix/backend/internal/geo"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	DB       *pgxpool.Pool
	Config   config.Config
	Geocoder *geo.Geocoder
	Auth     *auth.Service
}

// apiError is returned by handler helpers for expected failures
// (validation, permissions, invalid state) that map to an HTTP status.
type apiError struct {
	status int
	msg    string
	fields map[string]string
}

func (e *apiError) Error() string { return e.msg }

func badRequest(msg string) error { return &apiError{status: http.StatusBadRequest, msg: msg} }
func notFound(msg string) error   { return &apiError{status: http.StatusNotFound, msg: msg} }
func forbidden(msg string) error  { return &apiError{status: http.StatusForbidden, msg: msg} }
func conflict(msg string) error   { return &apiError{status: http.StatusConflict, msg: msg} }
func invalid(fields map[string]string) error {
	return &apiError{status: http.StatusUnprocessableEntity, msg: "please fix the highlighted fields", fields: fields}
}

// writeErr writes an apiError as JSON, or logs anything else as a 500.
func writeErr(w http.ResponseWriter, err error, context string) {
	var ae *apiError
	if errors.As(err, &ae) {
		if ae.fields != nil {
			writeJSON(w, ae.status, map[string]any{"error": ae.msg, "fields": ae.fields})
		} else {
			writeError(w, ae.status, ae.msg)
		}
		return
	}
	log.Printf("%s: %v", context, err)
	writeError(w, http.StatusInternalServerError, "something went wrong, please try again")
}

// decodeJSON reads a small JSON body into v.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return badRequest("invalid request body")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// Health reports whether the API and the database are up.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	dbStatus := "up"
	status := http.StatusOK
	if err := h.DB.Ping(ctx); err != nil {
		dbStatus = "down"
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]string{"status": "ok", "database": dbStatus})
}
