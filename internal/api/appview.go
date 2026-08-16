package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alesr/pocketPDS/internal/config"
	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/repo"
	"github.com/alesr/pocketPDS/internal/xrpc"
	"github.com/bluesky-social/indigo/atproto/syntax"
)

var errActorRequired = errors.New("actor is required")

// appviewSvc is a minimal, single-user AppView: it serves the app.bsky.* read
// endpoints for the account hosted on this PDS, and proxies network reads
// (search, remote profiles/feeds/threads) to a public AppView.
type appviewSvc struct {
	store *db.Store
	mgr   *repo.Manager
	cfg   *config.Config
}

func newAppview(store *db.Store, mgr *repo.Manager, cfg *config.Config) *appviewSvc {
	return &appviewSvc{store: store, mgr: mgr, cfg: cfg}
}

// resolveActorDID maps an actor identifier (DID or handle) to a DID and reports
// whether the account is hosted on this PDS.
func resolveActorDID(ctx context.Context, store *db.Store, actor string) (did string, local bool, err error) {
	if actor == "" {
		return "", false, errActorRequired
	}
	if strings.HasPrefix(actor, "did:") {
		var one int
		if store.DB.QueryRowContext(ctx, "SELECT 1 FROM accounts WHERE did = ?", actor).Scan(&one) == nil {
			return actor, true, nil
		}
		return actor, false, nil
	}
	var localDid string
	if store.DB.QueryRowContext(ctx, "SELECT did FROM accounts WHERE handle = ?", actor).Scan(&localDid) == nil {
		return localDid, true, nil
	}
	h, perr := syntax.ParseHandle(actor)
	if perr != nil {
		return "", false, fmt.Errorf("profile not found")
	}
	ident, lerr := dir.LookupHandle(ctx, h)
	if lerr != nil {
		return "", false, fmt.Errorf("profile not found")
	}
	return ident.DID.String(), false, nil
}

func (a *appviewSvc) isLocal(ctx context.Context, did string) bool {
	var one int
	return a.store.DB.QueryRowContext(ctx, "SELECT 1 FROM accounts WHERE did = ?", did).Scan(&one) == nil
}

func parseLimit(r *http.Request, def, max int) int {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// blobRef is the shape of a blob reference inside a record (e.g. the avatar or
// banner field of app.bsky.actor.profile).
type blobRef struct {
	Ref struct {
		Link string `json:"$link"`
	} `json:"ref"`
}

// profileRecord reads the account's app.bsky.actor.profile/self record. Avatar
// and banner are resolved from their blob refs into image URLs served by this
// PDS's getBlob endpoint.
func (a *appviewSvc) profileRecord(ctx context.Context, did string) (displayName, description, avatar, banner string, pinnedPost map[string]any) {
	_, value, err := a.mgr.GetRecord(ctx, did, "app.bsky.actor.profile", "self")
	if err != nil {
		return "", "", "", "", nil
	}
	var p struct {
		DisplayName string   `json:"displayName"`
		Description string   `json:"description"`
		Avatar      *blobRef `json:"avatar"`
		Banner      *blobRef `json:"banner"`
		PinnedPost  *struct {
			URI string `json:"uri"`
			CID string `json:"cid"`
		} `json:"pinnedPost"`
	}
	_ = json.Unmarshal(value, &p)
	if p.PinnedPost != nil && p.PinnedPost.URI != "" {
		pinnedPost = map[string]any{"uri": p.PinnedPost.URI, "cid": p.PinnedPost.CID}
	}
	return p.DisplayName, p.Description, a.blobURL(did, p.Avatar), a.blobURL(did, p.Banner), pinnedPost
}

// blobURL converts a blob ref into a URL served by this PDS's getBlob endpoint.
func (a *appviewSvc) blobURL(did string, ref *blobRef) string {
	if ref == nil || ref.Ref.Link == "" {
		return ""
	}
	return a.cfg.PublicURL + "/xrpc/com.atproto.sync.getBlob?did=" + url.QueryEscape(did) + "&cid=" + url.QueryEscape(ref.Ref.Link)
}

func (a *appviewSvc) profileBasic(ctx context.Context, did, viewerDID string) map[string]any {
	var handle string
	if err := a.store.DB.QueryRowContext(ctx,
		"SELECT handle FROM accounts WHERE did = ?", did).Scan(&handle); err != nil {
		return nil
	}
	displayName, _, avatar, _, _ := a.profileRecord(ctx, did)
	out := map[string]any{
		"did":    did,
		"handle": handle,
	}
	if displayName != "" {
		out["displayName"] = displayName
	}
	if avatar != "" {
		out["avatar"] = avatar
	}
	if viewerDID != "" {
		out["viewer"] = a.profileViewer(ctx, viewerDID, did)
	}
	return out
}

func (a *appviewSvc) profileDetailed(ctx context.Context, did, viewerDID string) map[string]any {
	var handle, createdAt string
	if err := a.store.DB.QueryRowContext(ctx,
		"SELECT handle, created_at FROM accounts WHERE did = ?", did).Scan(&handle, &createdAt); err != nil {
		return nil
	}
	displayName, description, avatar, banner, pinnedPost := a.profileRecord(ctx, did)

	posts := a.countCollection(ctx, did, "app.bsky.feed.post")
	follows := a.countCollection(ctx, did, "app.bsky.graph.follow")
	followers := a.countFollowers(ctx, did)

	indexedAt := createdAt
	if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
		indexedAt = t.UTC().Format("2006-01-02T15:04:05.000Z")
	}

	out := map[string]any{
		"did":            did,
		"handle":         handle,
		"followersCount": followers,
		"followsCount":   follows,
		"postsCount":     posts,
		"indexedAt":      indexedAt,
		"viewer":         a.profileViewer(ctx, viewerDID, did),
		"labels":         []any{},
	}
	if displayName != "" {
		out["displayName"] = displayName
	}
	if description != "" {
		out["description"] = description
	}
	if avatar != "" {
		out["avatar"] = avatar
	}
	if banner != "" {
		out["banner"] = banner
	}
	if pinnedPost != nil {
		out["pinnedPost"] = pinnedPost
	}
	return out
}

