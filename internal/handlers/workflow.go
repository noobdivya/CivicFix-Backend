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
	"unicode/utf8"

	"github.com/civicfix/backend/internal/auth"
	"github.com/civicfix/backend/internal/media"
	"github.com/jackc/pgx/v5"
)

// Issue lifecycle:
//
//	reported ──review/assign──▶ assigned ──start──▶ in_progress ──resolve──▶ resolved
//	    │                          │                    │                        │
//	    └────────reject────────────┴────────────────────┘ ◀──────reopen──────────┘
//
// Department officers and admins review, prioritise, assign, reject,
// reopen and transfer. Field workers start work, add notes and resolve
// with a completion photo.

var priorities = []string{"low", "medium", "high", "critical"}

// slaFor is the target time to fix an issue of the given priority.
func slaFor(priority string) time.Duration {
	switch priority {
	case "critical":
		return 24 * time.Hour
	case "high":
		return 3 * 24 * time.Hour
	case "low":
		return 14 * 24 * time.Hour
	default:
		return 7 * 24 * time.Hour
	}
}

var openStatuses = []string{"reported", "assigned", "in_progress"}

// issueRef is the locked row an action works on.
type issueRef struct {
	ID           int64
	Title        string
	Code         *string
	Status       string
	Priority     string
	DepartmentID *int
	WorkerID     *int64
	ReviewedAt   *time.Time
}

// canAccess reports whether u may see/act on the issue at all.
func canAccess(u *auth.User, ref *issueRef) bool {
	switch u.Role {
	case auth.RoleAdmin:
		return true
	case auth.RoleDepartment:
		return u.DepartmentID != nil && ref.DepartmentID != nil && *u.DepartmentID == *ref.DepartmentID
	case auth.RoleWorker:
		return ref.WorkerID != nil && *ref.WorkerID == u.ID
	}
	return false
}

func isManager(u *auth.User) bool { return u.Role == auth.RoleAdmin || u.Role == auth.RoleDepartment }

// lockIssue loads and row-locks an issue the user is allowed to access.
func lockIssue(ctx context.Context, tx pgx.Tx, u *auth.User, id int64) (*issueRef, error) {
	var ref issueRef
	err := tx.QueryRow(ctx, `
		SELECT id, title, tracking_code, status, priority, department_id, assigned_worker_id, reviewed_at
		FROM issues WHERE id = $1 FOR UPDATE`, id,
	).Scan(&ref.ID, &ref.Title, &ref.Code, &ref.Status, &ref.Priority, &ref.DepartmentID, &ref.WorkerID, &ref.ReviewedAt)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !canAccess(u, &ref)) {
		return nil, notFound("issue not found")
	}
	return &ref, err
}

func addEvent(ctx context.Context, tx pgx.Tx, u *auth.User, issueID int64, typ, from, to, message string, public bool) error {
	var fromPtr, toPtr *string
	if from != "" {
		fromPtr = &from
	}
	if to != "" {
		toPtr = &to
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO issue_events (issue_id, type, from_status, to_status, message, is_public, actor_id, actor_name, actor_role)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		issueID, typ, fromPtr, toPtr, message, public, u.ID, u.Name, u.Role)
	return err
}

func (ref *issueRef) label() string {
	if ref.Code != nil {
		return *ref.Code + ": " + ref.Title
	}
	return ref.Title
}

func issueID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, notFound("issue not found")
	}
	return id, nil
}

// runAction runs fn inside a transaction on a locked issue and responds
// with the updated issue detail.
// It reports whether the transaction was committed.
type actionFunc func(ctx context.Context, tx pgx.Tx, u *auth.User, ref *issueRef) error

