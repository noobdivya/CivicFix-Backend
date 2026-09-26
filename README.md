# CivicFix — Backend

Go REST API for **CivicFix**, a civic issue platform that connects citizens, municipal departments, field workers and administrators across the full lifecycle of an issue: report → review & prioritise → assign → fix with proof → track.

Frontend: [CivicFix-frontend](https://github.com/noobdivya/CivicFix-frontend)

## Tech stack
- Go (standard `net/http` router), PostgreSQL 16 via [pgx](https://github.com/jackc/pgx)
- bcrypt passwords, HttpOnly cookie sessions, role-based access
- Plain REST only (no SSE/WebSockets): clients fetch the latest data on page load or when the user refreshes
- OpenStreetMap Nominatim for reverse geocoding (no API key)
- Docker Compose for the local database

## Getting started

Prerequisites: [Go](https://go.dev/) 1.22+ and [Docker Desktop](https://www.docker.com/products/docker-desktop/).

```bash
cp .env.example .env          # Windows PowerShell: copy .env.example .env
# edit .env: set ADMIN_PASSWORD
docker compose up -d          # start PostgreSQL
go run ./cmd/server           # API on http://localhost:8080
go run ./cmd/set-admin        # apply ADMIN_EMAIL / ADMIN_PASSWORD from .env to the admin account
go run ./cmd/seed-demo        # optional, local testing only: demo officers & workers
go test ./...                 # run tests
```

Migrations in `internal/database/migrations/` run automatically on startup. The first admin account is created from `ADMIN_EMAIL` / `ADMIN_PASSWORD`.

## Roles & workflow

| Role | Can do |
|---|---|
| **Citizen** (no account) | Report an issue (photo, location, category); track it with tracking ID + mobile number |
| **Department officer** | See their department's issues; review & set priority (sets a deadline); assign/reassign field workers; reject; reopen; transfer to another department; add notes |
| **Field worker** | See assigned tasks; start work; add progress notes; resolve with a completion photo |
| **Admin** | Everything above for all departments; staff account management; city-wide analytics |

```
reported ──review/assign──▶ assigned ──start──▶ in_progress ──resolve (photo)──▶ resolved
   └──────────── reject ──────────┴──────────────────┘            reopen ◀───────┘
```

Priority deadlines: critical 24 h · high 3 days · medium 7 days · low 14 days.
Each category belongs to a department, so new reports go straight to the right officers.

## Configuration (`.env`)

| Variable | Default | Purpose |
|---|---|---|
| `PORT` | `8080` | API port |
| `DATABASE_URL` | `postgres://civicfix:civicfix@localhost:5432/civicfix?sslmode=disable` | PostgreSQL |
| `APP_TIMEZONE` | `Asia/Kolkata` | Timezone for per-day statistics |
| `CORS_ALLOWED_ORIGINS` | `http://localhost:3000,http://127.0.0.1:3000` | Frontend origins |
| `DEFAULT_CITY_NAME` / `_LAT` / `_LNG` | New Delhi | Map fallback location |
| `NOMINATIM_USER_AGENT` | `CivicFix/0.1 (local development)` | Identifies the app to Nominatim |
| `SEARCH_COUNTRY_CODES` | `in` | Limit area search to these countries (empty = worldwide) |
| `UPLOAD_DIR` | `uploads` | Where photos are stored |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` | `admin@civicfix.local` / *(random, printed once)* | First admin account |
| `COOKIE_SECURE` | `false` | Set `true` behind HTTPS |
| `TRUSTED_PROXY_HOPS` | `0` | Reverse proxies in front of the API that append to `X-Forwarded-For`, so rate limits see the real client IP (`2` for Vercel → Render) |

## API

**Public**

| Method | Endpoint | Description |
|---|---|---|
| GET | `/api/health` | API and database status |
| GET | `/api/dashboard` | Counts, categories, 14-day trend, hotspots, recent reports, activity |
| GET | `/api/map/issues` · `/api/map/default` | Map markers · default city |
| GET | `/api/geo/reverse?lat=&lng=` | Area / city for coordinates |
| GET | `/api/geo/search?q=` | Find a place by name (for choosing an area) |
| GET | `/api/categories` | Issue categories |
| POST | `/api/issues` | Report an issue (multipart: category, name, phone, description, 1–2 × photo, lat, lng, address, area, consent) |
| GET | `/api/track?code=&phone=` | Citizen tracking: status, public timeline, photos |
| GET | `/api/officials/messages` | Messages from officials |
| POST | `/api/contact` | Contact form |
| GET | `/uploads/...` | Photos |

**Staff** (session cookie; scoped to the user's department or tasks)

| Method | Endpoint | Who |
|---|---|---|
| POST | `/api/auth/login` · `/api/auth/logout` · GET `/api/auth/me` | All |
| GET | `/api/staff/summary` · `/api/staff/issues` · `/api/staff/issues/{id}` | All staff |
| POST | `/api/staff/issues/{id}/review` · `/assign` · `/reject` · `/reopen` · `/priority` · `/transfer` | Officers, admins |
| POST | `/api/staff/issues/{id}/start` · `/notes` · `/resolve` (multipart photo) | Workers (and managers) |
| GET | `/api/staff/workers` · `/api/staff/departments` | Officers, admins / all staff |
| GET | `/api/notifications` · POST `/api/notifications/read` | All staff |
| GET/POST | `/api/admin/users` · PATCH `/api/admin/users/{id}` | Admins |
| GET | `/api/admin/analytics` | Admins |

`/api/dashboard` accepts optional filters (the map endpoint is never filtered):
`lat`, `lng` (area centre) + `radiusKm` (default 15, max 100) · `status=reported,progress,resolved` · `category=<slug>` · `sinceDays=<1–365>`.

## Deployment (Render + Neon)

The API runs on [Render](https://render.com) as a Docker web service ([Dockerfile](Dockerfile), [render.yaml](render.yaml)). The PostgreSQL database is hosted on [Neon](https://neon.tech). The [frontend](https://github.com/noobdivya/CivicFix-frontend) runs on Vercel and forwards `/api` and `/uploads` to this service, so the browser only talks to one domain and the session cookie works.

1. **Neon:** create a project (region close to the Render region, e.g. AWS Singapore). Copy the **direct** connection string (turn *Connection pooling* off). It looks like `postgresql://user:pass@ep-xxx.ap-southeast-1.aws.neon.tech/neondb?sslmode=require`. Tables are created automatically on first start.
2. **Render:** *New → Blueprint*, then pick this repository. Render reads `render.yaml` and asks for:
   - `DATABASE_URL`: the Neon connection string
   - `CORS_ALLOWED_ORIGINS`: your Vercel URL, e.g. `https://civicfix.vercel.app`
   - `ADMIN_EMAIL` / `ADMIN_PASSWORD`: the first admin account
3. Check `https://<your-service>.onrender.com/api/health` returns `{"database":"up","status":"ok"}`.
4. Set `BACKEND_URL` on the Vercel project to the Render URL (see the frontend README).

**Free-tier limits:**
- Render's free plan sleeps after 15 minutes idle, so the first request after that takes about a minute.
- Uploaded photos are stored on the service's local disk, which is **wiped on every deploy or restart**. To keep photos, use a paid plan with a [persistent disk](https://render.com/docs/disks) mounted at `/app/uploads`, or move photo storage to object storage.

## Security & privacy
- No Aadhaar or other government ID is collected; citizens give only a name and mobile number.
- Passwords hashed with bcrypt; only SHA-256 hashes of session tokens are stored; sessions are revoked when an account is deactivated or its password reset.
- Photos are decoded and re-encoded (strips EXIF/GPS metadata), size- and pixel-limited.
- Per-IP rate limits on login, reporting, contact and tracking; tracking requires tracking ID **and** mobile number.
- The citizen tracking page never shows staff names or internal notes.

## Project structure

```
cmd/server/          API server (routes)
cmd/seed-demo/       creates demo staff accounts
internal/auth/       sessions, passwords, role checks
internal/config/     environment configuration
internal/database/   connection, migration runner, SQL migrations
internal/handlers/   HTTP handlers (public, staff workflow, admin, notifications)
internal/media/      photo validation / resizing
internal/middleware/ CORS, logging, rate limiting, static files
internal/validate/   phone number validation
Dockerfile           production image (used by Render)
render.yaml          Render Blueprint
```
