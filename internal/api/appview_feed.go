package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alesr/pocketPDS/internal/config"
	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/repo"
	"github.com/alesr/pocketPDS/internal/xrpc"
)

const postCollection = "app.bsky.feed.post"

func parsePostURI(uri string) (did, rkey string, ok bool) {
	rest, found := strings.CutPrefix(uri, "at://")
	if !found {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[1] != postCollection {
		return "", "", false
	}
	return parts[0], parts[2], true
}

func (a *appviewSvc) postView(ctx context.Context, did, rkey, cid string, value []byte, viewerDID string) map[string]any {
	author := a.profileBasic(ctx, did, viewerDID)
	if author == nil {
		return nil
	}
	uri := "at://" + did + "/" + postCollection + "/" + rkey

	var rec map[string]any
	if err := json.Unmarshal(value, &rec); err != nil || rec == nil {
		rec = map[string]any{}
	}
	if _, ok := rec["$type"]; !ok {
		rec["$type"] = postCollection
	}
	createdAt, _ := rec["createdAt"].(string)
	if createdAt == "" {
		createdAt = time.Now().UTC().Format(time.RFC3339)
	}

	return map[string]any{
		"uri":         uri,
		"cid":         cid,
		"author":      author,
		"record":      rec,
		"replyCount":  a.countMatches(ctx, did, postCollection, "$.reply.parent.uri", uri),
		"repostCount": a.countMatches(ctx, did, "app.bsky.feed.repost", "$.subject.uri", uri),
		"likeCount":   a.countMatches(ctx, did, "app.bsky.feed.like", "$.subject.uri", uri),
		"indexedAt":   createdAt,
		"viewer":      a.postViewer(ctx, viewerDID, uri),
		"labels":      []any{},
	}
}

func postMatchesFilter(rec map[string]any, filter string) bool {
	switch filter {
	case "posts_no_replies":
		_, hasReply := rec["reply"]
		return !hasReply
	case "posts_with_media":
		_, hasEmbed := rec["embed"]
		return hasEmbed
	default:
		return true
	}
}

// buildFeed returns newest-first FeedViewPosts for a local DID, bounded by limit.
func (a *appviewSvc) buildFeed(ctx context.Context, did, cursor string, limit int, viewerDID, filter string) ([]map[string]any, *string) {
	items, next, err := a.mgr.ListRecordsDesc(ctx, did, postCollection, cursor, limit)
	if err != nil {
		return nil, nil
	}
	feed := make([]map[string]any, 0, len(items))
	for _, it := range items {
		var rec map[string]any
		_ = json.Unmarshal(it.Value, &rec)
		if !postMatchesFilter(rec, filter) {
			continue
		}
		if pv := a.postView(ctx, did, it.RKey, it.CID, it.Value, viewerDID); pv != nil {
			feed = append(feed, map[string]any{"post": pv})
		}
	}
	return feed, next
}

func writeFeed(w http.ResponseWriter, feed []map[string]any, cursor *string) {
	out := map[string]any{"feed": feed}
	if cursor != nil {
		out["cursor"] = *cursor
	}
	xrpc.WriteJSON(w, out)
}

func postCreatedAt(item map[string]any) string {
	p, _ := item["post"].(map[string]any)
	rec, _ := p["record"].(map[string]any)
	ca, _ := rec["createdAt"].(string)
	return ca
}

// followedDIDs returns the DIDs this local account follows.
func (a *appviewSvc) followedDIDs(ctx context.Context, did string) []string {
	rows, err := a.store.DB.QueryContext(ctx,
		"SELECT value FROM repo_records WHERE did = ? AND collection = 'app.bsky.graph.follow'", did)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var value []byte
		if err := rows.Scan(&value); err != nil {
			continue
		}
		var rec struct {
			Subject string `json:"subject"`
		}
		if json.Unmarshal(value, &rec) == nil && rec.Subject != "" {
			out = append(out, rec.Subject)
		}
	}
	return out
}

// remoteAuthorFeed fetches a page of an account's posts from the public AppView.
func (a *appviewSvc) remoteAuthorFeed(ctx context.Context, did string, limit int) []map[string]any {
	if a.cfg.AppviewProxyURL == "" {
		return nil
	}
	status, body, err := proxyXrpcGet(ctx, a.cfg.AppviewProxyURL, "app.bsky.feed.getAuthorFeed",
		"actor="+url.QueryEscape(did)+"&limit="+strconv.Itoa(limit))
	if err != nil || status != http.StatusOK {
		return nil
	}
	var out struct {
		Feed []map[string]any `json:"feed"`
	}
	if json.Unmarshal(body, &out) != nil {
		return nil
	}
	return out.Feed
}