func (h *Handler) runAction(w http.ResponseWriter, r *http.Request, name string, fn actionFunc) (committed bool) {
	ctx := r.Context()
	u := auth.FromContext(ctx)
	id, err := issueID(r)
	if err != nil {
		writeErr(w, err, name)
		return false
	}

	tx, err := h.DB.Begin(ctx)
	if err != nil {
		writeErr(w, err, name)
		return false
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	ref, err := lockIssue(ctx, tx, u, id)
	if err != nil {
		writeErr(w, err, name)
		return false
	}
	if err := fn(ctx, tx, u, ref); err != nil {
		writeErr(w, err, name)
		return false
	}
	if _, err := tx.Exec(ctx, `UPDATE issues SET updated_at = now() WHERE id = $1`, id); err != nil {
		writeErr(w, err, name)
		return false
	}
	if err := tx.Commit(ctx); err != nil {
		writeErr(w, err, name)
		return false
	}
	committed = true

	detail, err := h.loadIssueDetail(ctx, u, id)
	if err != nil {
		writeErr(w, err, name+" reload")
		return true
	}
	writeJSON(w, http.StatusOK, detail)
	return true
}

func requireStatus(ref *issueRef, allowed ...string) error {
	if !slices.Contains(allowed, ref.Status) {
		return conflict(fmt.Sprintf("this can't be done while the issue is %q", strings.ReplaceAll(ref.Status, "_", " ")))
	}
	return nil
}

func requireManager(u *auth.User) error {
	if !isManager(u) {
		return forbidden("only department officers and admins can do this")
	}
	return nil
}

func textField(value string, min, max int, field, msg string) error {
	n := utf8.RuneCountInString(strings.TrimSpace(value))
	if n < min || n > max {
		return invalid(map[string]string{field: msg})
	}
	return nil
}

// assignWorker assigns ref to workerID (who must be an active worker of the
// issue's department) and notifies them.
func assignWorker(ctx context.Context, tx pgx.Tx, u *auth.User, ref *issueRef, workerID int64) error {
	var workerName string
	err := tx.QueryRow(ctx, `
		SELECT name FROM users
		WHERE id = $1 AND role = 'worker' AND active AND department_id IS NOT DISTINCT FROM $2`,
		workerID, ref.DepartmentID).Scan(&workerName)
	if errors.Is(err, pgx.ErrNoRows) {
		return invalid(map[string]string{"workerId": "Choose an active field worker from this department."})
	}
	if err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE issues SET
			status = 'assigned', assigned_worker_id = $2, assigned_at = now(),
			reviewed_at = COALESCE(reviewed_at, now()),
			due_at = COALESCE(due_at, now() + $3::interval)
		WHERE id = $1`, ref.ID, workerID, fmt.Sprintf("%d seconds", int(slaFor(ref.Priority).Seconds()))); err != nil {
		return err
	}
	if err := addEvent(ctx, tx, u, ref.ID, "assigned", ref.Status, "assigned",
		"Assigned to a field worker.", true); err != nil {
		return err
	}
	if err := addEvent(ctx, tx, u, ref.ID, "assigned_detail", "", "",
		"Assigned to "+workerName+".", false); err != nil {
		return err
	}

	if err := createNotifications(ctx, tx, []int64{workerID}, u.ID, ref.ID,
		"New task assigned", ref.label()); err != nil {
		return err
	}
	if ref.WorkerID != nil && *ref.WorkerID != workerID {
		if err := createNotifications(ctx, tx, []int64{*ref.WorkerID}, u.ID, ref.ID,
			"Task reassigned to another worker", ref.label()); err != nil {
			return err
		}
	}
	ref.Status = "assigned"
	ref.WorkerID = &workerID
	return nil
}

// ReviewIssue sets the priority (and deadline) of a new issue and
// optionally assigns it straight away.
func (h *Handler) ReviewIssue(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Priority string `json:"priority"`
		WorkerID *int64 `json:"workerId"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeErr(w, err, "review")
		return
	}
	h.runAction(w, r, "review", func(ctx context.Context, tx pgx.Tx, u *auth.User, ref *issueRef) error {
		if err := requireManager(u); err != nil {
			return err
		}
		if err := requireStatus(ref, "reported"); err != nil {
			return err
		}
		if !slices.Contains(priorities, body.Priority) {
			return invalid(map[string]string{"priority": "Choose a priority."})
		}
		if _, err := tx.Exec(ctx, `
			UPDATE issues SET priority = $2, reviewed_at = now(), due_at = now() + $3::interval WHERE id = $1`,
			ref.ID, body.Priority, fmt.Sprintf("%d seconds", int(slaFor(body.Priority).Seconds()))); err != nil {
			return err
		}
		ref.Priority = body.Priority
		dept := "the department"
		if u.DepartmentName != nil {
			dept = "the " + *u.DepartmentName + " department"
		}
		if err := addEvent(ctx, tx, u, ref.ID, "reviewed", "", "",
			fmt.Sprintf("Complaint reviewed by %s. Priority: %s.", dept, body.Priority), true); err != nil {
			return err
		}
		if body.WorkerID != nil {
			return assignWorker(ctx, tx, u, ref, *body.WorkerID)
		}
		return nil
	})
}

