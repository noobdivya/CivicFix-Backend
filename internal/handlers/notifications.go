package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/civicfix/backend/internal/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// querier is satisfied by both the pool and a transaction.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Notification is an in-app message for a staff member. Staff see them
// when they load a page or open the notification bell (plain REST).
type Notification struct {
	ID        int64      `json:"id"`
	IssueID   *int64     `json:"issueId"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	ReadAt    *time.Time `json:"readAt"`
	CreatedAt time.Time  `json:"createdAt"`
}

// createNotifications stores one notification per recipient, skipping the
// actor themself and duplicates.
func createNotifications(ctx context.Context, q querier, userIDs []int64, skip int64, issueID int64, title, body string) error {
	seen := map[int64]bool{skip: true}
	for _, uid := range userIDs {
		if seen[uid] {
			continue
		}
		seen[uid] = true
		if _, err := q.Exec(ctx,
			`INSERT INTO notifications (user_id, issue_id, title, body) VALUES ($1, $2, $3, $4)`,
			uid, issueID, title, body); err != nil {
			return err
		}
	}
	return nil
}

// officerIDs returns the active department officers of a department.
func officerIDs(ctx context.Context, q querier, departmentID *int) ([]int64, error) {
	if departmentID == nil {
		return nil, nil
	}
	rows, err := q.Query(ctx, `SELECT id FROM users WHERE role = 'department' AND department_id = $1 AND active`, *departmentID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// Notifications returns the latest notifications and the unread count.
func (h *Handler) Notifications(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	rows, err := h.DB.Query(r.Context(), `
		SELECT id, issue_id, title, body, read_at, created_at
		FROM notifications WHERE user_id = $1
		ORDER BY created_at DESC LIMIT 40`, u.ID)
	if err != nil {
		writeErr(w, err, "notifications")
		return
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Notification, error) {
		var n Notification
		err := row.Scan(&n.ID, &n.IssueID, &n.Title, &n.Body, &n.ReadAt, &n.CreatedAt)
		return n, err
	})
	if err != nil {
		writeErr(w, err, "scan notifications")
		return
	}
	var unread int
	if err := h.DB.QueryRow(r.Context(),
		`SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND read_at IS NULL`, u.ID).Scan(&unread); err != nil {
		writeErr(w, err, "unread count")
		return
	}
	if items == nil {
		items = []Notification{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "unread": unread})
}

// MarkNotificationsRead marks the given ids (or all) as read.
func (h *Handler) MarkNotificationsRead(w http.ResponseWriter, r *http.Request) {
	u := auth.FromContext(r.Context())
	var body struct {
		IDs []int64 `json:"ids"`
		All bool    `json:"all"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeErr(w, err, "mark read")
		return
	}
	var err error
	if body.All {
		_, err = h.DB.Exec(r.Context(), `UPDATE notifications SET read_at = now() WHERE user_id = $1 AND read_at IS NULL`, u.ID)
	} else if len(body.IDs) > 0 {
		_, err = h.DB.Exec(r.Context(),
			`UPDATE notifications SET read_at = now() WHERE user_id = $1 AND id = ANY($2) AND read_at IS NULL`, u.ID, body.IDs)
	}
	if err != nil {
		writeErr(w, err, "mark read")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
