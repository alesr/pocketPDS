package api

import (
	"net/http"
	"strings"

	"github.com/alesr/pocketPDS/internal/config"
	"github.com/alesr/pocketPDS/internal/crypto"
	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/repo"
	"github.com/alesr/pocketPDS/internal/xrpc"
	"github.com/bluesky-social/indigo/atproto/syntax"
)

// HandleImportRepo ingests a repo CAR for an existing account. The request body
// is the CAR (application/vnd.ipld.car) and the did query parameter names the
// account. The admin token must be sent as a bearer token.
func HandleImportRepo(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.AdminToken == "" {
			xrpc.WriteXRPCError(w, http.StatusNotFound, "MethodNotFound", "importRepo is disabled without an admin token")
			return
		}
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || !crypto.SecureEqual([]byte(tok), []byte(cfg.AdminToken)) {
			xrpc.WriteXRPCError(w, http.StatusUnauthorized, "AuthenticationRequired", "invalid admin token")
			return
		}

		did, err := syntax.ParseDID(r.URL.Query().Get("did"))
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}

		var one int
		if err := store.DB.QueryRowContext(r.Context(), "SELECT 1 FROM accounts WHERE did = ?", did.String()).Scan(&one); err != nil {
			xrpc.WriteXRPCError(w, http.StatusNotFound, "RepoNotFound", "account not found")
			return
		}

		if err := mgr.ImportRepo(r.Context(), did.String(), r.Body); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		xrpc.WriteJSON(w, map[string]any{})
	}
}
