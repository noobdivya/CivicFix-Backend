package handlers

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/civicfix/backend/internal/validate"
	"github.com/jackc/pgx/v5"
)

var trackingCodeRe = regexp.MustCompile(`^CF-[A-Z0-9]{8}$`)

type TrackedIssue struct {
	TrackingCode    string     `json:"trackingCode"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	Category        string     `json:"category"`
	CategoryIcon    string     `json:"categoryIcon"`
	Status          string     `json:"status"`
	Priority        *string    `json:"priority"` // only once reviewed
	Department      *string    `json:"department"`
	Address         string     `json:"address"`
	Lat             *float64   `json:"lat"`
	Lng             *float64   `json:"lng"`
	ReporterName    string     `json:"reporterName"`
	CreatedAt       time.Time  `json:"createdAt"`
	ReviewedAt      *time.Time `json:"reviewedAt"`
	AssignedAt      *time.Time `json:"assignedAt"`
	StartedAt       *time.Time `json:"startedAt"`
	ResolvedAt      *time.Time `json:"resolvedAt"`
	DueAt           *time.Time `json:"dueAt"`
	ResolutionHours *float64   `json:"resolutionHours"`
	RejectionReason *string    `json:"rejectionReason"`
	Photos          []Photo    `json:"photos"`
	Timeline        []Event    `json:"timeline"`
}

// normaliseCode accepts "cf-abcd2345", "ABCD2345", " CF ABCD2345 ".
func normaliseCode(s string) string {
	s = strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(s)))
	s = strings.TrimPrefix(s, "CF")
	return "CF-" + s
}

// Track lets a citizen look up their complaint with its tracking code and
// the mobile number they reported with. Both must match, so tracking codes
// alone can't be used to read other people's complaints.
func (h *Handler) Track(w http.ResponseWriter, r *http.Request) {
	code := normaliseCode(r.URL.Query().Get("code"))
	phone, okPhone := validate.IndianMobile(r.URL.Query().Get("phone"))
	if !trackingCodeRe.MatchString(code) || !okPhone {
		writeErr(w, invalid(map[string]string{"code": "Enter your tracking ID (e.g. CF-7KQ2M9XA) and the mobile number you reported with."}), "track")
		return
	}

	ctx := r.Context()
	var t TrackedIssue
	var id int64
	var reviewedPriority string
	var reporter *string
	err := h.DB.QueryRow(ctx, `
		SELECT i.id, i.tracking_code, i.title, i.description, c.name, c.icon, i.status, i.priority, d.name,
			i.address, i.latitude, i.longitude, i.reporter_name, i.created_at, i.reviewed_at, i.assigned_at,
			i.started_at, i.resolved_at, i.due_at,
			CASE WHEN i.status = 'resolved' THEN EXTRACT(EPOCH FROM (i.resolved_at - i.created_at)) / 3600 END,
			i.rejection_reason
		FROM issues i
		JOIN categories c ON c.id = i.category_id
		LEFT JOIN departments d ON d.id = i.department_id
		WHERE i.tracking_code = $1 AND i.reporter_phone = $2`, code, phone,
	).Scan(&id, &t.TrackingCode, &t.Title, &t.Description, &t.Category, &t.CategoryIcon, &t.Status, &reviewedPriority, &t.Department,
		&t.Address, &t.Lat, &t.Lng, &reporter, &t.CreatedAt, &t.ReviewedAt, &t.AssignedAt,
		&t.StartedAt, &t.ResolvedAt, &t.DueAt, &t.ResolutionHours, &t.RejectionReason)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "no complaint found with this tracking ID and mobile number")
		return
	}
	if err != nil {
		writeErr(w, err, "track")
		return
	}
	if t.ReviewedAt != nil {
		t.Priority = &reviewedPriority
	}
	if reporter != nil {
		t.ReporterName = *reporter
	}

	rows, err := h.DB.Query(ctx,
		`SELECT '/uploads/' || file_path, kind, created_at FROM issue_photos WHERE issue_id = $1 ORDER BY id`, id)
	if err != nil {
		writeErr(w, err, "track photos")
		return
	}
	t.Photos, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Photo, error) {
		var p Photo
		return p, row.Scan(&p.URL, &p.Kind, &p.CreatedAt)
	})
	if err != nil {
		writeErr(w, err, "track photos")
		return
	}

	// Only public events, and never staff names (just their role).
	rows, err = h.DB.Query(ctx, `
		SELECT id, type, from_status, to_status, message, TRUE, '', actor_role, created_at
		FROM issue_events WHERE issue_id = $1 AND is_public ORDER BY created_at, id`, id)
	if err != nil {
		writeErr(w, err, "track timeline")
		return
	}
	t.Timeline, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Event, error) {
		var e Event
		return e, row.Scan(&e.ID, &e.Type, &e.FromStatus, &e.ToStatus, &e.Message, &e.IsPublic, &e.ActorName, &e.ActorRole, &e.CreatedAt)
	})
	if err != nil {
		writeErr(w, err, "track timeline")
		return
	}
	writeJSON(w, http.StatusOK, t)
}
