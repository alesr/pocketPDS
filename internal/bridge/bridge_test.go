package bridge

import (
	"context"
	"testing"

	"github.com/alesr/pocketPDS/internal/blob"
	"github.com/alesr/pocketPDS/internal/config"
	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/repo"
)

func TestRewriteURIs(t *testing.T) {
	t.Parallel()
	rec := map[string]any{
		"$type": "app.bsky.feed.post",
		"text":  "hi",
		"reply": map[string]any{
			"root":   map[string]any{"uri": "at://did:web:old/app.bsky.feed.post/1", "cid": "cid1"},
			"parent": map[string]any{"uri": "at://did:web:old/app.bsky.feed.post/1", "cid": "cid1"},
		},
		"embed": map[string]any{
			"$type": "app.bsky.embed.images",
			"images": []any{
				map[string]any{"image": map[string]any{"$type": "blob", "ref": map[string]any{"$link": "bafkreiblob"}, "mimeType": "image/jpeg", "size": 1}},
			},
		},
	}
	rewriteURIs(rec, "did:web:old", "did:plc:new")
	reply := rec["reply"].(map[string]any)
	root := reply["root"].(map[string]any)
	if root["uri"] != "at://did:plc:new/app.bsky.feed.post/1" {
		t.Fatalf("root uri not rewritten: %v", root["uri"])
	}
	// blob $link must be untouched
	img := rec["embed"].(map[string]any)["images"].([]any)[0].(map[string]any)["image"].(map[string]any)
	if blobLink(img) != "bafkreiblob" {
		t.Fatalf("blob link changed: %v", blobLink(img))
	}
}

func TestWalkBlobs(t *testing.T) {
	t.Parallel()
	rec := map[string]any{
		"embed": map[string]any{
			"$type": "app.bsky.embed.images",
			"images": []any{
				map[string]any{"image": map[string]any{"$type": "blob", "ref": map[string]any{"$link": "a"}}},
				map[string]any{"image": map[string]any{"$type": "blob", "ref": map[string]any{"$link": "b"}}},
			},
		},
	}
	var links []string
	if err := walkBlobs(rec, func(blob map[string]any) error {
		links = append(links, blobLink(blob))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 || links[0] != "a" || links[1] != "b" {
		t.Fatalf("unexpected blob links: %v", links)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := db.Open(ctx, t.TempDir()+"/test.db", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	svc := New(&config.Config{}, store, repo.NewManager(store), mustBlobs(t, store))

	if err := svc.SetConfig(ctx, "alice.bsky.social", "app-pass-123"); err != nil {
		t.Fatal(err)
	}
	handle, passwordSet, err := svc.Config(ctx)
	if err != nil || handle != "alice.bsky.social" || !passwordSet {
		t.Fatalf("config: handle=%q set=%v err=%v", handle, passwordSet, err)
	}
	pw, err := svc.password(ctx)
	if err != nil || pw != "app-pass-123" {
		t.Fatalf("password decrypt: %q err=%v", pw, err)
	}

	// update only the handle, keep the password
	if err := svc.SetHandle(ctx, "bob.bsky.social"); err != nil {
		t.Fatal(err)
	}
	pw, err = svc.password(ctx)
	if err != nil || pw != "app-pass-123" {
		t.Fatalf("password lost after handle update: %q err=%v", pw, err)
	}
}

func mustBlobs(t *testing.T, store *db.Store) *blob.Store {
	t.Helper()
	b, err := blob.New(t.TempDir(), store)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