// timeline merges the caller's own posts with the followed accounts' feeds.
func (a *appviewSvc) timeline(ctx context.Context, did, cursor string, limit int) ([]map[string]any, *string) {
	items, _ := a.buildFeed(ctx, did, "", 50, did, "")
	for _, fd := range a.followedDIDs(ctx, did) {
		items = append(items, a.remoteAuthorFeed(ctx, fd, 30)...)
	}
	a.loadViewerState(ctx, did).injectFeed(items)
	sort.SliceStable(items, func(i, j int) bool {
		return postCreatedAt(items[i]) > postCreatedAt(items[j])
	})
	if cursor != "" {
		kept := items[:0]
		for _, it := range items {
			if postCreatedAt(it) < cursor {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		last := postCreatedAt(items[len(items)-1])
		next = &last
	}
	return items, next
}

func HandleAppBskyGetTimeline(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}
		a := newAppview(store, mgr, cfg)
		feed, cursor := a.timeline(r.Context(), did, r.URL.Query().Get("cursor"), parseLimit(r, 50, 100))
		writeFeed(w, feed, cursor)
	}
}

func HandleAppBskyGetAuthorFeed(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := newAppview(store, mgr, cfg)
		viewerDID := optionalAuth(r, store)
		did, local, err := resolveActorDID(r.Context(), store, r.URL.Query().Get("actor"))
		if err != nil {
			writeAppviewError(w, err)
			return
		}
		if local {
			feed, cursor := a.buildFeed(r.Context(), did, r.URL.Query().Get("cursor"),
				parseLimit(r, 50, 100), viewerDID, r.URL.Query().Get("filter"))
			writeFeed(w, feed, cursor)
			return
		}
		if a.cfg.AppviewProxyURL == "" {
			writeFeed(w, []map[string]any{}, nil)
			return
		}
		status, body, err := proxyXrpcGet(r.Context(), a.cfg.AppviewProxyURL, "app.bsky.feed.getAuthorFeed", r.URL.RawQuery)
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

func HandleAppBskyGetPostThread(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := newAppview(store, mgr, cfg)
		viewerDID := optionalAuth(r, store)
		uri := r.URL.Query().Get("uri")
		did, rkey, ok := parsePostURI(uri)
		if !ok {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", "invalid uri")
			return
		}
		if a.isLocal(r.Context(), did) {
			xrpc.WriteJSON(w, map[string]any{"thread": a.threadView(r.Context(), did, rkey, 5, viewerDID)})
			return
		}
		if a.cfg.AppviewProxyURL == "" {
			xrpc.WriteJSON(w, map[string]any{
				"thread": map[string]any{
					"$type":    "app.bsky.feed.defs#notFoundPost",
					"uri":      uri,
					"notFound": true,
				},
			})
			return
		}
		status, body, err := proxyXrpcGet(r.Context(), a.cfg.AppviewProxyURL, "app.bsky.feed.getPostThread", r.URL.RawQuery)
		if err != nil || status != http.StatusOK {
			xrpc.WriteJSON(w, map[string]any{
				"thread": map[string]any{
					"$type":    "app.bsky.feed.defs#notFoundPost",
					"uri":      uri,
					"notFound": true,
				},
			})
			return
		}
		var out map[string]any
		if json.Unmarshal(body, &out) != nil {
			xrpc.WriteJSON(w, map[string]any{
				"thread": map[string]any{
					"$type":    "app.bsky.feed.defs#notFoundPost",
					"uri":      uri,
					"notFound": true,
				},
			})
			return
		}
		if thread, ok := out["thread"].(map[string]any); ok {
			a.loadViewerState(r.Context(), viewerDID).injectThread(thread)
		}
		xrpc.WriteJSON(w, out)
	}
}

