package handlers

import (
	"log"
	"net/http"
)

type OfficialMessage struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Designation string `json:"designation"`
	Office      string `json:"office"`
	Message     string `json:"message"`
	IsSample    bool   `json:"isSample"`
}

// OfficialMessages returns the active messages from officials (MCD, MLA)
// shown on the landing page.
func (h *Handler) OfficialMessages(w http.ResponseWriter, r *http.Request) {
	rows, err := h.DB.Query(r.Context(), `
		SELECT id, name, designation, office, message, is_sample
		FROM official_messages
		WHERE active
		ORDER BY sort_order`)
	if err != nil {
		log.Printf("official messages: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load messages")
		return
	}
	defer rows.Close()

	msgs := []OfficialMessage{}
	for rows.Next() {
		var m OfficialMessage
		if err := rows.Scan(&m.ID, &m.Name, &m.Designation, &m.Office, &m.Message, &m.IsSample); err != nil {
			log.Printf("scan official message: %v", err)
			writeError(w, http.StatusInternalServerError, "could not load messages")
			return
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		log.Printf("official messages rows: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load messages")
		return
	}
	writeJSON(w, http.StatusOK, msgs)
}
