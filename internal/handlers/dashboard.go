package handlers

import (
	"log"
	"net/http"
	"time"
)

type DashboardStats struct {
	Total              int      `json:"total"`
	Reported           int      `json:"reported"`
	InProgress         int      `json:"inProgress"` // assigned + in_progress
	Resolved           int      `json:"resolved"`
	AvgResolutionHours *float64 `json:"avgResolutionHours"` // null until an issue is resolved
}

type CategoryCount struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	IssueCount  int    `json:"issueCount"`
}

type RecentIssue struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	Category  string    `json:"category"`
	Status    string    `json:"status"`
	Address   string    `json:"address"`
	CreatedAt time.Time `json:"createdAt"`
}

type TrendPoint struct {
	Date     string `json:"date"` // YYYY-MM-DD
	Reported int    `json:"reported"`
	Resolved int    `json:"resolved"`
}

type AreaCount struct {
	Area  string `json:"area"`
	Count int    `json:"count"`
}

type ActivityItem struct {
	IssueID int64     `json:"issueId"`
	Title   string    `json:"title"`
	Address string    `json:"address"`
	Type    string    `json:"type"` // "reported" | "resolved"
	At      time.Time `json:"at"`
}

type DashboardResponse struct {
	Stats        DashboardStats  `json:"stats"`
	Categories   []CategoryCount `json:"categories"`
	RecentIssues []RecentIssue   `json:"recentIssues"`
	Trend        []TrendPoint    `json:"trend"`    // last 14 days, oldest first
	TopAreas     []AreaCount     `json:"topAreas"` // areas with the most reports
	Activity     []ActivityItem  `json:"activity"` // latest reported/resolved events
	UpdatedAt    time.Time       `json:"updatedAt"`
}

// Dashboard returns live issue counts by status, per-category totals and
// the latest reports for the public landing page dashboard.
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	resp := DashboardResponse{
		Categories:   []CategoryCount{},
		RecentIssues: []RecentIssue{},
		Trend:        []TrendPoint{},
		TopAreas:     []AreaCount{},
		Activity:     []ActivityItem{},
		UpdatedAt:    time.Now().UTC(),
	}

	err := h.DB.QueryRow(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status = 'reported'),
			COUNT(*) FILTER (WHERE status IN ('assigned', 'in_progress')),
			COUNT(*) FILTER (WHERE status = 'resolved'),
			AVG(EXTRACT(EPOCH FROM (resolved_at - created_at)) / 3600)
				FILTER (WHERE status = 'resolved' AND resolved_at IS NOT NULL)
		FROM issues`,
	).Scan(&resp.Stats.Total, &resp.Stats.Reported, &resp.Stats.InProgress, &resp.Stats.Resolved, &resp.Stats.AvgResolutionHours)
	if err != nil {
		log.Printf("dashboard stats: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load dashboard")
		return
	}

	rows, err := h.DB.Query(ctx, `
		SELECT c.slug, c.name, c.description, c.icon, COUNT(i.id)
		FROM categories c
		LEFT JOIN issues i ON i.category_id = c.id
		GROUP BY c.id
		ORDER BY c.sort_order`)
	if err != nil {
		log.Printf("dashboard categories: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load dashboard")
		return
	}
	for rows.Next() {
		var c CategoryCount
		if err := rows.Scan(&c.Slug, &c.Name, &c.Description, &c.Icon, &c.IssueCount); err != nil {
			rows.Close()
			log.Printf("scan category: %v", err)
			writeError(w, http.StatusInternalServerError, "could not load dashboard")
			return
		}
		resp.Categories = append(resp.Categories, c)
	}
	rows.Close()

	rows, err = h.DB.Query(ctx, `
		SELECT i.id, i.title, c.name, i.status, i.address, i.created_at
		FROM issues i
		JOIN categories c ON c.id = i.category_id
		ORDER BY i.created_at DESC
		LIMIT 6`)
	if err != nil {
		log.Printf("dashboard recent: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load dashboard")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var ri RecentIssue
		if err := rows.Scan(&ri.ID, &ri.Title, &ri.Category, &ri.Status, &ri.Address, &ri.CreatedAt); err != nil {
			log.Printf("scan recent: %v", err)
			writeError(w, http.StatusInternalServerError, "could not load dashboard")
			return
		}
		resp.RecentIssues = append(resp.RecentIssues, ri)
	}
	if err := rows.Err(); err != nil {
		log.Printf("dashboard recent rows: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load dashboard")
		return
	}

	if err := h.loadTrend(r, &resp); err != nil {
		log.Printf("dashboard trend: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load dashboard")
		return
	}
	if err := h.loadTopAreas(r, &resp); err != nil {
		log.Printf("dashboard areas: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load dashboard")
		return
	}
	if err := h.loadActivity(r, &resp); err != nil {
		log.Printf("dashboard activity: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load dashboard")
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// loadTrend counts issues reported and resolved on each of the last 14 days.
func (h *Handler) loadTrend(r *http.Request, resp *DashboardResponse) error {
	rows, err := h.DB.Query(r.Context(), `
		SELECT to_char(g.d, 'YYYY-MM-DD'),
			(SELECT COUNT(*) FROM issues WHERE created_at::date = g.d::date),
			(SELECT COUNT(*) FROM issues WHERE resolved_at::date = g.d::date)
		FROM generate_series(current_date - 13, current_date, interval '1 day') AS g(d)
		ORDER BY g.d`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var p TrendPoint
		if err := rows.Scan(&p.Date, &p.Reported, &p.Resolved); err != nil {
			return err
		}
		resp.Trend = append(resp.Trend, p)
	}
	return rows.Err()
}

// loadTopAreas returns the 5 areas with the most reports (recurring hotspots).
func (h *Handler) loadTopAreas(r *http.Request, resp *DashboardResponse) error {
	rows, err := h.DB.Query(r.Context(), `
		SELECT address, COUNT(*)
		FROM issues
		WHERE address <> ''
		GROUP BY address
		ORDER BY COUNT(*) DESC, address
		LIMIT 5`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a AreaCount
		if err := rows.Scan(&a.Area, &a.Count); err != nil {
			return err
		}
		resp.TopAreas = append(resp.TopAreas, a)
	}
	return rows.Err()
}

// loadActivity returns the latest "reported" and "resolved" events.
func (h *Handler) loadActivity(r *http.Request, resp *DashboardResponse) error {
	rows, err := h.DB.Query(r.Context(), `
		SELECT id, title, address, 'reported', created_at FROM issues
		UNION ALL
		SELECT id, title, address, 'resolved', resolved_at FROM issues WHERE resolved_at IS NOT NULL
		ORDER BY 5 DESC
		LIMIT 6`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a ActivityItem
		if err := rows.Scan(&a.IssueID, &a.Title, &a.Address, &a.Type, &a.At); err != nil {
			return err
		}
		resp.Activity = append(resp.Activity, a)
	}
	return rows.Err()
}