func (a *appviewSvc) threadView(ctx context.Context, did, rkey string, depth int, viewerDID string) map[string]any {
	uri := "at://" + did + "/" + postCollection + "/" + rkey
	cidStr, value, err := a.mgr.GetRecord(ctx, did, postCollection, rkey)
	if err != nil {
		return map[string]any{
			"$type":    "app.bsky.feed.defs#notFoundPost",
			"uri":      uri,
			"notFound": true,
		}
	}
	thread := map[string]any{"post": a.postView(ctx, did, rkey, cidStr, value, viewerDID)}
	if depth > 0 {
		thread["replies"] = a.replyThreads(ctx, did, uri, depth-1, viewerDID)
	}
	return thread
}

func (a *appviewSvc) replyThreads(ctx context.Context, did, parentURI string, depth int, viewerDID string) []map[string]any {
	rows, err := a.store.DB.QueryContext(ctx,
		"SELECT rkey FROM repo_records WHERE did = ? AND collection = ? AND json_extract(value, '$.reply.parent.uri') = ? ORDER BY rkey ASC",
		did, postCollection, parentURI)
	if err != nil {
		return nil
	}

	var rkeys []string
	for rows.Next() {
		var rkey string
		if err := rows.Scan(&rkey); err == nil {
			rkeys = append(rkeys, rkey)
		}
	}
	_ = rows.Close()

	out := make([]map[string]any, 0, len(rkeys))
	for _, rkey := range rkeys {
		out = append(out, a.threadView(ctx, did, rkey, depth, viewerDID))
	}
	return out
}

func HandleAppBskyGetPosts(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := newAppview(store, mgr, cfg)
		viewerDID := optionalAuth(r, store)
		uris := strings.Split(r.URL.Query().Get("uris"), ",")

		var local, remote []string
		for _, uri := range uris {
			did, _, ok := parsePostURI(uri)
			if !ok {
				continue
			}
			if a.isLocal(r.Context(), did) {
				local = append(local, uri)
			} else {
				remote = append(remote, uri)
			}
		}

		posts := make([]map[string]any, 0, len(uris))
		for _, uri := range local {
			did, rkey, _ := parsePostURI(uri)
			cidStr, value, err := a.mgr.GetRecord(r.Context(), did, postCollection, rkey)
			if err != nil {
				continue
			}
			if pv := a.postView(r.Context(), did, rkey, cidStr, value, viewerDID); pv != nil {
				posts = append(posts, pv)
			}
		}
		if len(remote) > 0 && a.cfg.AppviewProxyURL != "" {
			status, body, err := proxyXrpcGet(r.Context(), a.cfg.AppviewProxyURL, "app.bsky.feed.getPosts",
				"uris="+url.QueryEscape(strings.Join(remote, ",")))
			if err == nil && status == http.StatusOK {
				var out struct {
					Posts []map[string]any `json:"posts"`
				}
				if json.Unmarshal(body, &out) == nil {
					vs := a.loadViewerState(r.Context(), viewerDID)
					for _, p := range out.Posts {
						vs.injectPostViewer(p)
					}
					posts = append(posts, out.Posts...)
				}
			}
		}
		xrpc.WriteJSON(w, map[string]any{"posts": posts})
	}
}

// localLikes returns the like records for a post hosted on this PDS.
func (a *appviewSvc) localLikes(ctx context.Context, uri string) []map[string]any {
	rows, err := a.store.DB.QueryContext(ctx,
		"SELECT did, value FROM repo_records WHERE collection = 'app.bsky.feed.like' AND json_extract(value, '$.subject.uri') = ? ORDER BY rkey DESC", uri)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var out []map[string]any
	for rows.Next() {
		var did string
		var value []byte
		if err := rows.Scan(&did, &value); err != nil {
			continue
		}
		var rec struct {
			CreatedAt string `json:"createdAt"`
		}
		_ = json.Unmarshal(value, &rec)
		actor := a.profileBasic(ctx, did, "")
		if actor == nil {
			continue
		}
		out = append(out, map[string]any{
			"indexedAt": rec.CreatedAt,
			"createdAt": rec.CreatedAt,
			"actor":     actor,
		})
	}
	return out
}

// localReposters returns the repost records for a post hosted on this PDS.
func (a *appviewSvc) localReposters(ctx context.Context, uri string) []map[string]any {
	rows, err := a.store.DB.QueryContext(ctx,
		"SELECT did FROM repo_records WHERE collection = 'app.bsky.feed.repost' AND json_extract(value, '$.subject.uri') = ? ORDER BY rkey DESC", uri)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var out []map[string]any
	for rows.Next() {
		var did string
		if err := rows.Scan(&did); err != nil {
			continue
		}
		if actor := a.profileBasic(ctx, did, ""); actor != nil {
			out = append(out, actor)
		}
	}
	return out
}

