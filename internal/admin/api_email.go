package admin

import (
	"net/http"
	"time"
)

// listEmailTokens lists pending/consumed email verification and password-reset
// tokens. The raw token value is never exposed — only its purpose, target
// email, expiry, and usage state.
func (h *Handler) listEmailTokens(w http.ResponseWriter, r *http.Request) {
	rows, err := h.store.DB.QueryContext(r.Context(),
		"SELECT did, purpose, email, expires_at, used_at FROM email_tokens ORDER BY expires_at DESC")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = rows.Close() }()

	var tokens = make([]map[string]any, 0)
	for rows.Next() {
		var did, purpose, email, expiresAt string
		var usedAt *string
		if err := rows.Scan(&did, &purpose, &email, &expiresAt, &usedAt); err != nil {
			continue
		}
		status := "pending"
		if usedAt != nil {
			status = "used"
		} else if exp, err := time.Parse(time.RFC3339, expiresAt); err != nil || time.Now().After(exp) {
			status = "expired"
		}
		tokens = append(tokens, map[string]any{
			"did":       did,
			"purpose":   purpose,
			"email":     email,
			"expiresAt": expiresAt,
			"usedAt":    usedAt,
			"status":    status,
		})
	}
	writeJSON(w, map[string]any{"tokens": tokens})
}
