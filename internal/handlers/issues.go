package handlers

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/big"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/civicfix/backend/internal/media"
	"github.com/civicfix/backend/internal/validate"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	maxPhotoBytes   = 10 << 20 // 10 MB per photo
	maxReportPhotos = 2        // photos per citizen report
	maxRequestBytes = maxReportPhotos*maxPhotoBytes + 2<<20
)

type issueForm struct {
	category    string
	name        string
	phone       string // normalised 10 digits
	aadhaar     string // normalised 12 digits — never stored or logged
	description string
	address     string
	area        string
	lat, lng    float64
	consent     bool
}

type CreatedIssue struct {
	ID           int64     `json:"id"`
	TrackingCode string    `json:"trackingCode"`
	Status       string    `json:"status"`
	Category     string    `json:"category"`
	Title        string    `json:"title"`
	Address      string    `json:"address"`
	PhotoURLs    []string  `json:"photoUrls"`
	CreatedAt    time.Time `json:"createdAt"`
}

// parseIssueForm reads and validates the text fields of the report form.
func parseIssueForm(r *http.Request) (issueForm, map[string]string) {
	v := func(k string) string { return strings.TrimSpace(r.FormValue(k)) }
	f := issueForm{
		category:    v("category"),
		name:        v("name"),
		description: v("description"),
		address:     v("address"),
		area:        v("area"),
		consent:     v("consent") == "true",
	}
	errs := map[string]string{}

	if f.category == "" {
		errs["category"] = "Choose a category."
	}
	if n := utf8.RuneCountInString(f.name); n < 2 || n > 100 {
		errs["name"] = "Name must be between 2 and 100 characters."
	}
	if p, ok := validate.IndianMobile(v("phone")); ok {
		f.phone = p
	} else {
		errs["phone"] = "Enter a valid 10-digit Indian mobile number."
	}
	if a, ok := validate.Aadhaar(v("aadhaar")); ok {
		f.aadhaar = a
	} else {
		errs["aadhaar"] = "Enter a valid 12-digit Aadhaar number."
	}
	if n := utf8.RuneCountInString(f.description); n < 20 || n > 2000 {
		errs["description"] = "Describe the issue in 20 to 2000 characters."
	}
	if n := utf8.RuneCountInString(f.address); n < 3 || n > 300 {
		errs["address"] = "Enter the location / address (3 to 300 characters)."
	}
	if utf8.RuneCountInString(f.area) > 120 {
		f.area = string([]rune(f.area)[:120])
	}

	lat, errLat := strconv.ParseFloat(v("lat"), 64)
	lng, errLng := strconv.ParseFloat(v("lng"), 64)
	if errLat != nil || errLng != nil || lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		errs["location"] = "Pin the issue's location on the map."
	} else {
		f.lat, f.lng = lat, lng
	}
	if !f.consent {
		errs["consent"] = "Please confirm the declaration to submit."
	}
	return f, errs
}

// CreateIssue handles the citizen "Report an issue" form
// (multipart/form-data with 1–2 photos in the "photo" field).
func (h *Handler) CreateIssue(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	if err := r.ParseMultipartForm(maxRequestBytes); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeFieldErrors(w, map[string]string{"photo": "The photos are too large (max 10 MB each)."})
			return
		}
		writeError(w, http.StatusBadRequest, "invalid form data")
		return
	}
	defer r.MultipartForm.RemoveAll()

	f, errs := parseIssueForm(r)

	files := r.MultipartForm.File["photo"]
	switch {
	case len(files) == 0:
		errs["photo"] = "Add a photo of the issue."
	case len(files) > maxReportPhotos:
		errs["photo"] = fmt.Sprintf("You can upload at most %d photos.", maxReportPhotos)
	default:
		for _, fh := range files {
			if fh.Size > maxPhotoBytes {
				errs["photo"] = "Each photo must be 10 MB or smaller."
			}
		}
	}

	ctx := r.Context()
	var categoryID int
	var categoryName string
	var departmentID *int
	if f.category != "" {
		err := h.DB.QueryRow(ctx, `SELECT id, name, department_id FROM categories WHERE slug = $1`, f.category).
			Scan(&categoryID, &categoryName, &departmentID)
		if errors.Is(err, pgx.ErrNoRows) {
			errs["category"] = "Unknown category."
		} else if err != nil {
			log.Printf("lookup category: %v", err)
			writeError(w, http.StatusInternalServerError, "could not submit your report, please try again")
			return
		}
	}

	if len(errs) > 0 {
		writeFieldErrors(w, errs)
		return
	}

	var saved []*media.Saved
	removeSaved := func() {
		for _, s := range saved {
			media.Remove(h.Config.UploadDir, s.RelPath)
		}
	}
	for _, fh := range files {
		s, err := saveUpload(fh, h.Config.UploadDir)
		if err != nil {
			removeSaved()
			if errors.Is(err, media.ErrNotImage) {
				writeFieldErrors(w, map[string]string{"photo": "Upload JPEG, PNG or WebP photos only."})
			} else {
				log.Printf("save photo: %v", err)
				writeFieldErrors(w, map[string]string{"photo": "A photo could not be processed. Try another one."})
			}
			return
		}
		saved = append(saved, s)
	}

	created, err := h.insertIssue(r, f, categoryID, departmentID, saved)
	if err != nil {
		removeSaved()
		log.Printf("insert issue: %v", err)
		writeError(w, http.StatusInternalServerError, "could not submit your report, please try again")
		return
	}
	created.Category = categoryName
	writeJSON(w, http.StatusCreated, created)
}

