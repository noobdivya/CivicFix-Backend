package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/civicfix/backend/internal/config"
	"github.com/civicfix/backend/internal/geo"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	DB       *pgxpool.Pool
	Config   config.Config
	Geocoder *geo.Geocoder
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
