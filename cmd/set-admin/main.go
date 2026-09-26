// Command set-admin applies ADMIN_EMAIL and ADMIN_PASSWORD from .env to the
// admin account: it updates the first admin (or creates one if none exists)
// and signs that account out everywhere.
//
//	go run ./cmd/set-admin
package main

import (
	"context"
	"errors"
	"log"
	"net/mail"

	"github.com/civicfix/backend/internal/auth"
	"github.com/civicfix/backend/internal/config"
	"github.com/civicfix/backend/internal/database"
	"github.com/jackc/pgx/v5"
)

func main() {
	cfg := config.Load()
	if a, err := mail.ParseAddress(cfg.AdminEmail); err != nil || a.Address != cfg.AdminEmail {
		log.Fatalf("ADMIN_EMAIL %q is not a valid email address", cfg.AdminEmail)
	}
	if len(cfg.AdminPassword) < 8 || len(cfg.AdminPassword) > 72 {
		log.Fatal("ADMIN_PASSWORD must be 8–72 characters")
	}

	ctx := context.Background()
	pool, err := database.Connect(ctx, cfg.DatabaseURL, cfg.Timezone)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	hash, err := auth.HashPassword(cfg.AdminPassword)
	if err != nil {
		log.Fatal(err)
	}

	var id int64
	err = pool.QueryRow(ctx, `SELECT id FROM users WHERE role = 'admin' ORDER BY id LIMIT 1`).Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if err := pool.QueryRow(ctx,
			`INSERT INTO users (name, email, password_hash, role) VALUES ('Administrator', $1, $2, 'admin') RETURNING id`,
			cfg.AdminEmail, hash).Scan(&id); err != nil {
			log.Fatalf("create admin: %v", err)
		}
		log.Printf("created admin account %s", cfg.AdminEmail)
	case err != nil:
		log.Fatal(err)
	default:
		if _, err := pool.Exec(ctx,
			`UPDATE users SET email = $2, password_hash = $3, active = TRUE WHERE id = $1`,
			id, cfg.AdminEmail, hash); err != nil {
			log.Fatalf("update admin (is the email used by another account?): %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, id); err != nil {
			log.Fatal(err)
		}
		log.Printf("admin account #%d now signs in as %s (existing sessions signed out)", id, cfg.AdminEmail)
	}
}
