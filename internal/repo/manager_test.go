package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/identity"
	"github.com/bluesky-social/indigo/api/bsky"
	"github.com/bluesky-social/indigo/atproto/atcrypto"
	atproto_repo "github.com/bluesky-social/indigo/atproto/repo"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/indigo/events"
	atrepo "github.com/bluesky-social/indigo/repo"
	cid "github.com/ipfs/go-cid"
	"github.com/stretchr/testify/require"
)

func insertAccount(t *testing.T, ctx context.Context, store *db.Store, keys *identity.Keys, handle string) {
	t.Helper()
	didDocJSON, err := json.Marshal(keys.DidDoc)
	require.NoError(t, err)
	recoveryKey, err := store.Box.Encrypt(keys.RecoveryKey.Bytes())
	require.NoError(t, err)
	signingKey, err := store.Box.Encrypt(keys.SigningKey.Bytes())
	require.NoError(t, err)
	_, err = store.DB.ExecContext(ctx,
		`INSERT INTO accounts (did, handle, email, password_hash, recovery_key, signing_key, tid_clock, created_at, did_doc)
		 VALUES (?, ?, '', '', ?, ?, 0, '', ?)`,
		keys.Did, handle, recoveryKey, signingKey, string(didDocJSON))
	require.NoError(t, err)
}

func TestCommitSignatureRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := db.Open(ctx, t.TempDir()+"/test.db", "test-secret")
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	keys, err := identity.CreateDidWeb("alice.example.com", "https://alice.example.com")
	require.NoError(t, err)

	insertAccount(t, ctx, store, keys, "alice.example.com")

	mgr := NewManager(store)

	require.NoError(t, mgr.CreateAccount(ctx, keys.Did))

	rec := &bsky.FeedPost{
		LexiconTypeID: "app.bsky.feed.post",
		Text:          "hello pocketpds",
		CreatedAt:     "2026-08-15T10:00:00.000Z",
	}
	res, err := mgr.CreateRecord(ctx, keys.Did, "app.bsky.feed.post", rec,
		[]byte(`{"$type":"app.bsky.feed.post","text":"hello pocketpds","createdAt":"2026-08-15T10:00:00.000Z"}`))
	require.NoError(t, err)

	// Reopen the repo from the blockstore using indigo's own read path, then
	// verify the head commit signature against the DID document's signing key.
	var headBytes []byte
	err = store.DB.QueryRowContext(ctx,
		"SELECT cid FROM repo_commits WHERE did = ? ORDER BY rev DESC LIMIT 1", keys.Did).Scan(&headBytes)
	require.NoError(t, err)
	head, err := cid.Cast(headBytes)
	require.NoError(t, err)

	bs := &db.SQLBlockstore{DB: store.DB}
	r, err := atrepo.OpenRepo(ctx, bs, head)
	require.NoError(t, err)

	methods := keys.DidDoc["verificationMethod"].([]map[string]any)
	pub, err := atcrypto.ParsePublicMultibase(methods[0]["publicKeyMultibase"].(string))
	require.NoError(t, err)

	sc := r.SignedCommit()
	unsigned, err := sc.Unsigned().BytesForSigning()
	require.NoError(t, err)
	require.NoError(t, pub.HashAndVerify(unsigned, sc.Sig), "commit signature does not verify")

	// Cross-check the record is reachable through indigo's MST reader.
	rkey := strings.TrimPrefix(res.URI, "at://"+keys.Did+"/app.bsky.feed.post/")
	_, _, err = r.GetRecord(ctx, "app.bsky.feed.post/"+rkey)
	require.NoError(t, err, "record not reachable via indigo MST")
}

func TestGetRepoCARRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := db.Open(ctx, t.TempDir()+"/test.db", "test-secret")
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	keys, err := identity.CreateDidWeb("bob.example.com", "https://bob.example.com")
	require.NoError(t, err)

	insertAccount(t, ctx, store, keys, "bob.example.com")

	mgr := NewManager(store)
	require.NoError(t, mgr.CreateAccount(ctx, keys.Did))

	rec := &bsky.FeedPost{
		LexiconTypeID: "app.bsky.feed.post",
		Text:          "hello world",
		CreatedAt:     "2026-08-15T12:00:00.000Z",
	}
	res, err := mgr.CreateRecord(ctx, keys.Did, "app.bsky.feed.post", rec,
		[]byte(`{"$type":"app.bsky.feed.post","text":"hello world","createdAt":"2026-08-15T12:00:00.000Z"}`))
	require.NoError(t, err)

	// Serialize the repo as a full CAR and load it back through indigo's read
	// path — this is exactly what a relay does via com.atproto.sync.getRepo.
	var buf bytes.Buffer
	require.NoError(t, mgr.WriteRepoCAR(ctx, &buf, keys.Did, ""))

	commit, loaded, err := atproto_repo.LoadRepoFromCAR(ctx, &buf)
	require.NoError(t, err, "relay failed to load getRepo CAR")
	require.Equal(t, keys.Did, commit.DID, "wrong commit DID")

	rkey := strings.TrimPrefix(res.URI, "at://"+keys.Did+"/app.bsky.feed.post/")
	gotCID, err := loaded.GetRecordCID(ctx, "app.bsky.feed.post", syntax.RecordKey(rkey))
	require.NoError(t, err, "record missing from reloaded repo")
	require.Equal(t, res.CID, gotCID.String(), "record CID mismatch")
}

func TestGetRepoCARIncremental(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := db.Open(ctx, t.TempDir()+"/test.db", "test-secret")
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	keys, err := identity.CreateDidWeb("carol.example.com", "https://carol.example.com")
	require.NoError(t, err)
	insertAccount(t, ctx, store, keys, "carol.example.com")

	mgr := NewManager(store)
	require.NoError(t, mgr.CreateAccount(ctx, keys.Did))

	_, err = mgr.CreateRecord(ctx, keys.Did, "app.bsky.feed.post",
		&bsky.FeedPost{LexiconTypeID: "app.bsky.feed.post", Text: "first", CreatedAt: "2026-08-15T12:00:00.000Z"},
		[]byte(`{"$type":"app.bsky.feed.post","text":"first"}`))
	require.NoError(t, err)
	_, firstRev, err := mgr.Head(ctx, keys.Did)
	require.NoError(t, err)

	res, err := mgr.CreateRecord(ctx, keys.Did, "app.bsky.feed.post",
		&bsky.FeedPost{LexiconTypeID: "app.bsky.feed.post", Text: "second", CreatedAt: "2026-08-15T12:00:01.000Z"},
		[]byte(`{"$type":"app.bsky.feed.post","text":"second"}`))
	require.NoError(t, err)

	// Incremental CAR from firstRev must be non-empty and contain the new
	// record block (partial MST is tolerated by the relay).
	var buf bytes.Buffer
	require.NoError(t, mgr.WriteRepoCAR(ctx, &buf, keys.Did, firstRev))
	require.NotZero(t, buf.Len(), "incremental CAR is empty")

	_, loaded, err := atproto_repo.LoadRepoFromCAR(ctx, &buf)
	require.NoError(t, err, "relay failed to load incremental CAR")
	rkey := strings.TrimPrefix(res.URI, "at://"+keys.Did+"/app.bsky.feed.post/")
	gotCID, err := loaded.GetRecordCID(ctx, "app.bsky.feed.post", syntax.RecordKey(rkey))
	require.NoError(t, err, "new record missing from incremental CAR")
	require.Equal(t, res.CID, gotCID.String(), "record CID mismatch")
}

