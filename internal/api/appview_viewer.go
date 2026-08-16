package api

import (
	"context"
	"maps"
	"net/http"

	"github.com/alesr/pocketPDS/internal/db"
)

// optionalAuth returns the authenticated DID when a valid bearer token is
// present, otherwise "" (anonymous viewer).
func optionalAuth(r *http.Request, store *db.Store) string {
	token, ok := bearerToken(r)
	if !ok {
		return ""
	}
	did, err := parseAccessJWT(store.Box.HMACKey(), token)
	if err != nil {
		return ""
	}
	return did
}

// recordForSubject finds the rkey of a record the viewer owns that references a
// subject (via a JSON path, e.g. "$.subject.uri" or "$.subject").
func (a *appviewSvc) recordForSubject(ctx context.Context, did, collection, path, want string) (string, bool) {
	var rkey string
	err := a.store.DB.QueryRowContext(ctx,
		"SELECT rkey FROM repo_records WHERE did = ? AND collection = ? AND json_extract(value, ?) = ? LIMIT 1",
		did, collection, path, want).Scan(&rkey)
	if err != nil {
		return "", false
	}
	return rkey, true
}

func (a *appviewSvc) likeURI(ctx context.Context, did, postURI string) (string, bool) {
	rkey, ok := a.recordForSubject(ctx, did, "app.bsky.feed.like", "$.subject.uri", postURI)
	if !ok {
		return "", false
	}
	return "at://" + did + "/app.bsky.feed.like/" + rkey, true
}

func (a *appviewSvc) repostURI(ctx context.Context, did, postURI string) (string, bool) {
	rkey, ok := a.recordForSubject(ctx, did, "app.bsky.feed.repost", "$.subject.uri", postURI)
	if !ok {
		return "", false
	}
	return "at://" + did + "/app.bsky.feed.repost/" + rkey, true
}

func (a *appviewSvc) blockURI(ctx context.Context, did, subject string) (string, bool) {
	rkey, ok := a.recordForSubject(ctx, did, "app.bsky.graph.block", "$.subject", subject)
	if !ok {
		return "", false
	}
	return "at://" + did + "/app.bsky.graph.block/" + rkey, true
}

func (a *appviewSvc) hasFollow(ctx context.Context, did, subject string) bool {
	_, ok := a.recordForSubject(ctx, did, "app.bsky.graph.follow", "$.subject", subject)
	return ok
}

func (a *appviewSvc) isMuted(ctx context.Context, did, subject string) bool {
	var one int
	return a.store.DB.QueryRowContext(ctx,
		"SELECT 1 FROM mutes WHERE did = ? AND subject = ?", did, subject).Scan(&one) == nil
}

// postViewer builds the viewer state for a post, as seen by viewerDID.
func (a *appviewSvc) postViewer(ctx context.Context, viewerDID, postURI string) map[string]any {
	v := map[string]any{}
	if viewerDID == "" {
		return v
	}
	if uri, ok := a.likeURI(ctx, viewerDID, postURI); ok {
		v["like"] = uri
	}
	if uri, ok := a.repostURI(ctx, viewerDID, postURI); ok {
		v["repost"] = uri
	}
	return v
}

// profileViewer builds the viewer state for a profile, as seen by viewerDID.
func (a *appviewSvc) profileViewer(ctx context.Context, viewerDID, subjectDID string) map[string]any {
	v := map[string]any{}
	if viewerDID == "" {
		return v
	}
	if a.hasFollow(ctx, viewerDID, subjectDID) {
		v["following"] = "at://" + subjectDID
	}
	if uri, ok := a.blockURI(ctx, viewerDID, subjectDID); ok {
		v["blocking"] = uri
	}
	if a.isMuted(ctx, viewerDID, subjectDID) {
		v["muted"] = true
	}
	return v
}

// viewerState is a pre-loaded snapshot of a viewer's graph relationships, used
// to inject viewer state into proxied (remote) responses without per-item
// queries.
type viewerState struct {
	likes   map[string]string
	reposts map[string]string
	follows map[string]bool
	blocks  map[string]string
	mutes   map[string]bool
}