func HandleAppBskyGetLikes(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := newAppview(store, mgr, cfg)
		uri := r.URL.Query().Get("uri")
		if did, _, ok := parsePostURI(uri); ok && a.isLocal(r.Context(), did) {
			xrpc.WriteJSON(w, map[string]any{"uri": uri, "likes": a.localLikes(r.Context(), uri), "cursor": ""})
			return
		}
		if a.cfg.AppviewProxyURL == "" {
			xrpc.WriteJSON(w, map[string]any{"uri": uri, "likes": []any{}, "cursor": ""})
			return
		}
		proxyXrpc(w, r, a.cfg.AppviewProxyURL, "app.bsky.feed.getLikes")
	}
}

func HandleAppBskyGetRepostedBy(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := newAppview(store, mgr, cfg)
		uri := r.URL.Query().Get("uri")
		if did, _, ok := parsePostURI(uri); ok && a.isLocal(r.Context(), did) {
			xrpc.WriteJSON(w, map[string]any{"uri": uri, "repostedBy": a.localReposters(r.Context(), uri), "cursor": ""})
			return
		}
		if a.cfg.AppviewProxyURL == "" {
			xrpc.WriteJSON(w, map[string]any{"uri": uri, "repostedBy": []any{}, "cursor": ""})
			return
		}
		proxyXrpc(w, r, a.cfg.AppviewProxyURL, "app.bsky.feed.getRepostedBy")
	}
}

func HandleAppBskyGetFollows(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := newAppview(store, mgr, cfg)
		viewerDID := optionalAuth(r, store)
		did, local, err := resolveActorDID(r.Context(), store, r.URL.Query().Get("actor"))
		if err != nil {
			writeAppviewError(w, err)
			return
		}
		if local {
			subject := a.profileBasic(r.Context(), did, viewerDID)
			if subject == nil {
				xrpc.WriteXRPCError(w, http.StatusBadRequest, "ProfileNotFound", "profile not found")
				return
			}
			vs := a.loadViewerState(r.Context(), viewerDID)
			follows := make([]map[string]any, 0)
			for _, fd := range a.followedDIDs(r.Context(), did) {
				if p, ok := a.remoteProfile(r.Context(), fd); ok {
					vs.injectProfileViewer(p)
					follows = append(follows, p)
				}
			}
			xrpc.WriteJSON(w, map[string]any{"subject": subject, "follows": follows, "cursor": ""})
			return
		}
		if a.cfg.AppviewProxyURL == "" {
			xrpc.WriteJSON(w, map[string]any{"follows": []any{}, "cursor": ""})
			return
		}
		proxyXrpc(w, r, a.cfg.AppviewProxyURL, "app.bsky.graph.getFollows")
	}
}

func HandleAppBskyGetFollowers(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := newAppview(store, mgr, cfg)
		viewerDID := optionalAuth(r, store)
		did, local, err := resolveActorDID(r.Context(), store, r.URL.Query().Get("actor"))
		if err != nil {
			writeAppviewError(w, err)
			return
		}
		if local {
			subject := a.profileBasic(r.Context(), did, viewerDID)
			if subject == nil {
				xrpc.WriteXRPCError(w, http.StatusBadRequest, "ProfileNotFound", "profile not found")
				return
			}
			xrpc.WriteJSON(w, map[string]any{"subject": subject, "followers": []any{}, "cursor": ""})
			return
		}
		if a.cfg.AppviewProxyURL == "" {
			xrpc.WriteJSON(w, map[string]any{"followers": []any{}, "cursor": ""})
			return
		}
		proxyXrpc(w, r, a.cfg.AppviewProxyURL, "app.bsky.graph.getFollowers")
	}
}

func HandleAppBskyGetSuggestions(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.AppviewProxyURL == "" {
			xrpc.WriteJSON(w, map[string]any{"actors": []any{}})
			return
		}
		proxyXrpc(w, r, cfg.AppviewProxyURL, "app.bsky.actor.getSuggestions")
	}
}

func HandleAppBskyListNotifications(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := requireAuth(w, r, store); !ok {
			return
		}
		xrpc.WriteJSON(w, map[string]any{"notifications": []any{}, "cursor": ""})
	}
}
