package api

import (
	"context"
	"encoding/json"
	"errors"
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
)

func setupAppviewTest(t *testing.T) (*db.Store, *repo.Manager, *identity.Keys, string, string) {
	t.Helper()
	ctx := context.Background()
	store, err := db.Open(ctx, t.TempDir()+"/test.db", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	keys, err := identity.CreateDidWeb("alice.example.com", "https://alice.example.com")
	if err != nil {
		t.Fatal(err)
	}

	didDocJSON, _ := json.Marshal(keys.DidDoc)
	recoveryKey, _ := store.Box.Encrypt(keys.RecoveryKey.Bytes())
	signingKey, _ := store.Box.Encrypt(keys.SigningKey.Bytes())
	if _, err := store.DB.ExecContext(ctx,
		`INSERT INTO accounts (did, handle, email, password_hash, recovery_key, signing_key, tid_clock, created_at, did_doc)
		 VALUES (?, ?, '', '', ?, ?, 0, '', ?)`,
		keys.Did, "alice.example.com", recoveryKey, signingKey, string(didDocJSON)); err != nil {
		t.Fatal(err)
	}

	mgr := repo.NewManager(store)
	if err := mgr.CreateAccount(ctx, keys.Did); err != nil {
		t.Fatal(err)
	}

	dn, desc := "Alice", "hello from the test"
	profile := &bsky.ActorProfile{LexiconTypeID: "app.bsky.actor.profile", DisplayName: &dn, Description: &desc}
	if _, err := mgr.PutRecord(ctx, keys.Did, "app.bsky.actor.profile", "self", profile,
		[]byte(`{"$type":"app.bsky.actor.profile","displayName":"Alice","description":"hello from the test"}`)); err != nil {
		t.Fatal(err)
	}

	post1 := &bsky.FeedPost{LexiconTypeID: "app.bsky.feed.post", Text: "first", CreatedAt: "2026-08-15T10:00:00.000Z"}
	res1, err := mgr.CreateRecord(ctx, keys.Did, "app.bsky.feed.post", post1,
		[]byte(`{"$type":"app.bsky.feed.post","text":"first","createdAt":"2026-08-15T10:00:00.000Z"}`))
	if err != nil {
		t.Fatal(err)
	}

	reply := &bsky.FeedPost{
		LexiconTypeID: "app.bsky.feed.post",
		Text:          "reply",
		CreatedAt:     "2026-08-15T11:00:00.000Z",
		Reply: &bsky.FeedPost_ReplyRef{
			Root:   &atproto.RepoStrongRef{Uri: res1.URI, Cid: res1.CID},
			Parent: &atproto.RepoStrongRef{Uri: res1.URI, Cid: res1.CID},
		},
	}
	if _, err := mgr.CreateRecord(ctx, keys.Did, "app.bsky.feed.post", reply,
		[]byte(`{"$type":"app.bsky.feed.post","text":"reply","createdAt":"2026-08-15T11:00:00.000Z","reply":{"root":{"uri":"`+res1.URI+`","cid":"`+res1.CID+`"},"parent":{"uri":"`+res1.URI+`","cid":"`+res1.CID+`"}}}`)); err != nil {
		t.Fatal(err)
	}

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
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestAppviewGetProfile(t *testing.T) {
	t.Parallel()
	store, mgr, keys, _, _ := setupAppviewTest(t)
	cfg := &config.Config{}
	out := doAppviewGET(t, HandleAppBskyGetProfile(cfg, store, mgr), "?actor=alice.example.com", "")
	if out["did"] != keys.Did || out["handle"] != "alice.example.com" {
		t.Fatalf("unexpected profile identity: %v", out)
	}
	if out["displayName"] != "Alice" {
		t.Fatalf("displayName = %v", out["displayName"])
	}
	if out["postsCount"] != float64(2) {
		t.Fatalf("postsCount = %v", out["postsCount"])
	}
	if _, present := out["avatar"]; present {
		t.Fatalf("avatar should be omitted when empty: %v", out["avatar"])
	}
	if _, present := out["banner"]; present {
		t.Fatalf("banner should be omitted when empty: %v", out["banner"])
	}
}

func TestAppviewGetAuthorFeed(t *testing.T) {
	t.Parallel()
	store, mgr, keys, uri1, _ := setupAppviewTest(t)
	cfg := &config.Config{}
	out := doAppviewGET(t, HandleAppBskyGetAuthorFeed(cfg, store, mgr), "?actor="+keys.Did, "")
	feed, _ := out["feed"].([]any)
	if len(feed) != 2 {
		t.Fatalf("feed length = %d", len(feed))
	}
	// Newest first: the reply post has the later TID.
	first := feed[0].(map[string]any)["post"].(map[string]any)
	if first["uri"] == uri1 {
		t.Fatalf("expected newest (reply) first, got %v", first["uri"])
	}
	if _, ok := first["author"].(map[string]any)["handle"]; !ok {
		t.Fatalf("missing author: %v", first["author"])
	}
}

func TestAppviewGetTimeline(t *testing.T) {
	t.Parallel()
	store, mgr, keys, _, _ := setupAppviewTest(t)
	cfg := &config.Config{}
	token, err := mintAccessJWT(store.Box.HMACKey(), keys.Did)
	if err != nil {
		t.Fatal(err)
	}
	out := doAppviewGET(t, HandleAppBskyGetTimeline(cfg, store, mgr), "", token)
	feed, _ := out["feed"].([]any)
	if len(feed) != 2 {
		t.Fatalf("timeline length = %d", len(feed))
	}
}

func TestAppviewGetPostThread(t *testing.T) {
	t.Parallel()
	store, mgr, _, uri1, _ := setupAppviewTest(t)
	cfg := &config.Config{}
	out := doAppviewGET(t, HandleAppBskyGetPostThread(cfg, store, mgr), "?uri="+uri1, "")
	thread, _ := out["thread"].(map[string]any)
	replies, _ := thread["replies"].([]any)
	if len(replies) != 1 {
		t.Fatalf("expected 1 reply, got %d", len(replies))
	}
}

func TestAppviewPreferencesRoundTrip(t *testing.T) {
	t.Parallel()
	store, mgr, keys, _, _ := setupAppviewTest(t)
	cfg := &config.Config{}
	token, err := mintAccessJWT(store.Box.HMACKey(), keys.Did)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/xrpc/test",
		strings.NewReader(`{"preferences":[{"$type":"app.bsky.actor.defs#savedFeedsPref","pinned":[],"saved":[]}]}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	HandleAppBskyPutPreferences(cfg, store, mgr)(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("put status %d: %s", rr.Code, rr.Body.String())
	}

	out := doAppviewGET(t, HandleAppBskyGetPreferences(cfg, store, mgr), "", token)
	prefs, _ := out["preferences"].([]any)
	if len(prefs) != 1 {
		t.Fatalf("preferences length = %d", len(prefs))
	}
}

func TestProfileAvatarResolution(t *testing.T) {
	t.Parallel()
	store, mgr, keys, _, _ := setupAppviewTest(t)
	ctx := context.Background()
	cfg := &config.Config{PublicURL: "https://pds.example.com"}

	if _, err := store.DB.ExecContext(ctx,
		"UPDATE repo_records SET value = ? WHERE did = ? AND collection = 'app.bsky.actor.profile' AND rkey = 'self'",
		[]byte(`{"$type":"app.bsky.actor.profile","displayName":"Alice","avatar":{"$type":"blob","ref":{"$link":"bafkreitest"},"mimeType":"image/jpeg","size":123}}`),
		keys.Did); err != nil {
		t.Fatal(err)
	}

	out := doAppviewGET(t, HandleAppBskyGetProfile(cfg, store, mgr), "?actor="+keys.Did, "")
	want := "https://pds.example.com/xrpc/com.atproto.sync.getBlob?did=did%3Aweb%3Aalice.example.com&cid=bafkreitest"
	if out["avatar"] != want {
		t.Fatalf("avatar = %v, want %v", out["avatar"], want)
	}
}

func TestResolveActorDID(t *testing.T) {
	t.Parallel()
	store, _, keys, _, _ := setupAppviewTest(t)
	ctx := context.Background()

	if did, local, err := resolveActorDID(ctx, store, keys.Did); err != nil || !local || did != keys.Did {
		t.Fatalf("local DID: got did=%q local=%v err=%v", did, local, err)
	}
	if did, local, err := resolveActorDID(ctx, store, "alice.example.com"); err != nil || !local || did != keys.Did {
		t.Fatalf("local handle: got did=%q local=%v err=%v", did, local, err)
	}
	if _, _, err := resolveActorDID(ctx, store, ""); !errors.Is(err, errActorRequired) {
		t.Fatalf("empty actor: err=%v", err)
	}
}
