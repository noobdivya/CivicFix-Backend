package handlers

import (
	"errors"
	"net/http"
	"net/mail"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/civicfix/backend/internal/auth"
	"github.com/civicfix/backend/internal/validate"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type StaffUser struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	Email          string     `json:"email"`
	Phone          string     `json:"phone"`
	Role           string     `json:"role"`
	DepartmentID   *int       `json:"departmentId"`
	DepartmentName *string    `json:"departmentName"`
	Active         bool       `json:"active"`
	CreatedAt      time.Time  `json:"createdAt"`
	LastLoginAt    *time.Time `json:"lastLoginAt"`
	OpenTasks      int        `json:"openTasks"`
}

// AdminUsers lists all staff accounts.
func (h *Handler) AdminUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := h.DB.Query(r.Context(), `
		SELECT u.id, u.name, u.email, u.phone, u.role, u.department_id, d.name, u.active, u.created_at, u.last_login_at,
			(SELECT COUNT(*) FROM issues i WHERE i.assigned_worker_id = u.id AND i.status IN ('assigned','in_progress'))
		FROM users u LEFT JOIN departments d ON d.id = u.department_id
		ORDER BY CASE u.role WHEN 'admin' THEN 0 WHEN 'department' THEN 1 ELSE 2 END, d.name NULLS FIRST, u.name`)
	if err != nil {
		writeErr(w, err, "admin users")
		return
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (StaffUser, error) {
		var s StaffUser
		return s, row.Scan(&s.ID, &s.Name, &s.Email, &s.Phone, &s.Role, &s.DepartmentID, &s.DepartmentName,
			&s.Active, &s.CreatedAt, &s.LastLoginAt, &s.OpenTasks)
	})
	if err != nil {
		writeErr(w, err, "scan admin users")
		return
	}
	writeJSON(w, http.StatusOK, items)
}

type userInput struct {
	Name         *string `json:"name"`
	Email        *string `json:"email"`
	Phone        *string `json:"phone"`
	Role         *string `json:"role"`
	DepartmentID *int    `json:"departmentId"`
	Password     *string `json:"password"`
	Active       *bool   `json:"active"`
}

// validate checks the fields that are present; create=true requires all.
func (in *userInput) validate(create bool) map[string]string {
	errs := map[string]string{}
	if in.Name != nil || create {
		if in.Name == nil || len(strings.TrimSpace(*in.Name)) < 2 {
			errs["name"] = "Enter the person's name."
		}
	}
	if in.Email != nil || create {
		if in.Email == nil {
			errs["email"] = "Enter a valid email address."
		} else if a, err := mail.ParseAddress(strings.TrimSpace(*in.Email)); err != nil || a.Address != strings.TrimSpace(*in.Email) {
			errs["email"] = "Enter a valid email address."
		}
	}
	if in.Phone != nil && strings.TrimSpace(*in.Phone) != "" {
		if p, ok := validate.IndianMobile(*in.Phone); ok {
			in.Phone = &p
		} else {
			errs["phone"] = "Enter a valid 10-digit mobile number."
		}
	}
	if in.Role != nil || create {
		if in.Role == nil || !slices.Contains([]string{auth.RoleAdmin, auth.RoleDepartment, auth.RoleWorker}, *in.Role) {
			errs["role"] = "Choose a role."
		} else if *in.Role != auth.RoleAdmin && in.DepartmentID == nil {
			errs["departmentId"] = "Choose a department for this role."
		}
	}
	if in.Password != nil || create {
		if in.Password == nil || len(*in.Password) < 8 || len(*in.Password) > 72 {
			errs["password"] = "Password must be 8–72 characters."
		}
	}
	return errs
}

func strOr(p *string, def string) string {
	if p == nil {
		return def
	}
	return strings.TrimSpace(*p)
}

