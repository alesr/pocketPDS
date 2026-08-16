package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/xrpc"
)

func HandleCreateAppPassword(store *db.Store) http.HandlerFunc {
	type input struct {
		Name string `json:"name"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}
		var in input
		if xrpc.DecodeBody(w, r, &in) != nil {
			return
		}
		if in.Name == "" {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", "name is required")
			return
		}

		password := randomAppPassword()
		hash, err := db.HashPassword(password)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		if _, err := store.DB.ExecContext(r.Context(),
			"INSERT INTO app_passwords (did, name, password_hash, created_at) VALUES (?, ?, ?, ?)",
			did, in.Name, hash, time.Now().Format(time.RFC3339)); err != nil {
			xrpc.WriteXRPCError(w, http.StatusConflict, "AppPasswordNameExists", "app password name already in use")
			return
		}

		xrpc.WriteJSON(w, map[string]any{"name": in.Name, "password": password})
	}
}

func HandleListAppPasswords(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}
		rows, err := store.DB.QueryContext(r.Context(),
			"SELECT name, created_at FROM app_passwords WHERE did = ? ORDER BY created_at", did)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		defer func() { _ = rows.Close() }()

		var passwords []map[string]any
		for rows.Next() {
			var name, createdAt string
			if err := rows.Scan(&name, &createdAt); err != nil {
				continue
			}
			passwords = append(passwords, map[string]any{"name": name, "createdAt": createdAt})
		}
		xrpc.WriteJSON(w, map[string]any{"passwords": passwords})
	}
}

func HandleRevokeAppPassword(store *db.Store) http.HandlerFunc {
	type input struct {
		Name string `json:"name"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}
		var in input
		if xrpc.DecodeBody(w, r, &in) != nil {
			return
		}
		if _, err := store.DB.ExecContext(r.Context(),
			"DELETE FROM app_passwords WHERE did = ? AND name = ?", did, in.Name); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		xrpc.WriteJSON(w, map[string]any{})
	}
}

func randomAppPassword() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	h := hex.EncodeToString(b)
	return h[0:4] + "-" + h[4:8] + "-" + h[8:12] + "-" + h[12:16]
}