// TestFirehoseFramesInterop verifies that frames emitted by PocketPDS
// deserialize correctly with indigo's own firehose reader (the format relays
// consume on com.atproto.sync.subscribeRepos).
func TestFirehoseFramesInterop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := db.Open(ctx, t.TempDir()+"/test.db", "test-secret")
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	keys, err := identity.CreateDidWeb("dave.example.com", "https://dave.example.com")
	require.NoError(t, err)
	insertAccount(t, ctx, store, keys, "dave.example.com")

	mgr := NewManager(store)
	require.NoError(t, mgr.CreateAccount(ctx, keys.Did))
	_, err = mgr.CreateRecord(ctx, keys.Did, "app.bsky.feed.post",
		&bsky.FeedPost{LexiconTypeID: "app.bsky.feed.post", Text: "firehose", CreatedAt: "2026-08-15T13:00:00.000Z"},
		[]byte(`{"$type":"app.bsky.feed.post","text":"firehose"}`))
	require.NoError(t, err)

	rows, err := store.DB.QueryContext(ctx, "SELECT frame FROM firehose_events ORDER BY seq ASC")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	var kinds []string
	for rows.Next() {
		var frame []byte
		require.NoError(t, rows.Scan(&frame))
		var xev events.XRPCStreamEvent
		require.NoError(t, xev.Deserialize(bytes.NewReader(frame)), "indigo failed to deserialize frame")
		switch {
		case xev.RepoCommit != nil:
			kinds = append(kinds, "#commit")
		case xev.RepoIdentity != nil:
			kinds = append(kinds, "#identity")
		default:
			require.Fail(t, "unexpected frame kind")
		}
	}
	require.NoError(t, rows.Err())

	// Expect: initial #commit, #identity, record #commit.
	require.Equal(t, []string{"#commit", "#identity", "#commit"}, kinds, "unexpected firehose sequence")
}

func TestApplyWritesAndSwap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := db.Open(ctx, t.TempDir()+"/test.db", "test-secret")
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	keys, err := identity.CreateDidWeb("erin.example.com", "https://erin.example.com")
	require.NoError(t, err)
	insertAccount(t, ctx, store, keys, "erin.example.com")
	mgr := NewManager(store)
	require.NoError(t, mgr.CreateAccount(ctx, keys.Did))

	post := func(text string) *bsky.FeedPost {
		return &bsky.FeedPost{LexiconTypeID: "app.bsky.feed.post", Text: text, CreatedAt: "2026-08-15T14:00:00.000Z"}
	}

	// Batch: create two posts atomically (single commit).
	commitCID, rev, results, err := mgr.ApplyWrites(ctx, keys.Did, []Op{
		{Action: "create", Collection: "app.bsky.feed.post", Rec: post("one"), RecordJSON: []byte(`{"$type":"app.bsky.feed.post","text":"one"}`)},
		{Action: "create", Collection: "app.bsky.feed.post", Rec: post("two"), RecordJSON: []byte(`{"$type":"app.bsky.feed.post","text":"two"}`)},
	}, nil)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.NotEmpty(t, commitCID, "missing commit metadata")
	require.NotEmpty(t, rev, "missing commit metadata")

	// swapCommit CAS: wrong head must fail.
	_, _, _, err = mgr.ApplyWrites(ctx, keys.Did, []Op{
		{Action: "create", Collection: "app.bsky.feed.post", Rec: post("three"), RecordJSON: []byte(`{"$type":"app.bsky.feed.post","text":"three"}`)},
	}, new("bogus"))
	require.ErrorIs(t, err, ErrSwapCommitMismatch)

	// swapRecord CAS on a delete with wrong CID must fail.
	rkey := results[0].Rkey
	_, _, _, err = mgr.ApplyWrites(ctx, keys.Did, []Op{
		{Action: "delete", Collection: "app.bsky.feed.post", Rkey: rkey, SwapRecord: new("bogus")},
	}, nil)
	require.ErrorIs(t, err, ErrSwapRecordMismatch)

	// Delete with the correct swapRecord must succeed.
	recordCID, _, err := mgr.GetRecord(ctx, keys.Did, "app.bsky.feed.post", rkey)
	require.NoError(t, err)
	_, _, _, err = mgr.ApplyWrites(ctx, keys.Did, []Op{
		{Action: "delete", Collection: "app.bsky.feed.post", Rkey: rkey, SwapRecord: &recordCID},
	}, nil)
	require.NoError(t, err, "delete with correct swapRecord")
}
