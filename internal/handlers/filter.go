package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// issueFilter narrows the public dashboard and map to an area, statuses,
// a category and a time window. The zero value means "everything".
type issueFilter struct {
	hasArea   bool
	lat, lng  float64
	radiusKm  float64
	statuses  []string // concrete issue statuses
	category  string
	sinceDays int
}

const (
	defaultRadiusKm = 15
	maxRadiusKm     = 100
)

// statusGroups maps the public status filter to issue statuses.
var statusGroups = map[string][]string{
	"reported": {"reported"},
	"progress": {"assigned", "in_progress"},
	"resolved": {"resolved"},
}

// parseIssueFilter reads ?lat=&lng=&radiusKm=&status=reported,progress,resolved&category=&sinceDays=
func parseIssueFilter(r *http.Request) (issueFilter, error) {
	q := r.URL.Query()
	var f issueFilter

	if q.Get("lat") != "" || q.Get("lng") != "" {
		lat, errLat := strconv.ParseFloat(q.Get("lat"), 64)
		lng, errLng := strconv.ParseFloat(q.Get("lng"), 64)
		if errLat != nil || errLng != nil || lat < -90 || lat > 90 || lng < -180 || lng > 180 {
			return f, badRequest("invalid lat/lng")
		}
		f.hasArea, f.lat, f.lng, f.radiusKm = true, lat, lng, defaultRadiusKm
		if s := q.Get("radiusKm"); s != "" {
			rad, err := strconv.ParseFloat(s, 64)
			if err != nil || rad <= 0 || rad > maxRadiusKm {
				return f, badRequest(fmt.Sprintf("radiusKm must be between 1 and %d", maxRadiusKm))
			}
			f.radiusKm = rad
		}
	}

	if s := q.Get("status"); s != "" {
		for _, g := range strings.Split(s, ",") {
			st, ok := statusGroups[strings.TrimSpace(g)]
			if !ok {
				return f, badRequest("status must be a list of reported, progress, resolved")
			}
			f.statuses = append(f.statuses, st...)
		}
	}

	f.category = strings.TrimSpace(q.Get("category"))

	if s := q.Get("sinceDays"); s != "" {
		d, err := strconv.Atoi(s)
		if err != nil || d < 1 || d > 365 {
			return f, badRequest("sinceDays must be between 1 and 365")
		}
		f.sinceDays = d
	}
	return f, nil
}

// distanceKmSQL is the great-circle distance (km) from ($lat, $lng) to issue i.
func distanceKmSQL(latArg, lngArg string) string {
	return fmt.Sprintf(`(6371 * acos(LEAST(1.0, GREATEST(-1.0,
		cos(radians(%[1]s)) * cos(radians(i.latitude)) * cos(radians(i.longitude) - radians(%[2]s))
		+ sin(radians(%[1]s)) * sin(radians(i.latitude))))))`, latArg, lngArg)
}

// where returns SQL conditions on issues aliased as i, appending to args.
func (f issueFilter) where(args *[]any) string {
	arg := func(v any) string { *args = append(*args, v); return fmt.Sprintf("$%d", len(*args)) }
	conds := []string{"TRUE"}
	if f.hasArea {
		lat, lng, rad := arg(f.lat), arg(f.lng), arg(f.radiusKm)
		conds = append(conds, fmt.Sprintf("i.latitude IS NOT NULL AND %s <= %s", distanceKmSQL(lat+"::float8", lng+"::float8"), rad+"::float8"))
	}
	if len(f.statuses) > 0 {
		conds = append(conds, "i.status = ANY("+arg(f.statuses)+")")
	}
	if f.category != "" {
		conds = append(conds, "i.category_id = (SELECT id FROM categories WHERE slug = "+arg(f.category)+")")
	}
	if f.sinceDays > 0 {
		conds = append(conds, fmt.Sprintf("i.created_at > now() - make_interval(days => %s)", arg(f.sinceDays)))
	}
	return strings.Join(conds, " AND ")
}

// cte returns "WITH f AS (...filtered issues...)" for use in dashboard queries.
func (f issueFilter) cte(args *[]any) string {
	return "WITH f AS (SELECT i.* FROM issues i WHERE " + f.where(args) + ") "
}
