package repo

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/identity"
	"github.com/bluesky-social/indigo/api/bsky"
	atproto_repo "github.com/bluesky-social/indigo/atproto/repo"
	cid "github.com/ipfs/go-cid"
	cbor "github.com/ipfs/go-ipld-cbor"
	"github.com/multiformats/go-multihash"
	"github.com/stretchr/testify/require"
)

func TestImportRepoRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := db.Open(ctx, t.TempDir()+"/test.db", "test-secret")
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	keys, err := identity.CreateDidWeb("frank.example.com", "https://frank.example.com")
	require.NoError(t, err)
	insertAccount(t, ctx, store, keys, "frank.example.com")

	mgr := NewManager(store)
	require.NoError(t, mgr.CreateAccount(ctx, keys.Did))

	rec := &bsky.FeedPost{LexiconTypeID: "app.bsky.feed.post", Text: "import me", CreatedAt: "2026-08-15T15:00:00.000Z"}
	res, err := mgr.CreateRecord(ctx, keys.Did, "app.bsky.feed.post", rec,
		[]byte(`{"$type":"app.bsky.feed.post","text":"import me","createdAt":"2026-08-15T15:00:00.000Z"}`))
	require.NoError(t, err)

	head, rev, err := mgr.Head(ctx, keys.Did)
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, mgr.WriteRepoCAR(ctx, &buf, keys.Did, ""))

	// Clear the repo, then import the exported CAR back into the same account.
	require.NoError(t, mgr.DeleteAccount(ctx, keys.Did))
	require.NoError(t, mgr.ImportRepo(ctx, keys.Did, &buf))

	newHead, newRev, err := mgr.Head(ctx, keys.Did)
	require.NoError(t, err)
	require.Equal(t, head.String(), newHead.String())
	require.Equal(t, rev, newRev)

	rkey := strings.TrimPrefix(res.URI, "at://"+keys.Did+"/app.bsky.feed.post/")
	_, value, err := mgr.GetRecord(ctx, keys.Did, "app.bsky.feed.post", rkey)
	require.NoError(t, err)
	require.JSONEq(t, `{"$type":"app.bsky.feed.post","text":"import me","createdAt":"2026-08-15T15:00:00.000Z"}`, string(value))

	// The re-imported repo must still load through indigo's read path.
	var buf2 bytes.Buffer
	require.NoError(t, mgr.WriteRepoCAR(ctx, &buf2, keys.Did, ""))
	commit, _, err := atproto_repo.LoadRepoFromCAR(ctx, &buf2)
	require.NoError(t, err)
	require.Equal(t, keys.Did, commit.DID)
}

func TestRecordJSON(t *testing.T) {
	t.Parallel()
	mhbuf, err := multihash.Sum([]byte("x"), multihash.SHA2_256, -1)
	require.NoError(t, err)
	link := cid.NewCidV1(cid.Raw, mhbuf)

	obj := map[string]any{"$type": "app.bsky.feed.post", "text": "hi", "ref": link, "bytes": []byte{1, 2, 3}}
	data, err := cbor.DumpObject(obj)
	require.NoError(t, err)

	out, err := recordJSON(data)
	require.NoError(t, err)
	require.JSONEq(t, `{"$type":"app.bsky.feed.post","text":"hi","ref":{"$link":"`+link.String()+`"},"bytes":{"$bytes":"AQID"}}`, string(out))
}

func TestListReposByCollection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := db.Open(ctx, t.TempDir()+"/test.db", "test-secret")
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	keys, err := identity.CreateDidWeb("gina.example.com", "https://gina.example.com")
	require.NoError(t, err)
	insertAccount(t, ctx, store, keys, "gina.example.com")

	mgr := NewManager(store)
	require.NoError(t, mgr.CreateAccount(ctx, keys.Did))
	_, err = mgr.CreateRecord(ctx, keys.Did, "app.bsky.feed.post",
		&bsky.FeedPost{LexiconTypeID: "app.bsky.feed.post", Text: "hi", CreatedAt: "2026-08-15T16:00:00.000Z"},
		[]byte(`{"$type":"app.bsky.feed.post","text":"hi"}`))
	require.NoError(t, err)

	dids, next, err := mgr.ListReposByCollection(ctx, "app.bsky.feed.post", "", 50)
	require.NoError(t, err)
	require.Nil(t, next)
	require.Equal(t, []string{keys.Did}, dids)

	empty, next, err := mgr.ListReposByCollection(ctx, "app.bsky.graph.follow", "", 50)
	require.NoError(t, err)
	require.Nil(t, next)
	require.Empty(t, empty)
}
