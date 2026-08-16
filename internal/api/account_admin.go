package api

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/repo"
	"github.com/alesr/pocketPDS/internal/xrpc"
)

func HandleDeactivateAccount(store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}
		if _, err := store.DB.ExecContext(r.Context(),
			"UPDATE accounts SET deactivated_at = ? WHERE did = ?",
			time.Now().Format(time.RFC3339), did); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		_ = mgr.EmitAccount(r.Context(), did, false, strPtr("deactivated"))
		xrpc.WriteJSON(w, map[string]any{})
	}
}

func HandleActivateAccount(store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}
		if _, err := store.DB.ExecContext(r.Context(),
			"UPDATE accounts SET deactivated_at = NULL WHERE did = ?", did); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		_ = mgr.EmitAccount(r.Context(), did, true, nil)
		xrpc.WriteJSON(w, map[string]any{})
	}
}

func HandleDeleteAccount(store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	type input struct {
		Did      string `json:"did"`
		Password string `json:"password"`
		Token    string `json:"token"`
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

		var passwordHash string
		if err := store.DB.QueryRowContext(r.Context(),
			"SELECT password_hash FROM accounts WHERE did = ?", did).Scan(&passwordHash); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		valid, err := db.VerifyPassword(passwordHash, in.Password)
		if err != nil || !valid {
			xrpc.WriteXRPCError(w, http.StatusUnauthorized, "AuthenticationRequired", "invalid password")
			return
		}

		if err := mgr.DeleteAccount(r.Context(), did); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		if _, err := store.DB.ExecContext(r.Context(),
			"DELETE FROM accounts WHERE did = ?", did); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		_ = mgr.EmitAccount(r.Context(), did, false, strPtr("deleted"))
		xrpc.WriteJSON(w, map[string]any{})
	}
}

func HandleCheckAccountStatus(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}
		var deactivated sql.NullString
		if err := store.DB.QueryRowContext(r.Context(),
			"SELECT deactivated_at FROM accounts WHERE did = ?", did).Scan(&deactivated); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		xrpc.WriteJSON(w, map[string]any{
			"activated": !deactivated.Valid,
			"validDid":  true,
		})
	}
}

func strPtr(s string) *string { return &s }
