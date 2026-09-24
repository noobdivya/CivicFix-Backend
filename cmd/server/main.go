package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/civicfix/backend/internal/config"
	"github.com/civicfix/backend/internal/database"
	"github.com/civicfix/backend/internal/geo"
	"github.com/civicfix/backend/internal/handlers"
	"github.com/civicfix/backend/internal/middleware"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()

	pool, err := database.Connect(ctx, cfg.DatabaseURL, cfg.Timezone)
	if err != nil {
		log.Fatalf("database: %v (is Postgres running? try `docker compose up -d`)", err)
	}
	defer pool.Close()

	if err := database.Migrate(ctx, pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	h := &handlers.Handler{DB: pool, Config: cfg, Geocoder: geo.NewGeocoder(cfg.NominatimUserAgent)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", h.Health)
	mux.HandleFunc("GET /api/dashboard", h.Dashboard)
	mux.HandleFunc("GET /api/map/default", h.MapDefault)
	mux.HandleFunc("GET /api/map/issues", h.MapIssues)
	mux.HandleFunc("GET /api/geo/reverse", h.ReverseGeocode)
	mux.HandleFunc("GET /api/officials/messages", h.OfficialMessages)
	mux.HandleFunc("POST /api/contact", h.Contact)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           middleware.Logger(middleware.CORS(cfg.CORSAllowedOrigins)(mux)),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("CivicFix API listening on http://localhost:%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
