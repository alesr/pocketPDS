package api

import (
	"net/http"
	"strings"

	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/xrpc"
)

// HandleDidWebWellKnown serves the DID document for the account whose handle
// matches the request Host (did:web resolution for /.well-known/did.json).
func HandleDidWebWellKnown(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if i := strings.Index(host, ":"); i >= 0 {
			host = host[:i]
		}
		serveDidDoc(w, r, store, host)
	}
}

// HandleDidWebPath serves a DID document by handle path segment
// (did:web path-form resolution: /{handle}/did.json).
func HandleDidWebPath(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handle := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/did.json")
		if handle == "" {
			xrpc.WriteXRPCError(w, http.StatusNotFound, "DidNotFound", "not found")
			return
		}
		serveDidDoc(w, r, store, handle)
	}
}

// HandleAtprotoDid serves the account DID as plain text for the request Host
// (atproto handle resolution via /.well-known/atproto-did).
func HandleAtprotoDid(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if i := strings.Index(host, ":"); i >= 0 {
			host = host[:i]
		}
		var did string
		if err := store.DB.QueryRowContext(r.Context(),
			"SELECT did FROM accounts WHERE handle = ?", host).Scan(&did); err != nil {
			xrpc.WriteXRPCError(w, http.StatusNotFound, "DidNotFound", "not found")
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(did))
	}
}

func serveDidDoc(w http.ResponseWriter, r *http.Request, store *db.Store, handle string) {
	var didDoc string
	err := store.DB.QueryRowContext(r.Context(),
		"SELECT did_doc FROM accounts WHERE handle = ?", handle).Scan(&didDoc)
	if err != nil {
		xrpc.WriteXRPCError(w, http.StatusNotFound, "DidNotFound", "not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(didDoc))
}
