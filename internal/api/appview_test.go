package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alesr/pocketPDS/internal/config"
	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/identity"
	"github.com/alesr/pocketPDS/internal/repo"
	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAppviewTest(t *testing.T) (*db.Store, *repo.Manager, *identity.Keys, string, string) {
	t.Helper()
	ctx := context.Background()
	store, err := db.Open(ctx, t.TempDir()+"/test.db", "test-secret")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	keys, err := identity.CreateDidWeb("alice.example.com", "https://alice.example.com")
	require.NoError(t, err)

	didDocJSON, _ := json.Marshal(keys.DidDoc)
	recoveryKey, _ := store.Box.Encrypt(keys.RecoveryKey.Bytes())
	signingKey, _ := store.Box.Encrypt(keys.SigningKey.Bytes())
	_, err = store.DB.ExecContext(ctx,
		`INSERT INTO accounts (did, handle, email, password_hash, recovery_key, signing_key, tid_clock, created_at, did_doc)
		 VALUES (?, ?, '', '', ?, ?, 0, '', ?)`,
		keys.Did, "alice.example.com", recoveryKey, signingKey, string(didDocJSON))
	require.NoError(t, err)

	mgr := repo.NewManager(store)
	require.NoError(t, mgr.CreateAccount(ctx, keys.Did))

	dn, desc := "Alice", "hello from the test"
	profile := &bsky.ActorProfile{LexiconTypeID: "app.bsky.actor.profile", DisplayName: &dn, Description: &desc}
	_, err = mgr.PutRecord(ctx, keys.Did, "app.bsky.actor.profile", "self", profile,
		[]byte(`{"$type":"app.bsky.actor.profile","displayName":"Alice","description":"hello from the test"}`))
	require.NoError(t, err)

	post1 := &bsky.FeedPost{LexiconTypeID: "app.bsky.feed.post", Text: "first", CreatedAt: "2026-08-15T10:00:00.000Z"}
	res1, err := mgr.CreateRecord(ctx, keys.Did, "app.bsky.feed.post", post1,
		[]byte(`{"$type":"app.bsky.feed.post","text":"first","createdAt":"2026-08-15T10:00:00.000Z"}`))
	require.NoError(t, err)

	reply := &bsky.FeedPost{
		LexiconTypeID: "app.bsky.feed.post",
		Text:          "reply",
		CreatedAt:     "2026-08-15T11:00:00.000Z",
		Reply: &bsky.FeedPost_ReplyRef{
			Root:   &atproto.RepoStrongRef{Uri: res1.URI, Cid: res1.CID},
			Parent: &atproto.RepoStrongRef{Uri: res1.URI, Cid: res1.CID},
		},
	}
	_, err = mgr.CreateRecord(ctx, keys.Did, "app.bsky.feed.post", reply,
		[]byte(`{"$type":"app.bsky.feed.post","text":"reply","createdAt":"2026-08-15T11:00:00.000Z","reply":{"root":{"uri":"`+res1.URI+`","cid":"`+res1.CID+`"},"parent":{"uri":"`+res1.URI+`","cid":"`+res1.CID+`"}}}`))
	require.NoError(t, err)

	return store, mgr, keys, res1.URI, res1.CID
}

func doAppviewGET(t *testing.T, h http.HandlerFunc, query, auth string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/xrpc/test"+query, nil)
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	rr := httptest.NewRecorder()
	h(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())
	var out map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
	return out
}

func TestAppviewGetProfile(t *testing.T) {
	t.Parallel()
	store, mgr, keys, _, _ := setupAppviewTest(t)
	cfg := &config.Config{}
	out := doAppviewGET(t, HandleAppBskyGetProfile(cfg, store, mgr), "?actor=alice.example.com", "")
	assert.Equal(t, keys.Did, out["did"])
	assert.Equal(t, "alice.example.com", out["handle"])
	assert.Equal(t, "Alice", out["displayName"])
	assert.Equal(t, float64(2), out["postsCount"])
	assert.NotContains(t, out, "avatar", "avatar should be omitted when empty")
	assert.NotContains(t, out, "banner", "banner should be omitted when empty")
}

