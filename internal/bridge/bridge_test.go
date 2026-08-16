package bridge

import (
	"context"
	"testing"

	"github.com/alesr/pocketPDS/internal/blob"
	"github.com/alesr/pocketPDS/internal/config"
	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/repo"
	"github.com/stretchr/testify/require"
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
	require.Equal(t, "at://did:plc:new/app.bsky.feed.post/1", root["uri"], "root uri not rewritten")

	// blob $link must be untouched
	img := rec["embed"].(map[string]any)["images"].([]any)[0].(map[string]any)["image"].(map[string]any)
	require.Equal(t, "bafkreiblob", blobLink(img), "blob link changed")
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
	require.NoError(t, walkBlobs(rec, func(blob map[string]any) error {
		links = append(links, blobLink(blob))
		return nil
	}))
	require.Equal(t, []string{"a", "b"}, links)
}

func TestConfigRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := db.Open(ctx, t.TempDir()+"/test.db", "test-secret")
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	svc := New(&config.Config{}, store, repo.NewManager(store), mustBlobs(t, store))

	require.NoError(t, svc.SetConfig(ctx, "alice.bsky.social", "app-pass-123"))
	handle, passwordSet, err := svc.Config(ctx)
	require.NoError(t, err)
	require.Equal(t, "alice.bsky.social", handle)
	require.True(t, passwordSet)
	pw, err := svc.password(ctx)
	require.NoError(t, err)
	require.Equal(t, "app-pass-123", pw)

	// update only the handle, keep the password
	require.NoError(t, svc.SetHandle(ctx, "bob.bsky.social"))
	pw, err = svc.password(ctx)
	require.NoError(t, err)
	require.Equal(t, "app-pass-123", pw)
}

func mustBlobs(t *testing.T, store *db.Store) *blob.Store {
	t.Helper()
	b, err := blob.New(t.TempDir(), store)
	require.NoError(t, err)
	return b
}
