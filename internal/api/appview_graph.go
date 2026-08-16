package api

import (
	"context"
	"net/http"
	"time"

	"github.com/alesr/pocketPDS/internal/config"
	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/repo"
	"github.com/alesr/pocketPDS/internal/xrpc"
)

// subjectProfile resolves a DID (local or remote) to a profile view, injecting
// the viewer's relationship state.
func (a *appviewSvc) subjectProfile(ctx context.Context, subject, viewerDID string, vs *viewerState) map[string]any {
	if a.isLocal(ctx, subject) {
		return a.profileDetailed(ctx, subject, viewerDID)
	}
	p, ok := a.remoteProfile(ctx, subject)
	if !ok {
		return nil
	}
	vs.injectProfileViewer(p)
	return p
}

func (a *appviewSvc) subjectDIDs(ctx context.Context, did, collection string) []string {
	rows, err := a.store.DB.QueryContext(ctx,
		"SELECT json_extract(value, '$.subject') FROM repo_records WHERE did = ? AND collection = ? AND json_extract(value, '$.subject') IS NOT NULL",
		did, collection)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var subject string
		if err := rows.Scan(&subject); err == nil {
			out = append(out, subject)
		}
	}
	return out
}

func HandleAppBskyMuteActor(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}
		var in struct {
			Actor string `json:"actor"`
		}
		if xrpc.DecodeBody(w, r, &in) != nil {
			return
		}
		subject, _, err := resolveActorDID(r.Context(), store, in.Actor)
		if err != nil {
			writeAppviewError(w, err)
			return
		}
		if _, err := store.DB.ExecContext(r.Context(),
			"INSERT INTO mutes (did, subject, created_at) VALUES (?, ?, ?) ON CONFLICT(did, subject) DO NOTHING",
			did, subject, time.Now().Format(time.RFC3339)); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		xrpc.WriteJSON(w, map[string]any{})
	}
}

func HandleAppBskyUnmuteActor(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}
		var in struct {
			Actor string `json:"actor"`
		}
		if xrpc.DecodeBody(w, r, &in) != nil {
			return
		}
		subject, _, err := resolveActorDID(r.Context(), store, in.Actor)
		if err != nil {
			writeAppviewError(w, err)
			return
		}
		if _, err := store.DB.ExecContext(r.Context(),
			"DELETE FROM mutes WHERE did = ? AND subject = ?", did, subject); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		xrpc.WriteJSON(w, map[string]any{})
	}
}

func HandleAppBskyGetBlocks(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}
		a := newAppview(store, mgr, cfg)
		vs := a.loadViewerState(r.Context(), did)
		blocks := make([]map[string]any, 0)
		for _, subject := range a.subjectDIDs(r.Context(), did, "app.bsky.graph.block") {
			if p := a.subjectProfile(r.Context(), subject, did, vs); p != nil {
				blocks = append(blocks, p)
			}
		}
		xrpc.WriteJSON(w, map[string]any{"blocks": blocks, "cursor": ""})
	}
}

func HandleAppBskyGetMutes(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}
		a := newAppview(store, mgr, cfg)
		vs := a.loadViewerState(r.Context(), did)
		mutes := make([]map[string]any, 0)
		rows, err := a.store.DB.QueryContext(r.Context(), "SELECT subject FROM mutes WHERE did = ?", did)
		if err == nil {
			var subjects []string
			for rows.Next() {
				var subject string
				if rows.Scan(&subject) == nil {
					subjects = append(subjects, subject)
				}
			}
			_ = rows.Close()
			for _, subject := range subjects {
				if p := a.subjectProfile(r.Context(), subject, did, vs); p != nil {
					mutes = append(mutes, p)
				}
			}
		}
		xrpc.WriteJSON(w, map[string]any{"mutes": mutes, "cursor": ""})
	}
}

// Stubs for endpoints a client may call that are not meaningful for a
// single-user PDS; they return valid empty shapes to avoid client errors.

func HandleAppBskyGetFeedGenerators(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		xrpc.WriteJSON(w, map[string]any{"feeds": []any{}})
	}
}

func HandleAppBskyGetLists(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := requireAuth(w, r, store); !ok {
			return
		}
		xrpc.WriteJSON(w, map[string]any{"lists": []any{}, "cursor": ""})
	}
}

func HandleAppBskyGetListBlocks(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := requireAuth(w, r, store); !ok {
			return
		}
		xrpc.WriteJSON(w, map[string]any{"blocks": []any{}, "cursor": ""})
	}
}

func HandleAppBskyGetListMutes(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := requireAuth(w, r, store); !ok {
			return
		}
		xrpc.WriteJSON(w, map[string]any{"mutes": []any{}, "cursor": ""})
	}
}

func HandleAppBskySearchActorsTypeahead(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := requireAuth(w, r, store); !ok {
			return
		}
		xrpc.WriteJSON(w, map[string]any{"actors": []any{}})
	}
}
