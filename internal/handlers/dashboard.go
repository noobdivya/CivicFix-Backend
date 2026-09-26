package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
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

// Dashboard returns live issue statistics for the public landing page.
// Everything can be narrowed with the filters described in parseIssueFilter
// (area + radius, status, category, time window).
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	f, err := parseIssueFilter(r)
	if err != nil {
		writeErr(w, err, "dashboard")
		return
	}
	resp, err := h.buildDashboard(r.Context(), f)
	if err != nil {
		writeErr(w, err, "dashboard")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) buildDashboard(ctx context.Context, f issueFilter) (*DashboardResponse, error) {
	resp := &DashboardResponse{UpdatedAt: time.Now().UTC()}
	args := []any{}
	with := f.cte(&args)

	if err := h.DB.QueryRow(ctx, with+`
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status = 'reported'),
			COUNT(*) FILTER (WHERE status IN ('assigned', 'in_progress')),
			COUNT(*) FILTER (WHERE status = 'resolved'),
			AVG(EXTRACT(EPOCH FROM (resolved_at - created_at)) / 3600)
				FILTER (WHERE status = 'resolved' AND resolved_at IS NOT NULL)
		FROM f`, args...,
	).Scan(&resp.Stats.Total, &resp.Stats.Reported, &resp.Stats.InProgress, &resp.Stats.Resolved, &resp.Stats.AvgResolutionHours); err != nil {
		return nil, err
	}

	var err error
	if resp.Categories, err = collect(ctx, h, with+`
		SELECT c.slug, c.name, c.description, c.icon, COUNT(f.id)
		FROM categories c LEFT JOIN f ON f.category_id = c.id
		GROUP BY c.id ORDER BY c.sort_order`, args, func(row pgx.CollectableRow) (CategoryCount, error) {
		var c CategoryCount
		return c, row.Scan(&c.Slug, &c.Name, &c.Description, &c.Icon, &c.IssueCount)
	}); err != nil {
		return nil, err
	}

	if resp.RecentIssues, err = collect(ctx, h, with+`
		SELECT f.id, f.title, c.name, f.status, COALESCE(NULLIF(f.area, ''), f.address), f.created_at
		FROM f JOIN categories c ON c.id = f.category_id
		ORDER BY f.created_at DESC LIMIT 6`, args, func(row pgx.CollectableRow) (RecentIssue, error) {
		var ri RecentIssue
		return ri, row.Scan(&ri.ID, &ri.Title, &ri.Category, &ri.Status, &ri.Address, &ri.CreatedAt)
	}); err != nil {
		return nil, err
	}

	// Issues reported and resolved on each of the last 14 days.
	if resp.Trend, err = collect(ctx, h, with+`
		SELECT to_char(g.d, 'YYYY-MM-DD'),
			(SELECT COUNT(*) FROM f WHERE f.created_at::date = g.d::date),
			(SELECT COUNT(*) FROM f WHERE f.resolved_at::date = g.d::date)
		FROM generate_series(current_date - 13, current_date, interval '1 day') AS g(d)
		ORDER BY g.d`, args, func(row pgx.CollectableRow) (TrendPoint, error) {
		var p TrendPoint
		return p, row.Scan(&p.Date, &p.Reported, &p.Resolved)
	}); err != nil {
		return nil, err
	}

	// The 5 localities with the most reports (recurring hotspots).
	if resp.TopAreas, err = collect(ctx, h, with+`
		SELECT COALESCE(NULLIF(area, ''), address) AS place, COUNT(*)
		FROM f WHERE COALESCE(NULLIF(area, ''), address) <> ''
		GROUP BY place ORDER BY COUNT(*) DESC, place LIMIT 5`, args, func(row pgx.CollectableRow) (AreaCount, error) {
		var a AreaCount
		return a, row.Scan(&a.Area, &a.Count)
	}); err != nil {
		return nil, err
	}

	// Latest "reported" and "resolved" events.
	if resp.Activity, err = collect(ctx, h, with+`
		SELECT id, title, COALESCE(NULLIF(area, ''), address), 'reported', created_at FROM f
		UNION ALL
		SELECT id, title, COALESCE(NULLIF(area, ''), address), 'resolved', resolved_at FROM f WHERE resolved_at IS NOT NULL
		ORDER BY 5 DESC LIMIT 6`, args, func(row pgx.CollectableRow) (ActivityItem, error) {
		var a ActivityItem
		return a, row.Scan(&a.IssueID, &a.Title, &a.Address, &a.Type, &a.At)
	}); err != nil {
		return nil, err
	}
	return resp, nil
}

// collect runs a query and scans every row, returning an empty (not nil) slice.
func collect[T any](ctx context.Context, h *Handler, sql string, args []any, scan pgx.RowToFunc[T]) ([]T, error) {
	rows, err := h.DB.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, scan)
	if items == nil {
		items = []T{}
	}
	return items, err
}
