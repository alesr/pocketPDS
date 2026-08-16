package admin

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/xrpc"
)

// appPasswordList returns an account's named app passwords (names + creation
// time only; the password itself is never recoverable).
func (h *Handler) appPasswordList(w http.ResponseWriter, r *http.Request) {
	did := r.PathValue("did")

	rows, err := h.store.DB.QueryContext(r.Context(),
		"SELECT name, created_at FROM app_passwords WHERE did = ? ORDER BY created_at", did)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = rows.Close() }()

	var passwords = make([]map[string]any, 0)
	for rows.Next() {
		var name, createdAt string
		if err := rows.Scan(&name, &createdAt); err != nil {
			continue
		}
		passwords = append(passwords, map[string]any{"name": name, "createdAt": createdAt})
	}
	writeJSON(w, map[string]any{"passwords": passwords})
}

// appPasswordCreate mints a new app password and returns it exactly once.
func (h *Handler) appPasswordCreate(w http.ResponseWriter, r *http.Request) {
	did := r.PathValue("did")

	var in struct {
		Name string `json:"name"`
	}
	if xrpc.DecodeBody(w, r, &in) != nil {
		return
	}
	if in.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	password := randomAppPassword()
	hash, err := db.HashPassword(password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := h.store.DB.ExecContext(r.Context(),
		"INSERT INTO app_passwords (did, name, password_hash, created_at) VALUES (?, ?, ?, ?)",
		did, in.Name, hash, time.Now().Format(time.RFC3339)); err != nil {
		http.Error(w, "app password name already in use", http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"name": in.Name, "password": password})
}

// appPasswordRevoke deletes an app password by name.
func (h *Handler) appPasswordRevoke(w http.ResponseWriter, r *http.Request) {
	did := r.PathValue("did")

	var in struct {
		Name string `json:"name"`
	}
	if xrpc.DecodeBody(w, r, &in) != nil {
		return
	}
	if in.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if _, err := h.store.DB.ExecContext(r.Context(),
		"DELETE FROM app_passwords WHERE did = ? AND name = ?", did, in.Name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// randomAppPassword matches the format used by the XRPC createAppPassword
// endpoint (four groups of four hex chars).
func randomAppPassword() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	h := hex.EncodeToString(b)
	return h[0:4] + "-" + h[4:8] + "-" + h[8:12] + "-" + h[12:16]
}
