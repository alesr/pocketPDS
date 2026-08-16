package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/xrpc"
)

const refreshTokenTTL = 30 * 24 * time.Hour

func HandleCreateSession(store *db.Store) http.HandlerFunc {
	type input struct {
		Identifier string `json:"identifier"`
		Password   string `json:"password"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var in input
		if xrpc.DecodeBody(w, r, &in) != nil {
			return
		}

		did, handle, passwordHash, err := lookupAccount(r.Context(), store, in.Identifier)
		if err != nil {
			unauthorized(w)
			return
		}

		ok, err := db.VerifyPassword(passwordHash, in.Password)
		appPassword := ""
		if err != nil || !ok {
			name, matched := verifyAppPassword(r, store, did, in.Password)
			if !matched {
				unauthorized(w)
				return
			}
			appPassword = name
		}

		access, refresh, err := mintSession(r.Context(), store, did, appPassword)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}

		xrpc.WriteJSON(w, map[string]any{
			"accessJwt":  access,
			"refreshJwt": refresh,
			"handle":     handle,
			"did":        did,
		})
	}
}

// lookupAccount resolves a login identifier (handle, email, or DID) to an
// account row, returning the DID, handle, and password hash. Deactivated
// accounts are treated as not found.
func lookupAccount(ctx context.Context, store *db.Store, identifier string) (did, handle, passwordHash string, err error) {
	var deactivated *string
	switch {
	case strings.HasPrefix(identifier, "did:"):
		err = store.DB.QueryRowContext(ctx,
			"SELECT did, handle, password_hash, deactivated_at FROM accounts WHERE did = ?",
			identifier).Scan(&did, &handle, &passwordHash, &deactivated)
	case strings.Contains(identifier, "@"):
		err = store.DB.QueryRowContext(ctx,
			"SELECT did, handle, password_hash, deactivated_at FROM accounts WHERE email = ? COLLATE NOCASE",
			identifier).Scan(&did, &handle, &passwordHash, &deactivated)
	default:
		err = store.DB.QueryRowContext(ctx,
			"SELECT did, handle, password_hash, deactivated_at FROM accounts WHERE handle = ? COLLATE NOCASE",
			identifier).Scan(&did, &handle, &passwordHash, &deactivated)
	}
	if err != nil {
		return "", "", "", err
	}
	if deactivated != nil {
		return "", "", "", sql.ErrNoRows
	}
	return did, handle, passwordHash, nil
}

func HandleGetSession(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}

		var handle string
		var deactivated *string
		if err := store.DB.QueryRowContext(r.Context(),
			"SELECT handle, deactivated_at FROM accounts WHERE did = ?", did).Scan(&handle, &deactivated); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}

		xrpc.WriteJSON(w, map[string]any{
			"did":    did,
			"handle": handle,
			"active": deactivated == nil,
		})
	}
}

func HandleRefreshSession(store *db.Store) http.HandlerFunc {
	type input struct {
		RefreshJwt string `json:"refreshJwt"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var in input
		if xrpc.DecodeBody(w, r, &in) != nil {
			return
		}

		var did, appPassword string
		var expiresAt string
		if err := store.DB.QueryRowContext(r.Context(),
			"SELECT did, COALESCE(app_password, ''), expires_at FROM auth_sessions WHERE token_hash = ?",
			hashToken(in.RefreshJwt)).Scan(&did, &appPassword, &expiresAt); err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidToken", "invalid refresh token")
			return
		}

		if exp, err := time.Parse(time.RFC3339, expiresAt); err != nil || time.Now().After(exp) {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidToken", "invalid refresh token")
			return
		}

		var deactivated *string
		if err := store.DB.QueryRowContext(r.Context(),
			"SELECT deactivated_at FROM accounts WHERE did = ?", did).Scan(&deactivated); err != nil || deactivated != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidToken", "invalid refresh token")
			return
		}

		if _, err := store.DB.ExecContext(r.Context(),
			"DELETE FROM auth_sessions WHERE token_hash = ?", hashToken(in.RefreshJwt)); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}

		access, refresh, err := mintSession(r.Context(), store, did, appPassword)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}

		var handle string
		if err := store.DB.QueryRowContext(r.Context(),
			"SELECT handle FROM accounts WHERE did = ?", did).Scan(&handle); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}

		xrpc.WriteJSON(w, map[string]any{
			"accessJwt":  access,
			"refreshJwt": refresh,
			"handle":     handle,
			"did":        did,
		})
	}
}

func HandleDeleteSession(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			xrpc.WriteXRPCError(w, http.StatusUnauthorized, "AuthMissing", "missing bearer token")
			return
		}
		if _, err := store.DB.ExecContext(r.Context(),
			"DELETE FROM auth_sessions WHERE token_hash = ?", hashToken(token)); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		xrpc.WriteJSON(w, map[string]any{})
	}
}

// mintSession creates a new session: a JWT access token plus an opaque refresh
// token (stored hashed). appPassword records the name of the app password used
// to authenticate (empty for password logins).
func mintSession(ctx context.Context, store *db.Store, did, appPassword string) (string, string, error) {
	access, err := mintAccessJWT(store.Box.HMACKey(), did)
	if err != nil {
		return "", "", err
	}

	refresh := hex.EncodeToString(randomBytes(32))
	if _, err := store.DB.ExecContext(ctx,
		"INSERT INTO auth_sessions (token_hash, did, refresh_token, created_at, expires_at, app_password) VALUES (?, ?, '', ?, ?, ?)",
		hashToken(refresh), did, time.Now().Format(time.RFC3339),
		time.Now().Add(refreshTokenTTL).Format(time.RFC3339), appPassword); err != nil {
		return "", "", err
	}
	return access, refresh, nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func bearerToken(r *http.Request) (string, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return token, ok && token != ""
}

func requireAuth(w http.ResponseWriter, r *http.Request, store *db.Store) (string, bool) {
	token, ok := bearerToken(r)
	if !ok {
		xrpc.WriteXRPCError(w, http.StatusUnauthorized, "AuthMissing", "missing bearer token")
		return "", false
	}

	did, err := parseAccessJWT(store.Box.HMACKey(), token)
	if err != nil {
		xrpc.WriteXRPCError(w, http.StatusUnauthorized, "InvalidToken", "invalid token")
		return "", false
	}
	return did, true
}

func unauthorized(w http.ResponseWriter) {
	xrpc.WriteXRPCError(w, http.StatusUnauthorized, "AuthenticationRequired", "invalid identifier or password")
}

func verifyAppPassword(r *http.Request, store *db.Store, did, password string) (string, bool) {
	rows, err := store.DB.QueryContext(r.Context(),
		"SELECT name, password_hash FROM app_passwords WHERE did = ?", did)
	if err != nil {
		return "", false
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name, hash string
		if err := rows.Scan(&name, &hash); err != nil {
			continue
		}
		if ok, _ := db.VerifyPassword(hash, password); ok {
			return name, true
		}
	}
	return "", false
}

// resolveLocalDid maps a repo identifier (DID or local handle) to a DID.
func resolveLocalDid(ctx context.Context, store *db.Store, repo string) (string, error) {
	if strings.HasPrefix(repo, "did:") {
		return repo, nil
	}
	var did string
	err := store.DB.QueryRowContext(ctx, "SELECT did FROM accounts WHERE handle = ?", repo).Scan(&did)
	return did, err
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}