// AssignIssue assigns (or reassigns) an open issue to a field worker.
func (h *Handler) AssignIssue(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WorkerID int64 `json:"workerId"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeErr(w, err, "assign")
		return
	}
	h.runAction(w, r, "assign", func(ctx context.Context, tx pgx.Tx, u *auth.User, ref *issueRef) error {
		if err := requireManager(u); err != nil {
			return err
		}
		if err := requireStatus(ref, openStatuses...); err != nil {
			return err
		}
		return assignWorker(ctx, tx, u, ref, body.WorkerID)
	})
}

// RejectIssue closes an issue without action (duplicate, invalid, not civic…).
func (h *Handler) RejectIssue(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeErr(w, err, "reject")
		return
	}
	h.runAction(w, r, "reject", func(ctx context.Context, tx pgx.Tx, u *auth.User, ref *issueRef) error {
		if err := requireManager(u); err != nil {
			return err
		}
		if err := requireStatus(ref, openStatuses...); err != nil {
			return err
		}
		if err := textField(body.Reason, 5, 500, "reason", "Give a reason (5–500 characters)."); err != nil {
			return err
		}
		reason := strings.TrimSpace(body.Reason)
		if _, err := tx.Exec(ctx, `UPDATE issues SET status = 'rejected', rejection_reason = $2 WHERE id = $1`, ref.ID, reason); err != nil {
			return err
		}
		if err := addEvent(ctx, tx, u, ref.ID, "rejected", ref.Status, "rejected", "Complaint closed: "+reason, true); err != nil {
			return err
		}
		if ref.WorkerID != nil {
			return createNotifications(ctx, tx, []int64{*ref.WorkerID}, u.ID, ref.ID, "Task cancelled", ref.label())
		}
		return nil
	})
}

// StartIssue marks an assigned issue as being worked on.
func (h *Handler) StartIssue(w http.ResponseWriter, r *http.Request) {
	h.runAction(w, r, "start", func(ctx context.Context, tx pgx.Tx, u *auth.User, ref *issueRef) error {
		if err := requireStatus(ref, "assigned"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE issues SET status = 'in_progress', started_at = now() WHERE id = $1`, ref.ID); err != nil {
			return err
		}
		if err := addEvent(ctx, tx, u, ref.ID, "started", ref.Status, "in_progress", "Work has started on site.", true); err != nil {
			return err
		}
		officers, err := officerIDs(ctx, tx, ref.DepartmentID)
		if err != nil {
			return err
		}
		return createNotifications(ctx, tx, officers, u.ID, ref.ID, "Work started", ref.label())
	})
}

