package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/alesr/pocketPDS/internal/blob"
	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/xrpc"
	atproto "github.com/bluesky-social/indigo/api/atproto"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/ipfs/go-cid"
)

func HandleUploadBlob(store *db.Store, blobs *blob.Store, sizeLimit int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}

		mime := r.Header.Get("Content-Type")
		if mime == "" {
			mime = "application/octet-stream"
		}

		body := r.Body
		if sizeLimit > 0 {
			body = http.MaxBytesReader(w, r.Body, sizeLimit)
		}

		c, size, err := blobs.Put(r.Context(), did, mime, body)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				xrpc.WriteXRPCError(w, http.StatusRequestEntityTooLarge, "BlobTooLarge", "blob exceeds size limit")
				return
			}
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}

		xrpc.WriteJSON(w, atproto.RepoUploadBlob_Output{
			Blob: &lexutil.LexBlob{Ref: lexutil.LexLink(c), MimeType: mime, Size: size},
		})
	}
}

func HandleGetBlob(blobs *blob.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		c, err := cid.Decode(q.Get("cid"))
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}

		f, mime, size, err := blobs.Open(r.Context(), q.Get("did"), c)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusNotFound, "BlobNotFound", "blob not found")
			return
		}
		defer func() { _ = f.Close() }()

		if mime != "" {
			w.Header().Set("Content-Type", mime)
		}
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, f)
	}
}

func HandleListBlobs(blobs *blob.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		limit := 500
		if raw := q.Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 1000 {
				xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", "limit must be 1-1000")
				return
			}
			limit = n
		}

		cids, next, err := blobs.List(r.Context(), q.Get("did"), q.Get("since"), q.Get("cursor"), limit)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}

		resp := map[string]any{"cids": cids}
		if next != nil {
			resp["cursor"] = *next
		}
		xrpc.WriteJSON(w, resp)
	}
}
