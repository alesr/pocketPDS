package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/alesr/pocketPDS/internal/config"
	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/repo"
	"github.com/alesr/pocketPDS/internal/xrpc"
)

// passthroughAppview proxies a public read endpoint to the public AppView,
// using the request path as the NSID, and returns `empty` when offline.
func passthroughAppview(cfg *config.Config, empty map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.AppviewProxyURL == "" {
			xrpc.WriteJSON(w, empty)
			return
		}
		proxyXrpc(w, r, cfg.AppviewProxyURL, strings.TrimPrefix(r.URL.Path, "/xrpc/"))
	}
}

// HandleAppBskyGetFeed proxies a custom/generated feed (e.g. Discover) and
// injects the viewer's like/repost state.
func HandleAppBskyGetFeed(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := newAppview(store, mgr, cfg)
		viewerDID := optionalAuth(r, store)
		if cfg.AppviewProxyURL == "" {
			writeFeed(w, []map[string]any{}, nil)
			return
		}
		status, body, err := proxyXrpcGet(r.Context(), cfg.AppviewProxyURL, "app.bsky.feed.getFeed", r.URL.RawQuery)
		if err != nil || status != http.StatusOK {
			writeFeed(w, []map[string]any{}, nil)
			return
		}
		var out struct {
			Feed   []map[string]any `json:"feed"`
			Cursor *string          `json:"cursor"`
		}
		if json.Unmarshal(body, &out) != nil {
			writeFeed(w, []map[string]any{}, nil)
			return
		}
		a.loadViewerState(r.Context(), viewerDID).injectFeed(out.Feed)
		writeFeed(w, out.Feed, out.Cursor)
	}
}

func HandleAppBskyGetFeedGenerator(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return passthroughAppview(cfg, map[string]any{"view": nil})
}

func HandleAppBskyGetPopularFeedGenerators(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return passthroughAppview(cfg, map[string]any{"feeds": []any{}})
}

func HandleAppBskyGetSuggestedFeeds(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return passthroughAppview(cfg, map[string]any{"feeds": []any{}})
}

func HandleAppBskyLabelerGetServices(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return passthroughAppview(cfg, map[string]any{"views": []any{}})
}

func HandleAppBskyGetSuggestedFollowsByActor(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return passthroughAppview(cfg, map[string]any{"suggestions": []any{}})
}

func HandleAppBskyGetUnreadCount(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := requireAuth(w, r, store); !ok {
			return
		}
		xrpc.WriteJSON(w, map[string]any{"count": 0})
	}
}

func HandleAppBskyUpdateSeen(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := requireAuth(w, r, store); !ok {
			return
		}
		if xrpc.DecodeBody(w, r, &struct {
			SeenAt string `json:"seenAt"`
		}{}) != nil {
			return
		}
		xrpc.WriteJSON(w, map[string]any{})
	}
}

func HandleAppBskyRegisterPush(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := requireAuth(w, r, store); !ok {
			return
		}
		var in struct {
			ServiceDid string `json:"serviceDid"`
			Token      string `json:"token"`
			Platform   string `json:"platform"`
			AppId      string `json:"appId"`
		}
		if xrpc.DecodeBody(w, r, &in) != nil {
			return
		}
		xrpc.WriteJSON(w, map[string]any{})
	}
}

func HandleChatListConvos(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := requireAuth(w, r, store); !ok {
			return
		}
		xrpc.WriteJSON(w, map[string]any{"convos": []any{}, "cursor": ""})
	}
}
