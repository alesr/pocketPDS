package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/firehose"
	"github.com/alesr/pocketPDS/internal/repo"
	"github.com/alesr/pocketPDS/internal/xrpc"
	atproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/ipfs/go-cid"
)

const carContentType = "application/vnd.ipld.car"

func HandleGetLatestCommit(mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, err := syntax.ParseDID(r.URL.Query().Get("did"))
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		head, rev, err := mgr.Head(r.Context(), did.String())
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusNotFound, "RepoNotFound", "repo not found")
			return
		}
		xrpc.WriteJSON(w, map[string]string{"cid": head.String(), "rev": rev})
	}
}

func HandleGetRepo(mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, err := syntax.ParseDID(r.URL.Query().Get("did"))
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		if _, _, err := mgr.Head(r.Context(), did.String()); err != nil {
			xrpc.WriteXRPCError(w, http.StatusNotFound, "RepoNotFound", "repo not found")
			return
		}

		w.Header().Set("Content-Type", carContentType)
		w.WriteHeader(http.StatusOK)
		if err := mgr.WriteRepoCAR(r.Context(), w, did.String(), r.URL.Query().Get("since")); err != nil {
			slog.Error("getRepo stream", "err", err)
		}
	}
}

func HandleGetCheckout(mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, err := syntax.ParseDID(r.URL.Query().Get("did"))
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		if _, _, err := mgr.Head(r.Context(), did.String()); err != nil {
			xrpc.WriteXRPCError(w, http.StatusNotFound, "RepoNotFound", "repo not found")
			return
		}

		w.Header().Set("Content-Type", carContentType)
		w.WriteHeader(http.StatusOK)
		if err := mgr.WriteRepoCAR(r.Context(), w, did.String(), ""); err != nil {
			slog.Error("getCheckout stream", "err", err)
		}
	}
}

func HandleSyncGetRecord(mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		did, err := syntax.ParseDID(q.Get("did"))
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		collection, err := syntax.ParseNSID(q.Get("collection"))
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		rkey, err := syntax.ParseRecordKey(q.Get("rkey"))
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}

		if _, _, err := mgr.RecordBlock(r.Context(), did.String(), collection.String(), rkey.String()); err != nil {
			xrpc.WriteXRPCError(w, http.StatusNotFound, "RecordNotFound", "record not found")
			return
		}

		w.Header().Set("Content-Type", carContentType)
		w.WriteHeader(http.StatusOK)
		if err := mgr.WriteRecordCAR(r.Context(), w, did.String(), collection.String(), rkey.String()); err != nil {
			slog.Error("getRecord stream", "err", err)
		}
	}
}

func HandleGetBlocks(mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var cids []cid.Cid
		for _, raw := range r.URL.Query()["cids"] {
			c, err := cid.Decode(raw)
			if err != nil {
				xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
				return
			}
			cids = append(cids, c)
		}

		w.Header().Set("Content-Type", carContentType)
		w.WriteHeader(http.StatusOK)
		if err := mgr.WriteBlocksCAR(r.Context(), w, cids); err != nil {
			slog.Error("getBlocks stream", "err", err)
		}
	}
}

func HandleListRepos(mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 1000 {
				xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", "limit must be 1-1000")
				return
			}
			limit = n
		}

		repos, next, err := mgr.ListRepos(r.Context(), r.URL.Query().Get("cursor"), limit)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}

		out := make([]map[string]any, 0, len(repos))
		for _, rp := range repos {
			out = append(out, map[string]any{
				"did":    rp.Did,
				"head":   rp.Head,
				"rev":    rp.Rev,
				"active": rp.Active,
			})
		}

		resp := map[string]any{"repos": out}
		if next != nil {
			resp["cursor"] = *next
		}
		xrpc.WriteJSON(w, resp)
	}
}

// HandleListMissingBlobs reports blobs referenced by repos but missing from
// local storage. A self-hosted PDS stores every blob it uploads, so this is
// always empty.
func HandleListMissingBlobs() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		xrpc.WriteJSON(w, map[string]any{"cursor": "", "blobs": []any{}})
	}
}

// HandleListReposByCollection lists the DIDs that have records in a collection.
func HandleListReposByCollection(mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		collection, err := syntax.ParseNSID(r.URL.Query().Get("collection"))
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}

		limit := 50
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 2000 {
				xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", "limit must be 1-2000")
				return
			}
			limit = n
		}

		dids, next, err := mgr.ListReposByCollection(r.Context(), collection.String(), r.URL.Query().Get("cursor"), limit)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}

		out := make([]map[string]any, 0, len(dids))
		for _, d := range dids {
			out = append(out, map[string]any{"did": d})
		}
		resp := map[string]any{"repos": out}
		if next != nil {
			resp["cursor"] = *next
		}
		xrpc.WriteJSON(w, resp)
	}
}

func HandleNotifyOfUpdate(store *db.Store) http.HandlerFunc {
	type input struct {
		Hostname string `json:"hostname"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var in input
		if xrpc.DecodeBody(w, r, &in) != nil {
			return
		}
		if in.Hostname != "" {
			_, _ = store.DB.ExecContext(r.Context(),
				"INSERT INTO relays (hostname, registered_at) VALUES (?, ?) ON CONFLICT(hostname) DO NOTHING",
				in.Hostname, time.Now().Format(time.RFC3339))
		}
		xrpc.WriteJSON(w, map[string]any{})
	}
}

func HandleRequestCrawl(w http.ResponseWriter, _ *http.Request) {
	xrpc.WriteJSON(w, map[string]any{})
}

func HandleGetHostStatus(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var accounts int64
		_ = store.DB.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM accounts").Scan(&accounts)
		xrpc.WriteJSON(w, map[string]any{
			"hostname":     r.Host,
			"online":       true,
			"accountCount": accounts,
		})
	}
}

func HandleGetRepoStatus(store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, err := syntax.ParseDID(r.URL.Query().Get("did"))
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		var deactivated *string
		var handle string
		if err := store.DB.QueryRowContext(r.Context(),
			"SELECT handle, deactivated_at FROM accounts WHERE did = ?", did.String()).Scan(&handle, &deactivated); err != nil {
			xrpc.WriteXRPCError(w, http.StatusNotFound, "RepoNotFound", "repo not found")
			return
		}
		_, rev, _ := mgr.Head(r.Context(), did.String())
		active := deactivated == nil
		xrpc.WriteJSON(w, map[string]any{
			"did":    did.String(),
			"active": active,
			"rev":    rev,
		})
	}
}

func HandleSubscribeRepos(mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var cursor int64
		if raw := r.URL.Query().Get("cursor"); raw != "" {
			c, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || c < 0 {
				xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", "invalid cursor")
				return
			}
			cursor = c
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", "streaming unsupported")
			return
		}

		w.Header().Set("Content-Type", carContentType)
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)

		info, err := firehose.MarshalFrame("#info", &atproto.SyncSubscribeRepos_Info{Name: "pocketpds"})
		if err != nil {
			return
		}
		if _, err := w.Write(info); err != nil {
			return
		}
		flusher.Flush()

		frames, cleanup := mgr.Emitter().Subscribe(r.Context(), cursor)
		defer cleanup()
		for {
			select {
			case frame, ok := <-frames:
				if !ok {
					return
				}
				if _, err := w.Write(frame); err != nil {
					return
				}
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	}
}
