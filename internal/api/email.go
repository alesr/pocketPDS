package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/email"
	"github.com/alesr/pocketPDS/internal/xrpc"
)

const emailTokenTTL = time.Hour

func HandleRequestEmailConfirmation(store *db.Store, sender *email.Sender) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}
		var emailAddr string
		if err := store.DB.QueryRowContext(r.Context(),
			"SELECT email FROM accounts WHERE did = ?", did).Scan(&emailAddr); err != nil || emailAddr == "" {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", "no email on account")
			return
		}
		token := randomEmailToken()
		if _, err := store.DB.ExecContext(r.Context(),
			"INSERT INTO email_tokens (token, did, purpose, email, expires_at) VALUES (?, ?, 'confirm', ?, ?)",
			token, did, emailAddr, time.Now().Add(emailTokenTTL).Format(time.RFC3339)); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		_ = sender.Send(emailAddr, "Confirm your email", "Your confirmation code: "+token)
		xrpc.WriteJSON(w, map[string]any{})
	}
}

func HandleConfirmEmail(store *db.Store) http.HandlerFunc {
	type input struct {
		Email string `json:"email"`
		Token string `json:"token"`
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
		if !consumeEmailToken(r, store, did, in.Token, "confirm") {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidToken", "invalid or expired token")
			return
		}
		if _, err := store.DB.ExecContext(r.Context(),
			"UPDATE accounts SET email = ?, email_confirmed_at = ? WHERE did = ?",
			in.Email, time.Now().Format(time.RFC3339), did); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		xrpc.WriteJSON(w, map[string]any{})
	}
}

func HandleRequestPasswordReset(store *db.Store, sender *email.Sender) http.HandlerFunc {
	type input struct {
		Email string `json:"email"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var in input
		if xrpc.DecodeBody(w, r, &in) != nil {
			return
		}
		var did string
		if err := store.DB.QueryRowContext(r.Context(),
			"SELECT did FROM accounts WHERE email = ?", in.Email).Scan(&did); err != nil {
			// Do not reveal whether the email exists.
			xrpc.WriteJSON(w, map[string]any{})
			return
		}
		token := randomEmailToken()
		if _, err := store.DB.ExecContext(r.Context(),
			"INSERT INTO email_tokens (token, did, purpose, email, expires_at) VALUES (?, ?, 'reset', ?, ?)",
			token, did, in.Email, time.Now().Add(emailTokenTTL).Format(time.RFC3339)); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		_ = sender.Send(in.Email, "Reset your password", "Your reset code: "+token)
		xrpc.WriteJSON(w, map[string]any{})
	}
}

func HandleResetPassword(store *db.Store) http.HandlerFunc {
	type input struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var in input
		if xrpc.DecodeBody(w, r, &in) != nil {
			return
		}
		did, _, ok := findEmailToken(r, store, in.Token, "reset")
		if !ok {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidToken", "invalid or expired token")
			return
		}
		hash, err := db.HashPassword(in.Password)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		if _, err := store.DB.ExecContext(r.Context(),
			"UPDATE accounts SET password_hash = ? WHERE did = ?", hash, did); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		markEmailTokenUsed(r, store, in.Token)
		xrpc.WriteJSON(w, map[string]any{})
	}
}

func findEmailToken(r *http.Request, store *db.Store, token, purpose string) (string, string, bool) {
	var did, emailAddr, expiresAt string
	var usedAt *string
	if err := store.DB.QueryRowContext(r.Context(),
		"SELECT did, email, expires_at, used_at FROM email_tokens WHERE token = ? AND purpose = ?",
		token, purpose).Scan(&did, &emailAddr, &expiresAt, &usedAt); err != nil {
		return "", "", false
	}
	if usedAt != nil {
		return "", "", false
	}
	if exp, err := time.Parse(time.RFC3339, expiresAt); err != nil || time.Now().After(exp) {
		return "", "", false
	}
	return did, emailAddr, true
}

func consumeEmailToken(r *http.Request, store *db.Store, did, token, purpose string) bool {
	tokenDid, _, ok := findEmailToken(r, store, token, purpose)
	if !ok || tokenDid != did {
		return false
	}
	markEmailTokenUsed(r, store, token)
	return true
}

func markEmailTokenUsed(r *http.Request, store *db.Store, token string) {
	_, _ = store.DB.ExecContext(r.Context(),
		"UPDATE email_tokens SET used_at = ? WHERE token = ?", time.Now().Format(time.RFC3339), token)
}

func randomEmailToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
