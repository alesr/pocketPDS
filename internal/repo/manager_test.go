package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
)

func insertAccount(t *testing.T, ctx context.Context, store *db.Store, keys *identity.Keys, handle string) {
	t.Helper()
	didDocJSON, err := json.Marshal(keys.DidDoc)
	if err != nil {
		t.Fatal(err)
	}
	recoveryKey, err := store.Box.Encrypt(keys.RecoveryKey.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	signingKey, err := store.Box.Encrypt(keys.SigningKey.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.ExecContext(ctx,
		`INSERT INTO accounts (did, handle, email, password_hash, recovery_key, signing_key, tid_clock, created_at, did_doc)
		 VALUES (?, ?, '', '', ?, ?, 0, '', ?)`,
		keys.Did, handle, recoveryKey, signingKey, string(didDocJSON)); err != nil {
		t.Fatal(err)
	}
}

func TestCommitSignatureRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := db.Open(ctx, t.TempDir()+"/test.db", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	keys, err := identity.CreateDidWeb("alice.example.com", "https://alice.example.com")
	if err != nil {
		t.Fatal(err)
	}

	insertAccount(t, ctx, store, keys, "alice.example.com")

	mgr := NewManager(store)

	if err := mgr.CreateAccount(ctx, keys.Did); err != nil {
		t.Fatal(err)
	}

	rec := &bsky.FeedPost{
		LexiconTypeID: "app.bsky.feed.post",
		Text:          "hello pocketpds",
		CreatedAt:     "2026-08-15T10:00:00.000Z",
	}
	res, err := mgr.CreateRecord(ctx, keys.Did, "app.bsky.feed.post", rec,
		[]byte(`{"$type":"app.bsky.feed.post","text":"hello pocketpds","createdAt":"2026-08-15T10:00:00.000Z"}`))
	if err != nil {
		t.Fatal(err)
	}

	// Reopen the repo from the blockstore using indigo's own read path, then
	// verify the head commit signature against the DID document's signing key.
	var headBytes []byte
	if err := store.DB.QueryRowContext(ctx,
		"SELECT cid FROM repo_commits WHERE did = ? ORDER BY rev DESC LIMIT 1", keys.Did).Scan(&headBytes); err != nil {
		t.Fatal(err)
	}
	head, err := cid.Cast(headBytes)
	if err != nil {
		t.Fatal(err)
	}

	bs := &db.SQLBlockstore{DB: store.DB}
	r, err := atrepo.OpenRepo(ctx, bs, head)
	if err != nil {
		t.Fatal(err)
	}

	methods := keys.DidDoc["verificationMethod"].([]map[string]any)
	pub, err := atcrypto.ParsePublicMultibase(methods[0]["publicKeyMultibase"].(string))
	if err != nil {
		t.Fatal(err)
	}

	sc := r.SignedCommit()
	unsigned, err := sc.Unsigned().BytesForSigning()
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.HashAndVerify(unsigned, sc.Sig); err != nil {
		t.Fatalf("commit signature does not verify: %v", err)
	}

	// Cross-check the record is reachable through indigo's MST reader.
	rkey := strings.TrimPrefix(res.URI, "at://"+keys.Did+"/app.bsky.feed.post/")
	if _, _, err := r.GetRecord(ctx, "app.bsky.feed.post/"+rkey); err != nil {
		t.Fatalf("record not reachable via indigo MST: %v", err)
	}
}

func TestGetRepoCARRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := db.Open(ctx, t.TempDir()+"/test.db", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	keys, err := identity.CreateDidWeb("bob.example.com", "https://bob.example.com")
	if err != nil {
		t.Fatal(err)
	}

	insertAccount(t, ctx, store, keys, "bob.example.com")

	mgr := NewManager(store)
	if err := mgr.CreateAccount(ctx, keys.Did); err != nil {
		t.Fatal(err)
	}

	rec := &bsky.FeedPost{
		LexiconTypeID: "app.bsky.feed.post",
		Text:          "hello world",
		CreatedAt:     "2026-08-15T12:00:00.000Z",
	}
	res, err := mgr.CreateRecord(ctx, keys.Did, "app.bsky.feed.post", rec,
		[]byte(`{"$type":"app.bsky.feed.post","text":"hello world","createdAt":"2026-08-15T12:00:00.000Z"}`))
	if err != nil {
		t.Fatal(err)
	}

	// Serialize the repo as a full CAR and load it back through indigo's read
	// path — this is exactly what a relay does via com.atproto.sync.getRepo.
	var buf bytes.Buffer
	if err := mgr.WriteRepoCAR(ctx, &buf, keys.Did, ""); err != nil {
		t.Fatal(err)
	}

	commit, loaded, err := atproto_repo.LoadRepoFromCAR(ctx, &buf)
	if err != nil {
		t.Fatalf("relay failed to load getRepo CAR: %v", err)
	}
	if commit.DID != keys.Did {
		t.Fatalf("wrong commit DID: %q", commit.DID)
	}

	rkey := strings.TrimPrefix(res.URI, "at://"+keys.Did+"/app.bsky.feed.post/")
	gotCID, err := loaded.GetRecordCID(ctx, "app.bsky.feed.post", syntax.RecordKey(rkey))
	if err != nil {
		t.Fatalf("record missing from reloaded repo: %v", err)
	}
	if gotCID.String() != res.CID {
		t.Fatalf("record CID mismatch: got %s want %s", gotCID, res.CID)
	}
}

func TestGetRepoCARIncremental(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := db.Open(ctx, t.TempDir()+"/test.db", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	keys, err := identity.CreateDidWeb("carol.example.com", "https://carol.example.com")
	if err != nil {
		t.Fatal(err)
	}
	insertAccount(t, ctx, store, keys, "carol.example.com")

	mgr := NewManager(store)
	if err := mgr.CreateAccount(ctx, keys.Did); err != nil {
		t.Fatal(err)
	}

	_, err = mgr.CreateRecord(ctx, keys.Did, "app.bsky.feed.post",
		&bsky.FeedPost{LexiconTypeID: "app.bsky.feed.post", Text: "first", CreatedAt: "2026-08-15T12:00:00.000Z"},
		[]byte(`{"$type":"app.bsky.feed.post","text":"first"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, firstRev, err := mgr.Head(ctx, keys.Did)
	if err != nil {
		t.Fatal(err)
	}

	res, err := mgr.CreateRecord(ctx, keys.Did, "app.bsky.feed.post",
		&bsky.FeedPost{LexiconTypeID: "app.bsky.feed.post", Text: "second", CreatedAt: "2026-08-15T12:00:01.000Z"},
		[]byte(`{"$type":"app.bsky.feed.post","text":"second"}`))
	if err != nil {
		t.Fatal(err)
	}

	// Incremental CAR from firstRev must be non-empty and contain the new
	// record block (partial MST is tolerated by the relay).
	var buf bytes.Buffer
	if err := mgr.WriteRepoCAR(ctx, &buf, keys.Did, firstRev); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Fatal("incremental CAR is empty")
	}

	_, loaded, err := atproto_repo.LoadRepoFromCAR(ctx, &buf)
	if err != nil {
		t.Fatalf("relay failed to load incremental CAR: %v", err)
	}
	rkey := strings.TrimPrefix(res.URI, "at://"+keys.Did+"/app.bsky.feed.post/")
	gotCID, err := loaded.GetRecordCID(ctx, "app.bsky.feed.post", syntax.RecordKey(rkey))
	if err != nil {
		t.Fatalf("new record missing from incremental CAR: %v", err)
	}
	if gotCID.String() != res.CID {
		t.Fatalf("record CID mismatch: got %s want %s", gotCID, res.CID)
	}
}

// TestFirehoseFramesInterop verifies that frames emitted by PocketPDS
// deserialize correctly with indigo's own firehose reader (the format relays
// consume on com.atproto.sync.subscribeRepos).
func TestFirehoseFramesInterop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := db.Open(ctx, t.TempDir()+"/test.db", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	keys, err := identity.CreateDidWeb("dave.example.com", "https://dave.example.com")
	if err != nil {
		t.Fatal(err)
	}
	insertAccount(t, ctx, store, keys, "dave.example.com")

	mgr := NewManager(store)
	if err := mgr.CreateAccount(ctx, keys.Did); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.CreateRecord(ctx, keys.Did, "app.bsky.feed.post",
		&bsky.FeedPost{LexiconTypeID: "app.bsky.feed.post", Text: "firehose", CreatedAt: "2026-08-15T13:00:00.000Z"},
		[]byte(`{"$type":"app.bsky.feed.post","text":"firehose"}`)); err != nil {
		t.Fatal(err)
	}

	rows, err := store.DB.QueryContext(ctx, "SELECT frame FROM firehose_events ORDER BY seq ASC")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	var kinds []string
	for rows.Next() {
		var frame []byte
		if err := rows.Scan(&frame); err != nil {
			t.Fatal(err)
		}
		var xev events.XRPCStreamEvent
		if err := xev.Deserialize(bytes.NewReader(frame)); err != nil {
			t.Fatalf("indigo failed to deserialize frame: %v", err)
		}
		switch {
		case xev.RepoCommit != nil:
			kinds = append(kinds, "#commit")
		case xev.RepoIdentity != nil:
			kinds = append(kinds, "#identity")
		default:
			t.Fatalf("unexpected frame kind")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	// Expect: initial #commit, #identity, record #commit.
	if len(kinds) != 3 || kinds[0] != "#commit" || kinds[1] != "#identity" || kinds[2] != "#commit" {
		t.Fatalf("unexpected firehose sequence: %v", kinds)
	}
}

func TestApplyWritesAndSwap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := db.Open(ctx, t.TempDir()+"/test.db", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	keys, err := identity.CreateDidWeb("erin.example.com", "https://erin.example.com")
	if err != nil {
		t.Fatal(err)
	}
	insertAccount(t, ctx, store, keys, "erin.example.com")
	mgr := NewManager(store)
	if err := mgr.CreateAccount(ctx, keys.Did); err != nil {
		t.Fatal(err)
	}

	post := func(text string) *bsky.FeedPost {
		return &bsky.FeedPost{LexiconTypeID: "app.bsky.feed.post", Text: text, CreatedAt: "2026-08-15T14:00:00.000Z"}
	}

	// Batch: create two posts atomically (single commit).
	commitCID, rev, results, err := mgr.ApplyWrites(ctx, keys.Did, []Op{
		{Action: "create", Collection: "app.bsky.feed.post", Rec: post("one"), RecordJSON: []byte(`{"$type":"app.bsky.feed.post","text":"one"}`)},
		{Action: "create", Collection: "app.bsky.feed.post", Rec: post("two"), RecordJSON: []byte(`{"$type":"app.bsky.feed.post","text":"two"}`)},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if commitCID == "" || rev == "" {
		t.Fatal("missing commit metadata")
	}

	// swapCommit CAS: wrong head must fail.
	_, _, _, err = mgr.ApplyWrites(ctx, keys.Did, []Op{
		{Action: "create", Collection: "app.bsky.feed.post", Rec: post("three"), RecordJSON: []byte(`{"$type":"app.bsky.feed.post","text":"three"}`)},
	}, strPtr("bogus"))
	if err == nil || !errors.Is(err, ErrSwapCommitMismatch) {
		t.Fatalf("expected swapCommit mismatch, got %v", err)
	}

	// swapRecord CAS on a delete with wrong CID must fail.
	rkey := results[0].Rkey
	_, _, _, err = mgr.ApplyWrites(ctx, keys.Did, []Op{
		{Action: "delete", Collection: "app.bsky.feed.post", Rkey: rkey, SwapRecord: strPtr("bogus")},
	}, nil)
	if err == nil || !errors.Is(err, ErrSwapRecordMismatch) {
		t.Fatalf("expected swapRecord mismatch, got %v", err)
	}

	// Delete with the correct swapRecord must succeed.
	recordCID, _, err := mgr.GetRecord(ctx, keys.Did, "app.bsky.feed.post", rkey)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = mgr.ApplyWrites(ctx, keys.Did, []Op{
		{Action: "delete", Collection: "app.bsky.feed.post", Rkey: rkey, SwapRecord: &recordCID},
	}, nil)
	if err != nil {
		t.Fatalf("delete with correct swapRecord: %v", err)
	}
}

func strPtr(s string) *string { return &s }