// AddNote adds a progress note; public notes are shown to the citizen.
func (h *Handler) AddNote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Message string `json:"message"`
		Public  bool   `json:"public"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeErr(w, err, "note")
		return
	}
	h.runAction(w, r, "note", func(ctx context.Context, tx pgx.Tx, u *auth.User, ref *issueRef) error {
		if err := textField(body.Message, 2, 1000, "message", "Write a note (2–1000 characters)."); err != nil {
			return err
		}
		if err := addEvent(ctx, tx, u, ref.ID, "note", "", "", strings.TrimSpace(body.Message), body.Public); err != nil {
			return err
		}
		// Workers' notes go to the officers; officers' notes go to the worker.
		var recipients []int64
		if u.Role == auth.RoleWorker {
			ids, err := officerIDs(ctx, tx, ref.DepartmentID)
			if err != nil {
				return err
			}
			recipients = ids
		} else if ref.WorkerID != nil {
			recipients = []int64{*ref.WorkerID}
		}
		return createNotifications(ctx, tx, recipients, u.ID, ref.ID, "New note from "+u.Name, truncate(body.Message, 140))
	})
}

// ResolveIssue closes an issue as fixed. Requires a completion photo
// (multipart field "photo") and accepts an optional "note".
func (h *Handler) ResolveIssue(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	if err := r.ParseMultipartForm(maxRequestBytes); err != nil {
		writeErr(w, invalid(map[string]string{"photo": "Upload a completion photo (max 10 MB)."}), "resolve")
		return
	}
	defer r.MultipartForm.RemoveAll()
	note := strings.TrimSpace(r.FormValue("note"))
	if utf8.RuneCountInString(note) > 1000 {
		writeErr(w, invalid(map[string]string{"note": "Keep the note under 1000 characters."}), "resolve")
		return
	}
	file, _, err := r.FormFile("photo")
	if err != nil {
		writeErr(w, invalid(map[string]string{"photo": "Upload a photo showing the completed work."}), "resolve")
		return
	}
	defer file.Close()

	var saved *media.Saved
	committed := h.runAction(w, r, "resolve", func(ctx context.Context, tx pgx.Tx, u *auth.User, ref *issueRef) error {
		if err := requireStatus(ref, "assigned", "in_progress"); err != nil {
			return err
		}
		s, err := media.SaveImage(file, h.Config.UploadDir, "completion")
		if err != nil {
			if errors.Is(err, media.ErrNotImage) {
				return invalid(map[string]string{"photo": "Upload a JPEG, PNG or WebP photo."})
			}
			return err
		}
		saved = s
		if _, err := tx.Exec(ctx, `
			UPDATE issues SET status = 'resolved', resolved_at = now(), started_at = COALESCE(started_at, now())
			WHERE id = $1`, ref.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO issue_photos (issue_id, kind, file_path, content_type, size_bytes, width, height)
			VALUES ($1, 'completion', $2, $3, $4, $5, $6)`,
			ref.ID, s.RelPath, s.ContentType, s.Size, s.Width, s.Height); err != nil {
			return err
		}
		msg := "Issue resolved. Completion photo uploaded."
		if note != "" {
			msg = "Issue resolved: " + note
		}
		if err := addEvent(ctx, tx, u, ref.ID, "resolved", ref.Status, "resolved", msg, true); err != nil {
			return err
		}
		recipients, err := officerIDs(ctx, tx, ref.DepartmentID)
		if err != nil {
			return err
		}
		if ref.WorkerID != nil {
			recipients = append(recipients, *ref.WorkerID)
		}
		return createNotifications(ctx, tx, recipients, u.ID, ref.ID, "Issue resolved", ref.label())
	})
	// If the photo was written but the transaction didn't commit, remove it.
	if saved != nil && !committed {
		media.Remove(h.Config.UploadDir, saved.RelPath)
	}
}

