package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/ipfs/go-cid"
)

// accountDetail returns a single account with repo head, collections, blob
// stats, and session counts. It never exposes password hashes or keys.
func (h *Handler) accountDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	did := r.PathValue("did")

	var handle, createdAt, didDocJSON string
	var email, emailConfirmedAt, deactivatedAt *string
	err := h.store.DB.QueryRowContext(ctx,
		"SELECT handle, email, email_confirmed_at, deactivated_at, created_at, did_doc FROM accounts WHERE did = ?",
		did).Scan(&handle, &email, &emailConfirmedAt, &deactivatedAt, &createdAt, &didDocJSON)
	if err != nil {
		http.Error(w, "account not found", http.StatusNotFound)
		return
	}

	var didDoc any
	_ = json.Unmarshal([]byte(didDocJSON), &didDoc)

	headCID, rev, headErr := h.mgr.Head(ctx, did)
	headCidStr, headRev := "", ""
	if headErr == nil {
		headCidStr = headCID.String()
		headRev = rev
	}

	type collectionCount struct {
		Collection string `json:"collection"`
		Count      int64  `json:"count"`
	}
	collections := make([]collectionCount, 0)
	rows, err := h.store.DB.QueryContext(ctx,
		"SELECT collection, COUNT(*) FROM repo_records WHERE did = ? GROUP BY collection ORDER BY collection", did)
	if err == nil {
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var c collectionCount
			if rows.Scan(&c.Collection, &c.Count) == nil {
				collections = append(collections, c)
			}
		}
	}

	var blobCount, blobBytes int64
	_ = h.store.DB.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(size),0) FROM blobs WHERE did = ?", did).Scan(&blobCount, &blobBytes)

	var sessions, appPasswords int64
	_ = h.store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM auth_sessions WHERE did = ?", did).Scan(&sessions)
	_ = h.store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM app_passwords WHERE did = ?", did).Scan(&appPasswords)

	writeJSON(w, map[string]any{
		"did":            did,
		"handle":         handle,
		"email":          email,
		"emailConfirmed": emailConfirmedAt != nil,
		"active":         deactivatedAt == nil,
		"createdAt":      createdAt,
		"didDoc":         didDoc,
		"head":           map[string]any{"cid": headCidStr, "rev": headRev},
		"collections":    collections,
		"blobs":          map[string]any{"count": blobCount, "bytes": blobBytes},
		"sessions":       sessions,
		"appPasswords":   appPasswords,
	})
}

// accountRecords returns paginated records within one collection.
func (h *Handler) accountRecords(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	did := r.PathValue("did")
	collection := r.URL.Query().Get("collection")
	if collection == "" {
		http.Error(w, "collection is required", http.StatusBadRequest)
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= 100 {
			limit = n
		}
	}

	items, next, err := h.mgr.ListRecords(ctx, did, collection, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	records := make([]map[string]any, 0, len(items))
	for _, it := range items {
		var value any
		_ = json.Unmarshal(it.Value, &value)
		records = append(records, map[string]any{
			"rkey":  it.RKey,
			"cid":   it.CID,
			"uri":   "at://" + did + "/" + collection + "/" + it.RKey,
			"value": value,
		})
	}

	resp := map[string]any{"records": records}
	if next != nil {
		resp["cursor"] = *next
	}
	writeJSON(w, resp)
}

// accountCommits returns the repo commit chain for an account.
func (h *Handler) accountCommits(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	did := r.PathValue("did")

	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= 500 {
			limit = n
		}
	}

	rows, err := h.store.DB.QueryContext(ctx,
		"SELECT rev, cid, prev_cid, data_root, created_at FROM repo_commits WHERE did = ? ORDER BY rev DESC LIMIT ?",
		did, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = rows.Close() }()

	var commits = make([]map[string]any, 0)
	for rows.Next() {
		var rev, createdAt string
		var cidBytes, prevBytes, dataRootBytes []byte
		if err := rows.Scan(&rev, &cidBytes, &prevBytes, &dataRootBytes, &createdAt); err != nil {
			continue
		}
		commits = append(commits, map[string]any{
			"rev":       rev,
			"cid":       cidString(cidBytes),
			"prevCid":   nullableCid(prevBytes),
			"dataRoot":  cidString(dataRootBytes),
			"createdAt": createdAt,
		})
	}
	writeJSON(w, map[string]any{"commits": commits})
}

// accountSessions lists active sessions for an account (no token material).
func (h *Handler) accountSessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	did := r.PathValue("did")

	rows, err := h.store.DB.QueryContext(ctx,
		"SELECT created_at, expires_at, app_password FROM auth_sessions WHERE did = ? ORDER BY created_at DESC", did)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = rows.Close() }()

	var sessions = make([]map[string]any, 0)
	for rows.Next() {
		var createdAt, expiresAt string
		var appPassword *string
		if err := rows.Scan(&createdAt, &expiresAt, &appPassword); err != nil {
			continue
		}
		sessions = append(sessions, map[string]any{
			"createdAt":   createdAt,
			"expiresAt":   expiresAt,
			"appPassword": appPassword,
		})
	}
	writeJSON(w, map[string]any{"sessions": sessions})
}

// accountBlobs lists blobs owned by one account.
func (h *Handler) accountBlobs(w http.ResponseWriter, r *http.Request) {
	did := r.PathValue("did")
	writeBlobs(w, r, h, did)
}

func cidString(b []byte) string {
	c, err := cid.Cast(b)
	if err != nil {
		return ""
	}
	return c.String()
}

func nullableCid(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return cidString(b)
}
