package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"net/mail"
	"strings"
	"unicode/utf8"
)

type contactRequest struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Subject string `json:"subject"`
	Message string `json:"message"`
}

// validate trims the fields and returns a map of field -> error message.
func (c *contactRequest) validate() map[string]string {
	c.Name = strings.TrimSpace(c.Name)
	c.Email = strings.TrimSpace(c.Email)
	c.Subject = strings.TrimSpace(c.Subject)
	c.Message = strings.TrimSpace(c.Message)

	errs := map[string]string{}
	if n := utf8.RuneCountInString(c.Name); n < 2 || n > 100 {
		errs["name"] = "Name must be between 2 and 100 characters."
	}
	if addr, err := mail.ParseAddress(c.Email); err != nil || addr.Address != c.Email || len(c.Email) > 254 {
		errs["email"] = "Enter a valid email address."
	}
	if utf8.RuneCountInString(c.Subject) > 150 {
		errs["subject"] = "Subject must be 150 characters or fewer."
	}
	if n := utf8.RuneCountInString(c.Message); n < 10 || n > 2000 {
		errs["message"] = "Message must be between 10 and 2000 characters."
	}
	return errs
}

// Contact stores a message submitted through the landing page contact form.
func (h *Handler) Contact(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)

	var req contactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if errs := req.validate(); len(errs) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":  "please fix the highlighted fields",
			"fields": errs,
		})
		return
	}

	var id int64
	err := h.DB.QueryRow(r.Context(),
		`INSERT INTO contact_messages (name, email, subject, message) VALUES ($1, $2, $3, $4) RETURNING id`,
		req.Name, req.Email, req.Subject, req.Message,
	).Scan(&id)
	if err != nil {
		log.Printf("insert contact message: %v", err)
		writeError(w, http.StatusInternalServerError, "could not send your message, please try again")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "message": "Thank you! We have received your message."})
}