func TestAppviewGetAuthorFeed(t *testing.T) {
	t.Parallel()
	store, mgr, keys, uri1, _ := setupAppviewTest(t)
	cfg := &config.Config{}
	out := doAppviewGET(t, HandleAppBskyGetAuthorFeed(cfg, store, mgr), "?actor="+keys.Did, "")
	feed, _ := out["feed"].([]any)
	require.Len(t, feed, 2)
	// Newest first: the reply post has the later TID.
	first := feed[0].(map[string]any)["post"].(map[string]any)
	assert.NotEqual(t, uri1, first["uri"], "expected newest (reply) first")
	_, ok := first["author"].(map[string]any)["handle"]
	assert.True(t, ok, "missing author: %v", first["author"])
}

func TestAppviewGetTimeline(t *testing.T) {
	t.Parallel()
	store, mgr, keys, _, _ := setupAppviewTest(t)
	cfg := &config.Config{}
	token, err := mintAccessJWT(store.Box.HMACKey(), keys.Did)
	require.NoError(t, err)
	out := doAppviewGET(t, HandleAppBskyGetTimeline(cfg, store, mgr), "", token)
	feed, _ := out["feed"].([]any)
	require.Len(t, feed, 2)
}

func TestAppviewGetPostThread(t *testing.T) {
	t.Parallel()
	store, mgr, _, uri1, _ := setupAppviewTest(t)
	cfg := &config.Config{}
	out := doAppviewGET(t, HandleAppBskyGetPostThread(cfg, store, mgr), "?uri="+uri1, "")
	thread, _ := out["thread"].(map[string]any)
	replies, _ := thread["replies"].([]any)
	require.Len(t, replies, 1)
}

func TestAppviewPreferencesRoundTrip(t *testing.T) {
	t.Parallel()
	store, mgr, keys, _, _ := setupAppviewTest(t)
	cfg := &config.Config{}
	token, err := mintAccessJWT(store.Box.HMACKey(), keys.Did)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/xrpc/test",
		strings.NewReader(`{"preferences":[{"$type":"app.bsky.actor.defs#savedFeedsPref","pinned":[],"saved":[]}]}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	HandleAppBskyPutPreferences(cfg, store, mgr)(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, "put body: %s", rr.Body.String())

	out := doAppviewGET(t, HandleAppBskyGetPreferences(cfg, store, mgr), "", token)
	prefs, _ := out["preferences"].([]any)
	require.Len(t, prefs, 1)
}

func TestProfileAvatarResolution(t *testing.T) {
	t.Parallel()
	store, mgr, keys, _, _ := setupAppviewTest(t)
	ctx := context.Background()
	cfg := &config.Config{PublicURL: "https://pds.example.com"}

	_, err := store.DB.ExecContext(ctx,
		"UPDATE repo_records SET value = ? WHERE did = ? AND collection = 'app.bsky.actor.profile' AND rkey = 'self'",
		[]byte(`{"$type":"app.bsky.actor.profile","displayName":"Alice","avatar":{"$type":"blob","ref":{"$link":"bafkreitest"},"mimeType":"image/jpeg","size":123}}`),
		keys.Did)
	require.NoError(t, err)

	out := doAppviewGET(t, HandleAppBskyGetProfile(cfg, store, mgr), "?actor="+keys.Did, "")
	want := "https://pds.example.com/xrpc/com.atproto.sync.getBlob?did=did%3Aweb%3Aalice.example.com&cid=bafkreitest"
	assert.Equal(t, want, out["avatar"])
}

func TestResolveActorDID(t *testing.T) {
	t.Parallel()
	store, _, keys, _, _ := setupAppviewTest(t)
	ctx := context.Background()

	did, local, err := resolveActorDID(ctx, store, keys.Did)
	require.NoError(t, err)
	assert.True(t, local)
	assert.Equal(t, keys.Did, did)

	did, local, err = resolveActorDID(ctx, store, "alice.example.com")
	require.NoError(t, err)
	assert.True(t, local)
	assert.Equal(t, keys.Did, did)

	_, _, err = resolveActorDID(ctx, store, "")
	require.ErrorIs(t, err, errActorRequired)
}
