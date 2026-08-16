package admin

import (
	"net/http"
	"strconv"
)

// listBlobs lists all blobs across accounts, newest first.
func (h *Handler) listBlobs(w http.ResponseWriter, r *http.Request) {
	writeBlobs(w, r, h, "")
}

// writeBlobs is the shared blob-list implementation; an empty did means
// "all accounts".
func writeBlobs(w http.ResponseWriter, r *http.Request, h *Handler, did string) {
	query := "SELECT cid, did, size, mime_type, created_at FROM blobs"
	args := []any{}

	if did != "" {
		query += " WHERE did = ?"
		args = append(args, did)
	}
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		if did != "" {
			query += " AND created_at < ?"
		} else {
			query += " WHERE created_at < ?"
		}
		args = append(args, cursor)
	}

	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= 500 {
			limit = n
		}
	}

	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit+1)

	rows, err := h.store.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = rows.Close() }()

	var blobs = make([]map[string]any, 0)
	var lastCreated string
	for rows.Next() {
		var cidBytes []byte
		var owner string
		var size int64
		var mimeType, createdAt *string
		if err := rows.Scan(&cidBytes, &owner, &size, &mimeType, &createdAt); err != nil {
			continue
		}
		blobs = append(blobs, map[string]any{
			"cid":       cidString(cidBytes),
			"did":       owner,
			"size":      size,
			"mimeType":  mimeType,
			"createdAt": createdAt,
		})
		if createdAt != nil {
			lastCreated = *createdAt
		}
	}

	var next *string
	if len(blobs) > limit {
		blobs = blobs[:limit]
		next = &lastCreated
	}
	resp := map[string]any{"blobs": blobs}
	if next != nil {
		resp["cursor"] = *next
	}
	writeJSON(w, resp)
}
