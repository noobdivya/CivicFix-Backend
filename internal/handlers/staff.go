package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/civicfix/backend/internal/auth"
	"github.com/jackc/pgx/v5"
)

// scopeSQL restricts issue queries (alias i) to what the user may see.
// It appends its argument to args and returns the SQL condition.
func scopeSQL(u *auth.User, args *[]any) string {
	switch u.Role {
	case auth.RoleAdmin:
		return "TRUE"
	case auth.RoleDepartment:
		*args = append(*args, u.DepartmentID)
		return fmt.Sprintf("i.department_id = $%d", len(*args))
	default: // worker
		*args = append(*args, u.ID)
		return fmt.Sprintf("i.assigned_worker_id = $%d", len(*args))
	}
}

type Person struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type StaffIssue struct {
	ID             int64      `json:"id"`
	TrackingCode   *string    `json:"trackingCode"`
	Title          string     `json:"title"`
	Category       string     `json:"category"`
	CategorySlug   string     `json:"categorySlug"`
	Status         string     `json:"status"`
	Priority       string     `json:"priority"`
	Reviewed       bool       `json:"reviewed"`
	Address        string     `json:"address"`
	Area           string     `json:"area"`
	DepartmentID   *int       `json:"departmentId"`
	DepartmentName *string    `json:"departmentName"`
	Worker         *Person    `json:"worker"`
	CreatedAt      time.Time  `json:"createdAt"`
	DueAt          *time.Time `json:"dueAt"`
	Overdue        bool       `json:"overdue"`
	PhotoURL       *string    `json:"photoUrl"`
}

const staffIssueColumns = `
	i.id, i.tracking_code, i.title, c.name, c.slug, i.status, i.priority, i.reviewed_at IS NOT NULL,
	i.address, i.area, i.department_id, d.name, w.id, w.name, i.created_at, i.due_at,
	COALESCE(i.status IN ('reported','assigned','in_progress') AND i.due_at < now(), FALSE),
	(SELECT '/uploads/' || p.file_path FROM issue_photos p WHERE p.issue_id = i.id AND p.kind = 'report' ORDER BY p.id LIMIT 1)`

const staffIssueJoins = `
	FROM issues i
	JOIN categories c ON c.id = i.category_id
	LEFT JOIN departments d ON d.id = i.department_id
	LEFT JOIN users w ON w.id = i.assigned_worker_id`

func scanStaffIssue(row pgx.CollectableRow) (StaffIssue, error) {
	var s StaffIssue
	var wID *int64
	var wName *string
	err := row.Scan(&s.ID, &s.TrackingCode, &s.Title, &s.Category, &s.CategorySlug, &s.Status, &s.Priority, &s.Reviewed,
		&s.Address, &s.Area, &s.DepartmentID, &s.DepartmentName, &wID, &wName, &s.CreatedAt, &s.DueAt, &s.Overdue, &s.PhotoURL)
	if wID != nil && wName != nil {
		s.Worker = &Person{ID: *wID, Name: *wName}
	}
	return s, err
}

