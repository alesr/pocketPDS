package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alesr/pocketPDS/internal/config"
	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/xrpc"
)

var searchClient = &http.Client{Timeout: 12 * time.Second}

// proxyXrpcGet performs an unauthenticated GET against an upstream AppView and
// returns the status code and raw body. The Authorization header is never
// forwarded, since the upstream AppView does not understand this PDS's tokens.
func proxyXrpcGet(ctx context.Context, base, nsid, rawQuery string) (int, []byte, error) {
	upstream := strings.TrimRight(base, "/") + "/xrpc/" + nsid
	if rawQuery != "" {
		upstream += "?" + rawQuery
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream, nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := searchClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

// proxyXrpc forwards the request to an upstream AppView, copying status,
// content type, and body.
func proxyXrpc(w http.ResponseWriter, r *http.Request, base, nsid string) {
	status, body, err := proxyXrpcGet(r.Context(), base, nsid, r.URL.RawQuery)
	if err != nil {
		xrpc.WriteXRPCError(w, http.StatusBadGateway, "UpstreamFailure", "appview proxy unreachable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func HandleAppBskySearchActors(cfg *config.Config, store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := requireAuth(w, r, store); !ok {
			return
		}
		if cfg.AppviewProxyURL == "" {
			xrpc.WriteJSON(w, map[string]any{"actors": []any{}})
			return
		}
		proxyXrpc(w, r, cfg.AppviewProxyURL, "app.bsky.actor.searchActors")
	}
}

func HandleAppBskySearchPosts(cfg *config.Config, store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := requireAuth(w, r, store); !ok {
			return
		}
		if cfg.AppviewProxyURL == "" {
			xrpc.WriteJSON(w, map[string]any{"posts": []any{}})
			return
		}
		proxyXrpc(w, r, cfg.AppviewProxyURL, "app.bsky.feed.searchPosts")
	}
}
