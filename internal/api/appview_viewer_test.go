package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alesr/pocketPDS/internal/config"
	"github.com/alesr/pocketPDS/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func insertIndexRecord(t *testing.T, ctx context.Context, store *db.Store, did, collection, rkey, valueJSON string) {
	t.Helper()
	_, err := store.DB.ExecContext(ctx,
		"INSERT INTO repo_records (did, collection, rkey, record_cid, value) VALUES (?, ?, ?, X'00', ?)",
		did, collection, rkey, []byte(valueJSON))
	require.NoError(t, err)
}

func TestViewerPostState(t *testing.T) {
	t.Parallel()
	store, mgr, keys, uri1, _ := setupAppviewTest(t)
	ctx := context.Background()
	cfg := &config.Config{}

	insertIndexRecord(t, ctx, store, keys.Did, "app.bsky.feed.like", "0000000000like1",
		`{"$type":"app.bsky.feed.like","subject":{"uri":"`+uri1+`","cid":"cid"},"createdAt":"2026-08-15T12:00:00.000Z"}`)

	token, err := mintAccessJWT(store.Box.HMACKey(), keys.Did)
	require.NoError(t, err)
	out := doAppviewGET(t, HandleAppBskyGetAuthorFeed(cfg, store, mgr), "?actor="+keys.Did, token)
	feed, _ := out["feed"].([]any)
	for _, it := range feed {
		post, _ := it.(map[string]any)["post"].(map[string]any)
		if post["uri"] != uri1 {
			continue
		}
		viewer, _ := post["viewer"].(map[string]any)
		assert.Equal(t, "at://"+keys.Did+"/app.bsky.feed.like/0000000000like1", viewer["like"], "expected viewer.like")
		return
	}
	require.Fail(t, "post %s not found in feed", uri1)
}

func TestViewerProfileState(t *testing.T) {
	t.Parallel()
	store, mgr, keys, _, _ := setupAppviewTest(t)
	ctx := context.Background()
	cfg := &config.Config{}
	subject := "did:plc:fake123"

	insertIndexRecord(t, ctx, store, keys.Did, "app.bsky.graph.follow", "0000000000folw1",
		`{"$type":"app.bsky.graph.follow","subject":"`+subject+`","createdAt":"2026-08-15T12:00:00.000Z"}`)
	insertIndexRecord(t, ctx, store, keys.Did, "app.bsky.graph.block", "0000000000blck1",
		`{"$type":"app.bsky.graph.block","subject":"`+subject+`","createdAt":"2026-08-15T12:00:00.000Z"}`)
	_, err := store.DB.ExecContext(ctx,
		"INSERT INTO mutes (did, subject, created_at) VALUES (?, ?, '2026-08-15T12:00:00.000Z')",
		keys.Did, subject)
	require.NoError(t, err)

	a := newAppview(store, mgr, cfg)
	v := a.profileViewer(ctx, keys.Did, subject)
	assert.Equal(t, "at://"+subject, v["following"], "expected following")
	assert.Equal(t, "at://"+keys.Did+"/app.bsky.graph.block/0000000000blck1", v["blocking"], "expected blocking at-uri")
	assert.Equal(t, true, v["muted"], "expected muted")
}

func TestMuteUnmuteRoundTrip(t *testing.T) {
	t.Parallel()
	store, mgr, keys, _, _ := setupAppviewTest(t)
	cfg := &config.Config{}
	ctx := context.Background()
	subject := "did:plc:fake123"

	token, err := mintAccessJWT(store.Box.HMACKey(), keys.Did)
	require.NoError(t, err)

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/xrpc/test", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		HandleAppBskyMuteActor(cfg, store, mgr)(rr, req)
		return rr
	}

	rr := post(`{"actor":"` + subject + `"}`)
	require.Equal(t, http.StatusOK, rr.Code, "mute body: %s", rr.Body.String())
	a := newAppview(store, mgr, cfg)
	require.True(t, a.isMuted(ctx, keys.Did, subject), "expected muted after muteActor")

	req := httptest.NewRequest(http.MethodPost, "/xrpc/test", strings.NewReader(`{"actor":"`+subject+`"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	HandleAppBskyUnmuteActor(cfg, store, mgr)(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "unmute body: %s", rr.Body.String())
	require.False(t, a.isMuted(ctx, keys.Did, subject), "expected unmuted after unmuteActor")
}
