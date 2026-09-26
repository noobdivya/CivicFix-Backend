package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Port               string
	DatabaseURL        string
	Timezone           string // used for "today" / per-day statistics
	CORSAllowedOrigins []string

	// Map shown when the user's location is unavailable.
	DefaultCityName string
	DefaultCityLat  float64
	DefaultCityLng  float64

	// Identifies this app to the OpenStreetMap Nominatim geocoder (required by its usage policy).
	NominatimUserAgent string
	// Limit place search to these ISO country codes (comma-separated, empty = worldwide).
	SearchCountryCodes string

	// Where uploaded photos are stored on disk.
	UploadDir string

	// First admin account, created on startup if no admin exists.
	AdminEmail    string
	AdminPassword string
	// Send the session cookie only over HTTPS (enable in production).
	CookieSecure bool
	// Reverse proxies in front of the API that append to X-Forwarded-For
	// (0 = direct; 2 = Vercel rewrite -> Render).
	TrustedProxyHops int
}

// Load reads configuration from environment variables, falling back to a
// local .env file (if present) and then to development defaults.
func Load() Config {
	_ = godotenv.Load()

	origins := strings.Split(getEnv("CORS_ALLOWED_ORIGINS", "http://localhost:3000,http://127.0.0.1:3000"), ",")
	for i := range origins {
		origins[i] = strings.TrimSpace(origins[i])
	}

	return Config{
		Port:               getEnv("PORT", "8080"),
		DatabaseURL:        getEnv("DATABASE_URL", "postgres://civicfix:civicfix@localhost:5432/civicfix?sslmode=disable"),
		CORSAllowedOrigins: origins,
		Timezone:           getEnv("APP_TIMEZONE", "Asia/Kolkata"),
		DefaultCityName:    getEnv("DEFAULT_CITY_NAME", "New Delhi"),
		DefaultCityLat:     getFloat("DEFAULT_CITY_LAT", 28.6139),
		DefaultCityLng:     getFloat("DEFAULT_CITY_LNG", 77.2090),
		NominatimUserAgent: getEnv("NOMINATIM_USER_AGENT", "CivicFix/0.1 (local development)"),
		SearchCountryCodes: getEnv("SEARCH_COUNTRY_CODES", "in"),
		UploadDir:          getEnv("UPLOAD_DIR", "uploads"),
		AdminEmail:         getEnv("ADMIN_EMAIL", "admin@civicfix.local"),
		AdminPassword:      os.Getenv("ADMIN_PASSWORD"),
		CookieSecure:       os.Getenv("COOKIE_SECURE") == "true",
		TrustedProxyHops:   getInt("TRUSTED_PROXY_HOPS", 0),
	}
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getFloat(key string, fallback float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil {
		return v
	}
	return fallback
}

func getInt(key string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v >= 0 {
		return v
	}
	return fallback
}