// AdminCreateUser creates a staff account.
func (h *Handler) AdminCreateUser(w http.ResponseWriter, r *http.Request) {
	var in userInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeErr(w, err, "create user")
		return
	}
	if errs := in.validate(true); len(errs) > 0 {
		writeErr(w, invalid(errs), "create user")
		return
	}
	hash, err := auth.HashPassword(*in.Password)
	if err != nil {
		writeErr(w, err, "hash password")
		return
	}
	dept := in.DepartmentID
	if *in.Role == auth.RoleAdmin {
		dept = nil
	}
	var id int64
	err = h.DB.QueryRow(r.Context(), `
		INSERT INTO users (name, email, phone, role, department_id, password_hash)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		strOr(in.Name, ""), strOr(in.Email, ""), strOr(in.Phone, ""), *in.Role, dept, hash).Scan(&id)
	if err != nil {
		writeErr(w, userWriteErr(err), "create user")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func userWriteErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return invalid(map[string]string{"email": "An account with this email already exists."})
		case "23503":
			return invalid(map[string]string{"departmentId": "Choose a valid department."})
		}
	}
	return err
}

// AdminUpdateUser edits a staff account (any subset of fields). Deactivating
// an account or changing its password signs it out everywhere.
func (h *Handler) AdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	me := auth.FromContext(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, notFound("user not found"), "update user")
		return
	}
	var in userInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeErr(w, err, "update user")
		return
	}
	if id == me.ID && ((in.Active != nil && !*in.Active) || (in.Role != nil && *in.Role != auth.RoleAdmin)) {
		writeErr(w, badRequest("you can't deactivate or demote your own account"), "update user")
		return
	}
	if errs := in.validate(false); len(errs) > 0 {
		writeErr(w, invalid(errs), "update user")
		return
	}

	sets := []string{}
	args := []any{id}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, col+" = $"+strconv.Itoa(len(args)))
	}
	if in.Name != nil {
		add("name", strings.TrimSpace(*in.Name))
	}
	if in.Email != nil {
		add("email", strings.TrimSpace(*in.Email))
	}
	if in.Phone != nil {
		add("phone", strings.TrimSpace(*in.Phone))
	}
	if in.Role != nil {
		add("role", *in.Role)
		if *in.Role == auth.RoleAdmin {
			add("department_id", nil)
		}
	}
	if in.DepartmentID != nil && (in.Role == nil || *in.Role != auth.RoleAdmin) {
		add("department_id", *in.DepartmentID)
	}
	if in.Active != nil {
		add("active", *in.Active)
	}
	if in.Password != nil {
		hash, err := auth.HashPassword(*in.Password)
		if err != nil {
			writeErr(w, err, "hash password")
			return
		}
		add("password_hash", hash)
	}
	if len(sets) == 0 {
		writeErr(w, badRequest("nothing to update"), "update user")
		return
	}

	tag, err := h.DB.Exec(r.Context(), "UPDATE users SET "+strings.Join(sets, ", ")+" WHERE id = $1", args...)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" {
			writeErr(w, invalid(map[string]string{"departmentId": "Choose a department for this role."}), "update user")
			return
		}
		writeErr(w, userWriteErr(err), "update user")
		return
	}
	if tag.RowsAffected() == 0 {
		writeErr(w, notFound("user not found"), "update user")
		return
	}
	if (in.Active != nil && !*in.Active) || in.Password != nil {
		_, _ = h.DB.Exec(r.Context(), `DELETE FROM sessions WHERE user_id = $1`, id)
	}
	w.WriteHeader(http.StatusNoContent)
}

// AdminAnalytics returns city-wide performance figures: per department,
// per category, recurring problem locations and field worker output.
func (h *Handler) AdminAnalytics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	type deptStat struct {
		ID                 int      `json:"id"`
		Name               string   `json:"name"`
		Total              int      `json:"total"`
		Open               int      `json:"open"`
		Overdue            int      `json:"overdue"`
		Resolved           int      `json:"resolved"`
		AvgResolutionHours *float64 `json:"avgResolutionHours"`
		Officers           int      `json:"officers"`
		Workers            int      `json:"workers"`
	}
	type catStat struct {
		Name               string   `json:"name"`
		Total              int      `json:"total"`
		Resolved           int      `json:"resolved"`
		AvgResolutionHours *float64 `json:"avgResolutionHours"`
	}
	type hotspot struct {
		Area           string    `json:"area"`
		Category       string    `json:"category"`
		Count          int       `json:"count"`
		Open           int       `json:"open"`
		LastReportedAt time.Time `json:"lastReportedAt"`
	}
	type workerStat struct {
		ID                 int64    `json:"id"`
		Name               string   `json:"name"`
		Department         string   `json:"department"`
		Active             int      `json:"active"`
		Resolved           int      `json:"resolved"`
		AvgResolutionHours *float64 `json:"avgResolutionHours"`
	}

	rows, err := h.DB.Query(ctx, `
		SELECT d.id, d.name,
			COUNT(i.id),
			COUNT(i.id) FILTER (WHERE i.status IN ('reported','assigned','in_progress')),
			COUNT(i.id) FILTER (WHERE i.status IN ('reported','assigned','in_progress') AND i.due_at < now()),
			COUNT(i.id) FILTER (WHERE i.status = 'resolved'),
			AVG(EXTRACT(EPOCH FROM (i.resolved_at - i.created_at)) / 3600) FILTER (WHERE i.status = 'resolved'),
			(SELECT COUNT(*) FROM users u WHERE u.department_id = d.id AND u.role = 'department' AND u.active),
			(SELECT COUNT(*) FROM users u WHERE u.department_id = d.id AND u.role = 'worker' AND u.active)
		FROM departments d LEFT JOIN issues i ON i.department_id = d.id
		GROUP BY d.id ORDER BY d.name`)
	if err != nil {
		writeErr(w, err, "analytics departments")
		return
	}
	depts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (deptStat, error) {
		var d deptStat
		return d, row.Scan(&d.ID, &d.Name, &d.Total, &d.Open, &d.Overdue, &d.Resolved, &d.AvgResolutionHours, &d.Officers, &d.Workers)
	})
	if err != nil {
		writeErr(w, err, "scan departments")
		return
	}

	rows, err = h.DB.Query(ctx, `
		SELECT c.name, COUNT(i.id), COUNT(i.id) FILTER (WHERE i.status = 'resolved'),
			AVG(EXTRACT(EPOCH FROM (i.resolved_at - i.created_at)) / 3600) FILTER (WHERE i.status = 'resolved')
		FROM categories c LEFT JOIN issues i ON i.category_id = c.id
		GROUP BY c.id ORDER BY COUNT(i.id) DESC, c.sort_order`)
	if err != nil {
		writeErr(w, err, "analytics categories")
		return
	}
	cats, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (catStat, error) {
		var c catStat
		return c, row.Scan(&c.Name, &c.Total, &c.Resolved, &c.AvgResolutionHours)
	})
	if err != nil {
		writeErr(w, err, "scan categories")
		return
	}

	// Recurring issues: the same category reported 2+ times in the same area
	// within the last 90 days.
	rows, err = h.DB.Query(ctx, `
		SELECT COALESCE(NULLIF(i.area, ''), i.address) AS place, c.name, COUNT(*),
			COUNT(*) FILTER (WHERE i.status IN ('reported','assigned','in_progress')), MAX(i.created_at)
		FROM issues i JOIN categories c ON c.id = i.category_id
		WHERE i.created_at > now() - interval '90 days' AND i.status <> 'rejected'
			AND COALESCE(NULLIF(i.area, ''), i.address) <> ''
		GROUP BY place, c.name HAVING COUNT(*) >= 2
		ORDER BY COUNT(*) DESC, MAX(i.created_at) DESC LIMIT 10`)
	if err != nil {
		writeErr(w, err, "analytics hotspots")
		return
	}
	spots, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (hotspot, error) {
		var s hotspot
		return s, row.Scan(&s.Area, &s.Category, &s.Count, &s.Open, &s.LastReportedAt)
	})
	if err != nil {
		writeErr(w, err, "scan hotspots")
		return
	}

	rows, err = h.DB.Query(ctx, `
		SELECT u.id, u.name, COALESCE(d.name, ''),
			COUNT(i.id) FILTER (WHERE i.status IN ('assigned','in_progress')),
			COUNT(i.id) FILTER (WHERE i.status = 'resolved'),
			AVG(EXTRACT(EPOCH FROM (i.resolved_at - i.assigned_at)) / 3600) FILTER (WHERE i.status = 'resolved')
		FROM users u
		LEFT JOIN departments d ON d.id = u.department_id
		LEFT JOIN issues i ON i.assigned_worker_id = u.id
		WHERE u.role = 'worker' AND u.active
		GROUP BY u.id, d.name ORDER BY COUNT(i.id) FILTER (WHERE i.status = 'resolved') DESC, u.name LIMIT 20`)
	if err != nil {
		writeErr(w, err, "analytics workers")
		return
	}
	workers, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (workerStat, error) {
		var s workerStat
		return s, row.Scan(&s.ID, &s.Name, &s.Department, &s.Active, &s.Resolved, &s.AvgResolutionHours)
	})
	if err != nil {
		writeErr(w, err, "scan workers")
		return
	}

	if spots == nil {
		spots = []hotspot{}
	}
	if workers == nil {
		workers = []workerStat{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"departments": depts,
		"categories":  cats,
		"hotspots":    spots,
		"workers":     workers,
	})
}