// remoteProfile fetches a profile from the public AppView.
func (a *appviewSvc) remoteProfile(ctx context.Context, did string) (map[string]any, bool) {
	if a.cfg.AppviewProxyURL == "" {
		return nil, false
	}
	status, body, err := proxyXrpcGet(ctx, a.cfg.AppviewProxyURL, "app.bsky.actor.getProfile",
		"actor="+url.QueryEscape(did))
	if err != nil || status != http.StatusOK {
		return nil, false
	}
	var out map[string]any
	if json.Unmarshal(body, &out) != nil {
		return nil, false
	}
	return out, true
}

func (a *appviewSvc) countCollection(ctx context.Context, did, collection string) int {
	var n int
	_ = a.store.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM repo_records WHERE did = ? AND collection = ?", did, collection).Scan(&n)
	return n
}

func (a *appviewSvc) countFollowers(ctx context.Context, did string) int {
	var n int
	_ = a.store.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM repo_records WHERE collection = 'app.bsky.graph.follow' AND json_extract(value, '$.subject') = ?",
		did).Scan(&n)
	return n
}

func (a *appviewSvc) countMatches(ctx context.Context, did, collection, path, want string) int {
	var n int
	_ = a.store.DB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM repo_records WHERE did = ? AND collection = ? AND json_extract(value, ?) = ?",
		did, collection, path, want).Scan(&n)
	return n
}

func HandleAppBskyGetProfile(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := newAppview(store, mgr, cfg)
		viewerDID := optionalAuth(r, store)
		did, local, err := resolveActorDID(r.Context(), store, r.URL.Query().Get("actor"))
		if err != nil {
			writeAppviewError(w, err)
			return
		}
		if local {
			profile := a.profileDetailed(r.Context(), did, viewerDID)
			if profile == nil {
				xrpc.WriteXRPCError(w, http.StatusBadRequest, "ProfileNotFound", "profile not found")
				return
			}
			xrpc.WriteJSON(w, profile)
			return
		}
		profile, ok := a.remoteProfile(r.Context(), did)
		if !ok {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "ProfileNotFound", "profile not found")
			return
		}
		a.loadViewerState(r.Context(), viewerDID).injectProfileViewer(profile)
		xrpc.WriteJSON(w, profile)
	}
}

func HandleAppBskyGetProfiles(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := newAppview(store, mgr, cfg)
		viewerDID := optionalAuth(r, store)
		vs := a.loadViewerState(r.Context(), viewerDID)
		actors := strings.Split(r.URL.Query().Get("actors"), ",")
		profiles := make([]map[string]any, 0, len(actors))
		for _, actor := range actors {
			did, local, err := resolveActorDID(r.Context(), store, actor)
			if err != nil {
				continue
			}
			if local {
				if p := a.profileDetailed(r.Context(), did, viewerDID); p != nil {
					profiles = append(profiles, p)
				}
				continue
			}
			if p, ok := a.remoteProfile(r.Context(), did); ok {
				vs.injectProfileViewer(p)
				profiles = append(profiles, p)
			}
		}
		xrpc.WriteJSON(w, map[string]any{"profiles": profiles})
	}
}

func HandleAppBskyGetPreferences(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}
		a := newAppview(store, mgr, cfg)
		prefs := a.loadPreferences(r.Context(), did)
		xrpc.WriteJSON(w, map[string]any{"preferences": prefs})
	}
}

func HandleAppBskyPutPreferences(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}
		var in struct {
			Preferences json.RawMessage `json:"preferences"`
		}
		if xrpc.DecodeBody(w, r, &in) != nil {
			return
		}
		var arr []any
		if err := json.Unmarshal(in.Preferences, &arr); err != nil || arr == nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", "preferences must be an array")
			return
		}
		a := newAppview(store, mgr, cfg)
		if err := a.savePreferences(r.Context(), did, in.Preferences); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		xrpc.WriteJSON(w, map[string]any{})
	}
}

func (a *appviewSvc) loadPreferences(ctx context.Context, did string) []any {
	var raw []byte
	if err := a.store.DB.QueryRowContext(ctx,
		"SELECT prefs FROM app_preferences WHERE did = ?", did).Scan(&raw); err != nil {
		return []any{}
	}
	var prefs []any
	if err := json.Unmarshal(raw, &prefs); err != nil {
		return []any{}
	}
	return prefs
}

func (a *appviewSvc) savePreferences(ctx context.Context, did string, raw []byte) error {
	_, err := a.store.DB.ExecContext(ctx,
		`INSERT INTO app_preferences (did, prefs) VALUES (?, ?)
		 ON CONFLICT(did) DO UPDATE SET prefs = excluded.prefs`, did, raw)
	return err
}

func writeAppviewError(w http.ResponseWriter, err error) {
	if errors.Is(err, errActorRequired) {
		xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", "actor is required")
		return
	}
	xrpc.WriteXRPCError(w, http.StatusBadRequest, "ProfileNotFound", "profile not found")
}