// StaffIssues lists issues in the user's scope with filters:
// status (new|pending|open|active|<status>), priority, category,
// departmentId (admin), overdue=1, unassigned=1, q (search), sort, limit, offset.
func (h *Handler) StaffIssues(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	q := r.URL.Query()
	args := []any{}
	where := []string{scopeSQL(u, &args)}
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }

	switch s := q.Get("status"); s {
	case "", "all":
	case "new":
		where = append(where, "i.status = 'reported' AND i.reviewed_at IS NULL")
	case "pending":
		where = append(where, "i.status = 'reported' AND i.reviewed_at IS NOT NULL")
	case "open":
		where = append(where, "i.status IN ('reported','assigned','in_progress')")
	case "active":
		where = append(where, "i.status IN ('assigned','in_progress')")
	case "reported", "assigned", "in_progress", "resolved", "rejected":
		where = append(where, "i.status = "+arg(s))
	default:
		writeErr(w, badRequest("unknown status filter"), "staff issues")
		return
	}
	if p := q.Get("priority"); p != "" {
		if !slices.Contains(priorities, p) {
			writeErr(w, badRequest("unknown priority filter"), "staff issues")
			return
		}
		where = append(where, "i.priority = "+arg(p))
	}
	if c := q.Get("category"); c != "" {
		where = append(where, "c.slug = "+arg(c))
	}
	if d, err := strconv.Atoi(q.Get("departmentId")); err == nil && u.Role == auth.RoleAdmin {
		where = append(where, "i.department_id = "+arg(d))
	}
	if q.Get("overdue") == "1" {
		where = append(where, "i.status IN ('reported','assigned','in_progress') AND i.due_at < now()")
	}
	if q.Get("unassigned") == "1" {
		where = append(where, "i.assigned_worker_id IS NULL")
	}
	if s := strings.TrimSpace(q.Get("q")); s != "" {
		p := arg("%" + s + "%")
		where = append(where, fmt.Sprintf("(i.tracking_code ILIKE %s OR i.title ILIKE %s OR i.address ILIKE %s OR i.area ILIKE %s)", p, p, p, p))
	}

	order := "i.created_at DESC"
	if q.Get("sort") == "priority" {
		order = `CASE i.priority WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END,
			i.due_at NULLS LAST, i.created_at`
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 {
		offset = 0
	}

	cond := strings.Join(where, " AND ")
	var total int
	if err := h.DB.QueryRow(r.Context(), "SELECT COUNT(*) "+staffIssueJoins+" WHERE "+cond, args...).Scan(&total); err != nil {
		writeErr(w, err, "count staff issues")
		return
	}
	rows, err := h.DB.Query(r.Context(),
		"SELECT "+staffIssueColumns+staffIssueJoins+" WHERE "+cond+" ORDER BY "+order+
			fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset), args...)
	if err != nil {
		writeErr(w, err, "staff issues")
		return
	}
	items, err := pgx.CollectRows(rows, scanStaffIssue)
	if err != nil {
		writeErr(w, err, "scan staff issues")
		return
	}
	if items == nil {
		items = []StaffIssue{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

type Photo struct {
	URL       string    `json:"url"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"createdAt"`
}

type Event struct {
	ID         int64     `json:"id"`
	Type       string    `json:"type"`
	FromStatus *string   `json:"fromStatus"`
	ToStatus   *string   `json:"toStatus"`
	Message    string    `json:"message"`
	IsPublic   bool      `json:"isPublic"`
	ActorName  string    `json:"actorName"`
	ActorRole  string    `json:"actorRole"`
	CreatedAt  time.Time `json:"createdAt"`
}

type Reporter struct {
	Name  *string `json:"name"`
	Phone *string `json:"phone"`
}

type IssueDetail struct {
	StaffIssue
	Description     string     `json:"description"`
	Lat             *float64   `json:"lat"`
	Lng             *float64   `json:"lng"`
	ReviewedAt      *time.Time `json:"reviewedAt"`
	AssignedAt      *time.Time `json:"assignedAt"`
	StartedAt       *time.Time `json:"startedAt"`
	ResolvedAt      *time.Time `json:"resolvedAt"`
	RejectionReason *string    `json:"rejectionReason"`
	Reporter        *Reporter  `json:"reporter"` // name and phone, to contact the citizen
	Photos          []Photo    `json:"photos"`
	Events          []Event    `json:"events"`
}

// loadIssueDetail returns one issue (with photos and full timeline) if the
// user may access it.
func (h *Handler) loadIssueDetail(ctx context.Context, u *auth.User, id int64) (*IssueDetail, error) {
	args := []any{id}
	cond := scopeSQL(u, &args)
	var d IssueDetail
	var wID *int64
	var wName *string
	var rep Reporter
	err := h.DB.QueryRow(ctx, "SELECT "+staffIssueColumns+`,
			i.description, i.latitude, i.longitude, i.reviewed_at, i.assigned_at, i.started_at, i.resolved_at,
			i.rejection_reason, i.reporter_name, i.reporter_phone`+
		staffIssueJoins+" WHERE i.id = $1 AND "+cond, args...,
	).Scan(&d.ID, &d.TrackingCode, &d.Title, &d.Category, &d.CategorySlug, &d.Status, &d.Priority, &d.Reviewed,
		&d.Address, &d.Area, &d.DepartmentID, &d.DepartmentName, &wID, &wName, &d.CreatedAt, &d.DueAt, &d.Overdue, &d.PhotoURL,
		&d.Description, &d.Lat, &d.Lng, &d.ReviewedAt, &d.AssignedAt, &d.StartedAt, &d.ResolvedAt,
		&d.RejectionReason, &rep.Name, &rep.Phone)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound("issue not found")
	}
	if err != nil {
		return nil, err
	}
	if wID != nil && wName != nil {
		d.Worker = &Person{ID: *wID, Name: *wName}
	}
	d.Reporter = &rep

	rows, err := h.DB.Query(ctx,
		`SELECT '/uploads/' || file_path, kind, created_at FROM issue_photos WHERE issue_id = $1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	d.Photos, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Photo, error) {
		var p Photo
		return p, row.Scan(&p.URL, &p.Kind, &p.CreatedAt)
	})
	if err != nil {
		return nil, err
	}

	rows, err = h.DB.Query(ctx, `
		SELECT id, type, from_status, to_status, message, is_public, actor_name, actor_role, created_at
		FROM issue_events WHERE issue_id = $1 ORDER BY created_at, id`, id)
	if err != nil {
		return nil, err
	}
	d.Events, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Event, error) {
		var e Event
		return e, row.Scan(&e.ID, &e.Type, &e.FromStatus, &e.ToStatus, &e.Message, &e.IsPublic, &e.ActorName, &e.ActorRole, &e.CreatedAt)
	})
	if err != nil {
		return nil, err
	}
	if d.Photos == nil {
		d.Photos = []Photo{}
	}
	if d.Events == nil {
		d.Events = []Event{}
	}
	return &d, nil
}

func (h *Handler) StaffIssue(w http.ResponseWriter, r *http.Request) {
	id, err := issueID(r)
	if err != nil {
		writeErr(w, err, "issue detail")
		return
	}
	d, err := h.loadIssueDetail(r.Context(), auth.FromContext(r.Context()), id)
	if err != nil {
		writeErr(w, err, "issue detail")
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// StaffSummary returns counts for the user's scope (their department,
// their tasks, or the whole city for admins).
func (h *Handler) StaffSummary(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	args := []any{}
	cond := scopeSQL(u, &args)
	var s struct {
		New                int      `json:"new"`
		Pending            int      `json:"pending"`
		Assigned           int      `json:"assigned"`
		InProgress         int      `json:"inProgress"`
		Resolved           int      `json:"resolved"`
		ResolvedThisWeek   int      `json:"resolvedThisWeek"`
		Rejected           int      `json:"rejected"`
		Overdue            int      `json:"overdue"`
		Critical           int      `json:"critical"`
		AvgResolutionHours *float64 `json:"avgResolutionHours"`
	}
	err := h.DB.QueryRow(r.Context(), `
		SELECT
			COUNT(*) FILTER (WHERE status = 'reported' AND reviewed_at IS NULL),
			COUNT(*) FILTER (WHERE status = 'reported' AND reviewed_at IS NOT NULL),
			COUNT(*) FILTER (WHERE status = 'assigned'),
			COUNT(*) FILTER (WHERE status = 'in_progress'),
			COUNT(*) FILTER (WHERE status = 'resolved'),
			COUNT(*) FILTER (WHERE status = 'resolved' AND resolved_at > now() - interval '7 days'),
			COUNT(*) FILTER (WHERE status = 'rejected'),
			COUNT(*) FILTER (WHERE status IN ('reported','assigned','in_progress') AND due_at < now()),
			COUNT(*) FILTER (WHERE status IN ('reported','assigned','in_progress') AND priority = 'critical'),
			AVG(EXTRACT(EPOCH FROM (resolved_at - created_at)) / 3600) FILTER (WHERE status = 'resolved')
		FROM issues i WHERE `+cond, args...,
	).Scan(&s.New, &s.Pending, &s.Assigned, &s.InProgress, &s.Resolved, &s.ResolvedThisWeek, &s.Rejected,
		&s.Overdue, &s.Critical, &s.AvgResolutionHours)
	if err != nil {
		writeErr(w, err, "staff summary")
		return
	}
	writeJSON(w, http.StatusOK, s)
}

type Worker struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Phone          string `json:"phone"`
	DepartmentID   *int   `json:"departmentId"`
	DepartmentName string `json:"departmentName"`
	ActiveTasks    int    `json:"activeTasks"`
}

// Workers lists active field workers (own department for officers;
// ?departmentId= or all for admins) with their current workload.
func (h *Handler) Workers(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	args := []any{}
	cond := "TRUE"
	if u.Role == auth.RoleDepartment {
		args = append(args, u.DepartmentID)
		cond = "u.department_id = $1"
	} else if d, err := strconv.Atoi(r.URL.Query().Get("departmentId")); err == nil {
		args = append(args, d)
		cond = "u.department_id = $1"
	}
	rows, err := h.DB.Query(r.Context(), `
		SELECT u.id, u.name, u.phone, u.department_id, COALESCE(d.name, ''),
			(SELECT COUNT(*) FROM issues i WHERE i.assigned_worker_id = u.id AND i.status IN ('assigned','in_progress'))
		FROM users u LEFT JOIN departments d ON d.id = u.department_id
		WHERE u.role = 'worker' AND u.active AND `+cond+`
		ORDER BY u.name`, args...)
	if err != nil {
		writeErr(w, err, "workers")
		return
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Worker, error) {
		var wk Worker
		return wk, row.Scan(&wk.ID, &wk.Name, &wk.Phone, &wk.DepartmentID, &wk.DepartmentName, &wk.ActiveTasks)
	})
	if err != nil {
		writeErr(w, err, "scan workers")
		return
	}
	if items == nil {
		items = []Worker{}
	}
	writeJSON(w, http.StatusOK, items)
}

type Department struct {
	ID          int      `json:"id"`
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Categories  []string `json:"categories"`
}

// Departments lists departments and the categories they handle.
func (h *Handler) Departments(w http.ResponseWriter, r *http.Request) {
	rows, err := h.DB.Query(r.Context(), `
		SELECT d.id, d.slug, d.name, d.description,
			COALESCE(array_agg(c.name ORDER BY c.sort_order) FILTER (WHERE c.id IS NOT NULL), '{}')
		FROM departments d LEFT JOIN categories c ON c.department_id = d.id
		GROUP BY d.id ORDER BY d.name`)
	if err != nil {
		writeErr(w, err, "departments")
		return
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Department, error) {
		var d Department
		return d, row.Scan(&d.ID, &d.Slug, &d.Name, &d.Description, &d.Categories)
	})
	if err != nil {
		writeErr(w, err, "scan departments")
		return
	}
	writeJSON(w, http.StatusOK, items)
}
