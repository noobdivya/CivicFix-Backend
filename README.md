# CivicFix — Backend

Go REST API for **CivicFix**, a civic issue reporting and management platform connecting citizens, municipal departments, field workers and administrators.

Frontend: [CivicFix-frontend](https://github.com/noobdivya/CivicFix-frontend)

**Current status:** Feature 1 — Landing page APIs.

## Tech stack
- Go (standard `net/http` router)
- PostgreSQL 16 via [pgx](https://github.com/jackc/pgx)
- Docker Compose for the local database
- OpenStreetMap Nominatim for reverse geocoding (no API key)

## Getting started

Prerequisites: [Go](https://go.dev/) 1.22+ and [Docker Desktop](https://www.docker.com/products/docker-desktop/).

```bash
cp .env.example .env          # Windows PowerShell: copy .env.example .env
docker compose up -d          # start PostgreSQL
go run ./cmd/server           # API on http://localhost:8080
```

Database migrations in `internal/database/migrations/` run automatically on startup.

## Configuration (`.env`)

| Variable | Default | Purpose |
|---|---|---|
| `PORT` | `8080` | API port |
| `DATABASE_URL` | `postgres://civicfix:civicfix@localhost:5432/civicfix?sslmode=disable` | PostgreSQL connection |
| `APP_TIMEZONE` | `Asia/Kolkata` | Timezone for per-day statistics |
| `CORS_ALLOWED_ORIGINS` | `http://localhost:3000,http://127.0.0.1:3000` | Frontend origins allowed to call the API |
| `DEFAULT_CITY_NAME` / `_LAT` / `_LNG` | New Delhi | Map fallback location |
| `NOMINATIM_USER_AGENT` | `CivicFix/0.1 (local development)` | Identifies the app to Nominatim |

## API

| Method | Endpoint | Description |
|---|---|---|
| GET | `/api/health` | API and database status |
| GET | `/api/dashboard` | Issue counts, categories, 14-day trend, hotspot areas, recent reports, activity |
| GET | `/api/map/issues` | Issues with coordinates for the map |
| GET | `/api/map/default` | Default map city |
| GET | `/api/geo/reverse?lat=&lng=` | Area / city name for coordinates |
| GET | `/api/officials/messages` | Messages from officials |
| POST | `/api/contact` | Submit the contact form (`name`, `email`, `subject`, `message`) |

## Project structure

```
cmd/server/main.go          entry point & routes
internal/config/            environment configuration
internal/database/          connection + migration runner, SQL migrations
internal/handlers/          HTTP handlers
internal/middleware/        CORS & request logging
internal/geo/               Nominatim reverse geocoder (cached, rate-limited)
docker-compose.yml          local PostgreSQL
```
