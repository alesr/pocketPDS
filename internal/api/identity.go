package api

import (
	"net/http"

	"github.com/alesr/pocketPDS/internal/xrpc"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
)

var (
	dir     = identity.DefaultDirectory()
	baseDir = &identity.BaseDirectory{}
)

func HandleResolveHandle(w http.ResponseWriter, r *http.Request) {
	handle, err := syntax.ParseHandle(r.URL.Query().Get("handle"))
	if err != nil {
		xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
		return
	}
	ident, err := dir.LookupHandle(r.Context(), handle)
	if err != nil {
		xrpc.WriteXRPCError(w, http.StatusBadRequest, "HandleNotFound", "handle not found")
		return
	}
	xrpc.WriteJSON(w, map[string]string{"did": ident.DID.String()})
}

func HandleResolveDid(w http.ResponseWriter, r *http.Request) {
	did, err := syntax.ParseDID(r.URL.Query().Get("did"))
	if err != nil {
		xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
		return
	}
	doc, err := baseDir.ResolveDIDRaw(r.Context(), did)
	if err != nil {
		xrpc.WriteXRPCError(w, http.StatusBadRequest, "DidNotFound", "did not found")
		return
	}
	xrpc.WriteJSON(w, doc)
}
