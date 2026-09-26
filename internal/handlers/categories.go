package handlers

import (
	"log"
	"net/http"
)

type Category struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
}

// Categories lists the issue categories a citizen can report under.
func (h *Handler) Categories(w http.ResponseWriter, r *http.Request) {
	rows, err := h.DB.Query(r.Context(),
		`SELECT slug, name, description, icon FROM categories ORDER BY sort_order, name`)
	if err != nil {
		log.Printf("categories: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load categories")
		return
	}
	defer rows.Close()

	cats := []Category{}
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.Slug, &c.Name, &c.Description, &c.Icon); err != nil {
			log.Printf("scan category: %v", err)
			writeError(w, http.StatusInternalServerError, "could not load categories")
			return
		}
		cats = append(cats, c)
	}
	if err := rows.Err(); err != nil {
		log.Printf("categories rows: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load categories")
		return
	}
	writeJSON(w, http.StatusOK, cats)
}