func saveUpload(fh *multipart.FileHeader, uploadDir string) (*media.Saved, error) {
	file, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return media.SaveImage(file, uploadDir, "issues")
}

// insertIssue stores the issue, its photos, its first timeline event and a
// notification for the department's officers in one transaction. It retries with
// a new tracking code on the (very unlikely) chance of a collision.
func (h *Handler) insertIssue(r *http.Request, f issueForm, categoryID int, departmentID *int, photos []*media.Saved) (*CreatedIssue, error) {
	ctx := r.Context()
	title := makeTitle(f.description)
	aadhaarLast4 := f.aadhaar[len(f.aadhaar)-4:]
	aadhaarHash := h.aadhaarHash(f.aadhaar)

	for attempt := 0; attempt < 5; attempt++ {
		code, err := trackingCode()
		if err != nil {
			return nil, err
		}

		tx, err := h.DB.Begin(ctx)
		if err != nil {
			return nil, err
		}
		c := &CreatedIssue{TrackingCode: code, Title: title, Address: f.address, Status: "reported"}
		err = tx.QueryRow(ctx, `
			INSERT INTO issues (category_id, department_id, title, description, status, address, area, latitude, longitude,
				tracking_code, reporter_name, reporter_phone, reporter_aadhaar_last4, reporter_aadhaar_hash)
			VALUES ($1, $2, $3, $4, 'reported', $5, $6, $7, $8, $9, $10, $11, $12, $13)
			RETURNING id, created_at`,
			categoryID, departmentID, title, f.description, f.address, f.area, f.lat, f.lng,
			code, f.name, f.phone, aadhaarLast4, aadhaarHash,
		).Scan(&c.ID, &c.CreatedAt)
		if err != nil {
			_ = tx.Rollback(ctx)
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation on tracking_code
				continue
			}
			return nil, err
		}

		c.PhotoURLs = []string{}
		for _, photo := range photos {
			if _, err := tx.Exec(ctx, `
				INSERT INTO issue_photos (issue_id, kind, file_path, content_type, size_bytes, width, height)
				VALUES ($1, 'report', $2, $3, $4, $5, $6)`,
				c.ID, photo.RelPath, photo.ContentType, photo.Size, photo.Width, photo.Height,
			); err != nil {
				_ = tx.Rollback(ctx)
				return nil, err
			}
			c.PhotoURLs = append(c.PhotoURLs, "/uploads/"+photo.RelPath)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO issue_events (issue_id, type, to_status, message, actor_name, actor_role)
			VALUES ($1, 'reported', 'reported', 'Complaint registered.', $2, 'citizen')`, c.ID, f.name); err != nil {
			_ = tx.Rollback(ctx)
			return nil, err
		}
		officers, err := officerIDs(ctx, tx, departmentID)
		if err != nil {
			_ = tx.Rollback(ctx)
			return nil, err
		}
		if err := createNotifications(ctx, tx, officers, 0, c.ID, "New complaint: "+code, title); err != nil {
			_ = tx.Rollback(ctx)
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return c, nil
	}
	return nil, errors.New("could not generate a unique tracking code")
}

// aadhaarHash is a keyed one-way hash: it lets us recognise the same
// Aadhaar number again without being able to recover it.
func (h *Handler) aadhaarHash(aadhaar string) string {
	m := hmac.New(sha256.New, []byte(h.Config.AadhaarHashKey))
	m.Write([]byte(aadhaar))
	return hex.EncodeToString(m.Sum(nil))
}

// makeTitle derives a short title from the first line of the description.
func makeTitle(description string) string {
	t := strings.TrimSpace(strings.SplitN(description, "\n", 2)[0])
	if r := []rune(t); len(r) > 80 {
		cut := string(r[:80])
		if i := strings.LastIndex(cut, " "); i > 40 {
			cut = cut[:i]
		}
		t = cut + "…"
	}
	return t
}

// trackingCode returns a code like "CF-7KQ2M9XA" (no 0/O/1/I to avoid confusion).
func trackingCode() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b[i] = alphabet[n.Int64()]
	}
	return "CF-" + string(b), nil
}

func writeFieldErrors(w http.ResponseWriter, fields map[string]string) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error":  "please fix the highlighted fields",
		"fields": fields,
	})
}