func (a *appviewSvc) loadViewerState(ctx context.Context, did string) *viewerState {
	vs := &viewerState{
		likes:   map[string]string{},
		reposts: map[string]string{},
		follows: map[string]bool{},
		blocks:  map[string]string{},
		mutes:   map[string]bool{},
	}
	if did == "" {
		return vs
	}

	rows, err := a.store.DB.QueryContext(ctx,
		"SELECT rkey, json_extract(value, '$.subject.uri') FROM repo_records WHERE did = ? AND collection = 'app.bsky.feed.like' AND json_extract(value, '$.subject.uri') IS NOT NULL", did)
	if err == nil {
		for rows.Next() {
			var rkey, uri string
			if rows.Scan(&rkey, &uri) == nil {
				vs.likes[uri] = "at://" + did + "/app.bsky.feed.like/" + rkey
			}
		}
		_ = rows.Close()
	}

	rows, err = a.store.DB.QueryContext(ctx,
		"SELECT rkey, json_extract(value, '$.subject.uri') FROM repo_records WHERE did = ? AND collection = 'app.bsky.feed.repost' AND json_extract(value, '$.subject.uri') IS NOT NULL", did)
	if err == nil {
		for rows.Next() {
			var rkey, uri string
			if rows.Scan(&rkey, &uri) == nil {
				vs.reposts[uri] = "at://" + did + "/app.bsky.feed.repost/" + rkey
			}
		}
		_ = rows.Close()
	}

	rows, err = a.store.DB.QueryContext(ctx,
		"SELECT json_extract(value, '$.subject') FROM repo_records WHERE did = ? AND collection = 'app.bsky.graph.follow' AND json_extract(value, '$.subject') IS NOT NULL", did)
	if err == nil {
		for rows.Next() {
			var subject string
			if rows.Scan(&subject) == nil {
				vs.follows[subject] = true
			}
		}
		_ = rows.Close()
	}

	rows, err = a.store.DB.QueryContext(ctx,
		"SELECT rkey, json_extract(value, '$.subject') FROM repo_records WHERE did = ? AND collection = 'app.bsky.graph.block' AND json_extract(value, '$.subject') IS NOT NULL", did)
	if err == nil {
		for rows.Next() {
			var rkey, subject string
			if rows.Scan(&rkey, &subject) == nil {
				vs.blocks[subject] = "at://" + did + "/app.bsky.graph.block/" + rkey
			}
		}
		_ = rows.Close()
	}

	rows, err = a.store.DB.QueryContext(ctx, "SELECT subject FROM mutes WHERE did = ?", did)
	if err == nil {
		for rows.Next() {
			var subject string
			if rows.Scan(&subject) == nil {
				vs.mutes[subject] = true
			}
		}
		_ = rows.Close()
	}

	return vs
}

func mergeViewer(existing any) map[string]any {
	v := map[string]any{}
	if m, ok := existing.(map[string]any); ok {
		maps.Copy(v, m)
	}
	return v
}

func (vs *viewerState) injectPostViewer(post map[string]any) {
	if vs == nil {
		return
	}
	uri, _ := post["uri"].(string)
	if uri == "" {
		return
	}
	v := mergeViewer(post["viewer"])
	if likeURI, ok := vs.likes[uri]; ok {
		v["like"] = likeURI
	}
	if repostURI, ok := vs.reposts[uri]; ok {
		v["repost"] = repostURI
	}
	post["viewer"] = v
}

func (vs *viewerState) injectProfileViewer(profile map[string]any) {
	if vs == nil {
		return
	}
	did, _ := profile["did"].(string)
	if did == "" {
		return
	}
	v := mergeViewer(profile["viewer"])
	if vs.follows[did] {
		v["following"] = "at://" + did
	}
	if uri, ok := vs.blocks[did]; ok {
		v["blocking"] = uri
	}
	if vs.mutes[did] {
		v["muted"] = true
	}
	profile["viewer"] = v
}

func (vs *viewerState) injectFeed(feed []map[string]any) {
	for _, item := range feed {
		if post, ok := item["post"].(map[string]any); ok {
			vs.injectPostViewer(post)
		}
	}
}

func (vs *viewerState) injectThread(thread map[string]any) {
	if post, ok := thread["post"].(map[string]any); ok {
		vs.injectPostViewer(post)
	}
	if replies, ok := thread["replies"].([]any); ok {
		for _, r := range replies {
			if m, ok := r.(map[string]any); ok {
				vs.injectThread(m)
			}
		}
	}
}
