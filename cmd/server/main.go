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

	"github.com/civicfix/backend/internal/auth"
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

	if cfg.AadhaarHashKey == config.DevAadhaarHashKey {
		log.Println("WARNING: AADHAAR_HASH_KEY is not set; using an insecure development key")
	}
	if err := os.MkdirAll(cfg.UploadDir, 0o755); err != nil {
		log.Fatalf("upload dir: %v", err)
	}

	authSvc := &auth.Service{DB: pool, SecureCookie: cfg.CookieSecure}
	if err := authSvc.EnsureAdmin(ctx, cfg.AdminEmail, cfg.AdminPassword); err != nil {
		log.Fatalf("admin account: %v", err)
	}

	h := &handlers.Handler{
		DB: pool, Config: cfg, Geocoder: geo.NewGeocoder(cfg.NominatimUserAgent),
		Auth: authSvc,
	}
	staff := auth.Require()
	managers := auth.Require(auth.RoleAdmin, auth.RoleDepartment)
	admin := auth.Require(auth.RoleAdmin)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", h.Health)
	mux.HandleFunc("GET /api/dashboard", h.Dashboard)
	mux.HandleFunc("GET /api/map/default", h.MapDefault)
	mux.HandleFunc("GET /api/map/issues", h.MapIssues)
	mux.HandleFunc("GET /api/geo/reverse", h.ReverseGeocode)
	mux.HandleFunc("GET /api/geo/search", middleware.RateLimit(60, 10*time.Minute, h.GeoSearch))
	mux.HandleFunc("GET /api/officials/messages", h.OfficialMessages)
	mux.HandleFunc("POST /api/contact", middleware.RateLimit(5, 10*time.Minute, h.Contact))
	mux.HandleFunc("GET /api/categories", h.Categories)
	mux.HandleFunc("POST /api/issues", middleware.RateLimit(10, 10*time.Minute, h.CreateIssue))
	mux.Handle("GET /uploads/", http.StripPrefix("/uploads/", middleware.StaticFiles(cfg.UploadDir)))
	mux.HandleFunc("GET /api/track", middleware.RateLimit(30, 10*time.Minute, h.Track))

	// Staff authentication
	mux.HandleFunc("POST /api/auth/login", middleware.RateLimit(10, 10*time.Minute, h.Login))
	mux.HandleFunc("POST /api/auth/logout", h.Logout)
	mux.HandleFunc("GET /api/auth/me", h.Me)

	// Staff: issues (scoped by role), workers, departments
	mux.HandleFunc("GET /api/staff/summary", staff(h.StaffSummary))
	mux.HandleFunc("GET /api/staff/issues", staff(h.StaffIssues))
	mux.HandleFunc("GET /api/staff/issues/{id}", staff(h.StaffIssue))
	mux.HandleFunc("POST /api/staff/issues/{id}/review", managers(h.ReviewIssue))
	mux.HandleFunc("POST /api/staff/issues/{id}/assign", managers(h.AssignIssue))
	mux.HandleFunc("POST /api/staff/issues/{id}/reject", managers(h.RejectIssue))
	mux.HandleFunc("POST /api/staff/issues/{id}/reopen", managers(h.ReopenIssue))
	mux.HandleFunc("POST /api/staff/issues/{id}/priority", managers(h.SetPriority))
	mux.HandleFunc("POST /api/staff/issues/{id}/transfer", managers(h.TransferIssue))
	mux.HandleFunc("POST /api/staff/issues/{id}/start", staff(h.StartIssue))
	mux.HandleFunc("POST /api/staff/issues/{id}/notes", staff(h.AddNote))
	mux.HandleFunc("POST /api/staff/issues/{id}/resolve", staff(h.ResolveIssue))
	mux.HandleFunc("GET /api/staff/workers", managers(h.Workers))
	mux.HandleFunc("GET /api/staff/departments", staff(h.Departments))

	// Notifications (plain REST: read when a page loads or the bell is opened)
	mux.HandleFunc("GET /api/notifications", staff(h.Notifications))
	mux.HandleFunc("POST /api/notifications/read", staff(h.MarkNotificationsRead))

	// Admin
	mux.HandleFunc("GET /api/admin/users", admin(h.AdminUsers))
	mux.HandleFunc("POST /api/admin/users", admin(h.AdminCreateUser))
	mux.HandleFunc("PATCH /api/admin/users/{id}", admin(h.AdminUpdateUser))
	mux.HandleFunc("GET /api/admin/analytics", admin(h.AdminAnalytics))

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           middleware.Logger(middleware.CORS(cfg.CORSAllowedOrigins)(authSvc.Middleware(mux))),
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
