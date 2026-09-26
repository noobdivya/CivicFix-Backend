// Command seed-demo creates demo staff accounts for local testing:
// one department officer and two field workers for every department.
//
//	go run ./cmd/seed-demo              # password: Demo@1234
//	go run ./cmd/seed-demo -password X  # choose the password
//
// Do not run this against a production database.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"github.com/civicfix/backend/internal/auth"
	"github.com/civicfix/backend/internal/config"
	"github.com/civicfix/backend/internal/database"
)

// Fictional names for demo accounts, per department slug.
var demoStaff = map[string][3]string{
	"roads":      {"Anil Mehta", "Ravi Kumar", "Sunil Yadav"},
	"electrical": {"Neha Gupta", "Arjun Singh", "Manoj Das"},
	"water":      {"Pooja Iyer", "Vikram Rao", "Deepak Joshi"},
	"sanitation": {"Kavita Nair", "Rahul Verma", "Suresh Pal"},
	"traffic":    {"Imran Khan", "Amit Chauhan", "Rohit Saini"},
	"general":    {"Meera Kapoor", "Karan Malhotra", "Sanjay Mishra"},
}

func main() {
	password := flag.String("password", "Demo@1234", "password for all demo accounts")
	flag.Parse()

	cfg := config.Load()
	ctx := context.Background()
	pool, err := database.Connect(ctx, cfg.DatabaseURL, cfg.Timezone)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	hash, err := auth.HashPassword(*password)
	if err != nil {
		log.Fatal(err)
	}

	rows, err := pool.Query(ctx, `SELECT id, slug, name FROM departments ORDER BY name`)
	if err != nil {
		log.Fatal(err)
	}
	type dept struct {
		id         int
		slug, name string
	}
	var depts []dept
	for rows.Next() {
		var d dept
		if err := rows.Scan(&d.id, &d.slug, &d.name); err != nil {
			log.Fatal(err)
		}
		depts = append(depts, d)
	}
	rows.Close()

	fmt.Printf("\nDemo accounts (password: %s)\n\n", *password)
	fmt.Printf("%-32s %-11s %-16s %s\n", "EMAIL", "ROLE", "NAME", "DEPARTMENT")
	for _, d := range depts {
		names, ok := demoStaff[d.slug]
		if !ok {
			continue
		}
		accounts := []struct{ email, role, name string }{
			{d.slug + ".officer@civicfix.local", auth.RoleDepartment, names[0]},
			{d.slug + ".worker1@civicfix.local", auth.RoleWorker, names[1]},
			{d.slug + ".worker2@civicfix.local", auth.RoleWorker, names[2]},
		}
		for _, a := range accounts {
			if _, err := pool.Exec(ctx, `
				INSERT INTO users (name, email, role, department_id, password_hash)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT ((lower(email))) DO NOTHING`,
				a.name, a.email, a.role, d.id, hash); err != nil {
				log.Fatalf("create %s: %v", a.email, err)
			}
			fmt.Printf("%-32s %-11s %-16s %s\n", a.email, a.role, a.name, d.name)
		}
	}
	fmt.Println("\nExisting accounts with these emails were left unchanged.")
}
