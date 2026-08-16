package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alesr/pocketPDS/internal/config"
	"github.com/alesr/pocketPDS/internal/db"
)

func insertIndexRecord(t *testing.T, ctx context.Context, store *db.Store, did, collection, rkey, valueJSON string) {
	t.Helper()
	if _, err := store.DB.ExecContext(ctx,
		"INSERT INTO repo_records (did, collection, rkey, record_cid, value) VALUES (?, ?, ?, X'00', ?)",
		did, collection, rkey, []byte(valueJSON)); err != nil {
		t.Fatal(err)
	}
}

func TestViewerPostState(t *testing.T) {
	t.Parallel()
	store, mgr, keys, uri1, _ := setupAppviewTest(t)
	ctx := context.Background()
	cfg := &config.Config{}

	insertIndexRecord(t, ctx, store, keys.Did, "app.bsky.feed.like", "0000000000like1",
		`{"$type":"app.bsky.feed.like","subject":{"uri":"`+uri1+`","cid":"cid"},"createdAt":"2026-08-15T12:00:00.000Z"}`)

	token, err := mintAccessJWT(store.Box.HMACKey(), keys.Did)
	if err != nil {
		t.Fatal(err)
	}
	out := doAppviewGET(t, HandleAppBskyGetAuthorFeed(cfg, store, mgr), "?actor="+keys.Did, token)
	feed, _ := out["feed"].([]any)
	for _, it := range feed {
		post, _ := it.(map[string]any)["post"].(map[string]any)
		if post["uri"] != uri1 {
			continue
		}
		viewer, _ := post["viewer"].(map[string]any)
		if viewer["like"] != "at://"+keys.Did+"/app.bsky.feed.like/0000000000like1" {
			t.Fatalf("expected viewer.like, got %v", viewer)
		}
		return
	}
	t.Fatalf("post %s not found in feed", uri1)
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
	if _, err := store.DB.ExecContext(ctx,
		"INSERT INTO mutes (did, subject, created_at) VALUES (?, ?, '2026-08-15T12:00:00.000Z')",
		keys.Did, subject); err != nil {
		t.Fatal(err)
	}

	a := newAppview(store, mgr, cfg)
	v := a.profileViewer(ctx, keys.Did, subject)
	if v["following"] != "at://"+subject {
		t.Fatalf("expected following, got %v", v)
	}
	if v["blocking"] != "at://"+keys.Did+"/app.bsky.graph.block/0000000000blck1" {
		t.Fatalf("expected blocking at-uri, got %v", v)
	}
	if v["muted"] != true {
		t.Fatalf("expected muted, got %v", v)
	}
}

func TestMuteUnmuteRoundTrip(t *testing.T) {
	t.Parallel()
	store, mgr, keys, _, _ := setupAppviewTest(t)
	cfg := &config.Config{}
	ctx := context.Background()
	subject := "did:plc:fake123"

	token, err := mintAccessJWT(store.Box.HMACKey(), keys.Did)
	if err != nil {
		t.Fatal(err)
	}

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/xrpc/test", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		HandleAppBskyMuteActor(cfg, store, mgr)(rr, req)
		return rr
	}

	if rr := post(`{"actor":"` + subject + `"}`); rr.Code != http.StatusOK {
		t.Fatalf("mute status %d: %s", rr.Code, rr.Body.String())
	}
	a := newAppview(store, mgr, cfg)
	if !a.isMuted(ctx, keys.Did, subject) {
		t.Fatal("expected muted after muteActor")
	}

	req := httptest.NewRequest(http.MethodPost, "/xrpc/test", strings.NewReader(`{"actor":"`+subject+`"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	HandleAppBskyUnmuteActor(cfg, store, mgr)(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unmute status %d: %s", rr.Code, rr.Body.String())
	}
	if a.isMuted(ctx, keys.Did, subject) {
		t.Fatal("expected unmuted after unmuteActor")
	}
}
