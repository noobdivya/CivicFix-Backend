// Package geo turns coordinates into human-readable place names using the
// free OpenStreetMap Nominatim service.
package geo

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SearchResult is a place found by name.
type SearchResult struct {
	Name        string  `json:"name"`
	DisplayName string  `json:"displayName"`
	Lat         float64 `json:"lat"`
	Lng         float64 `json:"lng"`
}

type Place struct {
	Area        string `json:"area"`
	City        string `json:"city"`
	State       string `json:"state"`
	DisplayName string `json:"displayName"`
}

// Geocoder calls Nominatim with caching and a 1 request/second limit,
// as required by the Nominatim usage policy.
type Geocoder struct {
	userAgent string
	client    *http.Client

	mu       sync.Mutex
	cache    map[string]Place
	searches map[string][]SearchResult
	lastCall time.Time
}

func NewGeocoder(userAgent string) *Geocoder {
	return &Geocoder{
		userAgent: userAgent,
		client:    &http.Client{Timeout: 8 * time.Second},
		cache:     make(map[string]Place),
		searches:  make(map[string][]SearchResult),
	}
}

// Reverse returns the place at the given coordinates. Results are cached
// per ~1 km grid cell, so nearby users share a lookup.
func (g *Geocoder) Reverse(ctx context.Context, lat, lng float64) (Place, error) {
	key := fmt.Sprintf("%.2f,%.2f", lat, lng)

	g.mu.Lock()
	defer g.mu.Unlock()

	if p, ok := g.cache[key]; ok {
		return p, nil
	}
	if wait := time.Second - time.Since(g.lastCall); wait > 0 {
		time.Sleep(wait)
	}
	g.lastCall = time.Now()

	q := url.Values{}
	q.Set("format", "jsonv2")
	q.Set("lat", strconv.FormatFloat(round(lat), 'f', -1, 64))
	q.Set("lon", strconv.FormatFloat(round(lng), 'f', -1, 64))
	q.Set("zoom", "14")
	q.Set("addressdetails", "1")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://nominatim.openstreetmap.org/reverse?"+q.Encode(), nil)
	if err != nil {
		return Place{}, err
	}
	req.Header.Set("User-Agent", g.userAgent)
	req.Header.Set("Accept-Language", "en")

	res, err := g.client.Do(req)
	if err != nil {
		return Place{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Place{}, fmt.Errorf("nominatim status %d", res.StatusCode)
	}

	var body struct {
		DisplayName string            `json:"display_name"`
		Address     map[string]string `json:"address"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return Place{}, err
	}

	a := body.Address
	p := Place{
		Area:        first(a, "suburb", "neighbourhood", "city_district", "quarter", "residential"),
		City:        first(a, "city", "town", "village", "state_district", "county"),
		State:       a["state"],
		DisplayName: body.DisplayName,
	}
	g.cache[key] = p
	return p, nil
}

// round keeps 2 decimals (~1 km) so we don't send precise user locations.
func round(v float64) float64 { return math.Round(v*100) / 100 }

func first(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := m[k]; v != "" {
			return v
		}
	}
	return ""
}

// Search looks places up by name (forward geocoding), optionally limited to
// comma-separated ISO country codes. Results are cached; requests share the
// 1-per-second limit with Reverse. Only call this on explicit user action
// (Nominatim's policy forbids search-as-you-type).
func (g *Geocoder) Search(ctx context.Context, query, countryCodes string) ([]SearchResult, error) {
	key := strings.ToLower(strings.TrimSpace(query)) + "|" + countryCodes

	g.mu.Lock()
	defer g.mu.Unlock()

	if r, ok := g.searches[key]; ok {
		return r, nil
	}
	if wait := time.Second - time.Since(g.lastCall); wait > 0 {
		time.Sleep(wait)
	}
	g.lastCall = time.Now()

	q := url.Values{}
	q.Set("format", "jsonv2")
	q.Set("q", query)
	q.Set("limit", "6")
	q.Set("addressdetails", "1")
	if countryCodes != "" {
		q.Set("countrycodes", countryCodes)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://nominatim.openstreetmap.org/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", g.userAgent)
	req.Header.Set("Accept-Language", "en")

	res, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nominatim status %d", res.StatusCode)
	}

	var body []struct {
		Lat         string            `json:"lat"`
		Lon         string            `json:"lon"`
		Name        string            `json:"name"`
		DisplayName string            `json:"display_name"`
		Address     map[string]string `json:"address"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := []SearchResult{}
	for _, b := range body {
		lat, err1 := strconv.ParseFloat(b.Lat, 64)
		lng, err2 := strconv.ParseFloat(b.Lon, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		name := b.Name
		if city := first(b.Address, "city", "town", "village", "state_district"); city != "" && city != name {
			if name == "" {
				name = city
			} else {
				name += ", " + city
			}
		}
		if name == "" {
			name = b.DisplayName
		}
		out = append(out, SearchResult{Name: name, DisplayName: b.DisplayName, Lat: lat, Lng: lng})
	}
	g.searches[key] = out
	return out, nil
}
