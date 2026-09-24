package handlers

import (
	"log"
	"net/http"
	"strconv"
	"time"
)

type MapIssue struct {
	ID           int64     `json:"id"`
	Title        string    `json:"title"`
	Category     string    `json:"category"`
	CategorySlug string    `json:"categorySlug"`
	Status       string    `json:"status"`
	Address      string    `json:"address"`
	Lat          float64   `json:"lat"`
	Lng          float64   `json:"lng"`
	CreatedAt    time.Time `json:"createdAt"`
}

// MapDefault returns the city shown when the user's location is unavailable.
func (h *Handler) MapDefault(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name": h.Config.DefaultCityName,
		"lat":  h.Config.DefaultCityLat,
		"lng":  h.Config.DefaultCityLng,
	})
}

// MapIssues returns the most recent issues that have a location, for
// plotting as markers on the landing page map.
func (h *Handler) MapIssues(w http.ResponseWriter, r *http.Request) {
	rows, err := h.DB.Query(r.Context(), `
		SELECT i.id, i.title, c.name, c.slug, i.status, i.address, i.latitude, i.longitude, i.created_at
		FROM issues i
		JOIN categories c ON c.id = i.category_id
		WHERE i.latitude IS NOT NULL AND i.longitude IS NOT NULL
		  AND i.status <> 'rejected'
		ORDER BY i.created_at DESC
		LIMIT 500`)
	if err != nil {
		log.Printf("map issues: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load map issues")
		return
	}
	defer rows.Close()

	issues := []MapIssue{}
	for rows.Next() {
		var m MapIssue
		if err := rows.Scan(&m.ID, &m.Title, &m.Category, &m.CategorySlug, &m.Status, &m.Address, &m.Lat, &m.Lng, &m.CreatedAt); err != nil {
			log.Printf("scan map issue: %v", err)
			writeError(w, http.StatusInternalServerError, "could not load map issues")
			return
		}
		issues = append(issues, m)
	}
	if err := rows.Err(); err != nil {
		log.Printf("map issues rows: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load map issues")
		return
	}
	writeJSON(w, http.StatusOK, issues)
}

// ReverseGeocode turns ?lat=&lng= into an area / city name.
func (h *Handler) ReverseGeocode(w http.ResponseWriter, r *http.Request) {
	lat, errLat := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
	lng, errLng := strconv.ParseFloat(r.URL.Query().Get("lng"), 64)
	if errLat != nil || errLng != nil || lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		writeError(w, http.StatusBadRequest, "valid lat and lng query parameters are required")
		return
	}

	place, err := h.Geocoder.Reverse(r.Context(), lat, lng)
	if err != nil {
		log.Printf("reverse geocode: %v", err)
		writeError(w, http.StatusBadGateway, "could not look up this location")
		return
	}
	writeJSON(w, http.StatusOK, place)
}