// ReopenIssue sends a resolved or rejected issue back for more work.
func (h *Handler) ReopenIssue(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeErr(w, err, "reopen")
		return
	}
	h.runAction(w, r, "reopen", func(ctx context.Context, tx pgx.Tx, u *auth.User, ref *issueRef) error {
		if err := requireManager(u); err != nil {
			return err
		}
		if err := requireStatus(ref, "resolved", "rejected"); err != nil {
			return err
		}
		if err := textField(body.Reason, 5, 500, "reason", "Give a reason (5–500 characters)."); err != nil {
			return err
		}
		next := "reported"
		if ref.WorkerID != nil {
			next = "assigned"
		}
		if _, err := tx.Exec(ctx, `
			UPDATE issues SET status = $2, resolved_at = NULL, rejection_reason = NULL,
				started_at = NULL, assigned_at = CASE WHEN $2 = 'assigned' THEN now() ELSE assigned_at END
			WHERE id = $1`, ref.ID, next); err != nil {
			return err
		}
		if err := addEvent(ctx, tx, u, ref.ID, "reopened", ref.Status, next,
			"Complaint reopened: "+strings.TrimSpace(body.Reason), true); err != nil {
			return err
		}
		if ref.WorkerID != nil {
			return createNotifications(ctx, tx, []int64{*ref.WorkerID}, u.ID, ref.ID, "Task reopened", ref.label())
		}
		return nil
	})
}

// SetPriority changes an open issue's priority and recalculates its deadline.
func (h *Handler) SetPriority(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Priority string `json:"priority"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeErr(w, err, "priority")
		return
	}
	h.runAction(w, r, "priority", func(ctx context.Context, tx pgx.Tx, u *auth.User, ref *issueRef) error {
		if err := requireManager(u); err != nil {
			return err
		}
		if err := requireStatus(ref, openStatuses...); err != nil {
			return err
		}
		if !slices.Contains(priorities, body.Priority) {
			return invalid(map[string]string{"priority": "Choose a priority."})
		}
		if body.Priority == ref.Priority {
			return nil
		}
		if _, err := tx.Exec(ctx, `
			UPDATE issues SET priority = $2, due_at = COALESCE(reviewed_at, now()) + $3::interval WHERE id = $1`,
			ref.ID, body.Priority, fmt.Sprintf("%d seconds", int(slaFor(body.Priority).Seconds()))); err != nil {
			return err
		}
		if err := addEvent(ctx, tx, u, ref.ID, "priority", "", "",
			fmt.Sprintf("Priority changed from %s to %s.", ref.Priority, body.Priority), false); err != nil {
			return err
		}
		if ref.WorkerID != nil {
			return createNotifications(ctx, tx, []int64{*ref.WorkerID}, u.ID, ref.ID,
				"Priority changed to "+body.Priority, ref.label())
		}
		return nil
	})
}

// TransferIssue moves an open issue to another department.
func (h *Handler) TransferIssue(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DepartmentID int `json:"departmentId"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeErr(w, err, "transfer")
		return
	}
	h.runAction(w, r, "transfer", func(ctx context.Context, tx pgx.Tx, u *auth.User, ref *issueRef) error {
		if err := requireManager(u); err != nil {
			return err
		}
		if err := requireStatus(ref, openStatuses...); err != nil {
			return err
		}
		if ref.DepartmentID != nil && *ref.DepartmentID == body.DepartmentID {
			return invalid(map[string]string{"departmentId": "The issue is already with this department."})
		}
		var deptName string
		if err := tx.QueryRow(ctx, `SELECT name FROM departments WHERE id = $1`, body.DepartmentID).Scan(&deptName); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return invalid(map[string]string{"departmentId": "Choose a department."})
			}
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE issues SET department_id = $2, status = 'reported', assigned_worker_id = NULL,
				reviewed_at = NULL, assigned_at = NULL, started_at = NULL, due_at = NULL
			WHERE id = $1`, ref.ID, body.DepartmentID); err != nil {
			return err
		}
		if err := addEvent(ctx, tx, u, ref.ID, "transferred", ref.Status, "reported",
			"Transferred to the "+deptName+" department.", true); err != nil {
			return err
		}
		newDept := body.DepartmentID
		recipients, err := officerIDs(ctx, tx, &newDept)
		if err != nil {
			return err
		}
		if err := createNotifications(ctx, tx, recipients, u.ID, ref.ID, "Issue transferred to your department", ref.label()); err != nil {
			return err
		}
		if ref.WorkerID != nil {
			return createNotifications(ctx, tx, []int64{*ref.WorkerID}, u.ID, ref.ID, "Task moved to another department", ref.label())
		}
		return nil
	})
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
