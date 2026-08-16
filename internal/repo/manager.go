package repo

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alesr/pocketPDS/internal/blob"
	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/firehose"
	atproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/atcrypto"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	atrepo "github.com/bluesky-social/indigo/repo"
	cid "github.com/ipfs/go-cid"
)

// Manager orchestrates the indigo write path for local accounts. It caches
// open *atrepo.Repo handles in memory so each account's TID clock stays
// monotonic for the lifetime of the process (single-binary, single-host).
type Manager struct {
	store     *db.Store
	bs        *db.SQLBlockstore
	recBS     *recordingBlockstore
	emitter   *firehose.Emitter
	blobs     *blob.Store
	publicURL string

	mu    sync.Mutex
	repos map[string]*atrepo.Repo
}

func NewManager(store *db.Store) *Manager {
	bs := &db.SQLBlockstore{DB: store.DB}
	return &Manager{
		store:   store,
		bs:      bs,
		recBS:   &recordingBlockstore{inner: bs},
		emitter: firehose.NewEmitter(store.DB),
		repos:   make(map[string]*atrepo.Repo),
	}
}

func (m *Manager) Emitter() *firehose.Emitter { return m.emitter }

// SetBlobStore wires the blob store so account deletion can clean up files.
func (m *Manager) SetBlobStore(b *blob.Store) { m.blobs = b }

// SetPublicURL configures the PDS hostname used when notifying relays.
func (m *Manager) SetPublicURL(u string) { m.publicURL = u }

// NotifyRelays requests a crawl from every registered relay (best-effort).
func (m *Manager) NotifyRelays() { m.notifyRelays() }

// notifyRelays fire-and-forgets a requestCrawl to every registered relay.
func (m *Manager) notifyRelays() {
	if m.publicURL == "" {
		return
	}
	go func() {
		rows, err := m.store.DB.Query("SELECT hostname FROM relays")
		if err != nil {
			return
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var host string
			if rows.Scan(&host) != nil {
				continue
			}
			go notifyRelay(host, m.publicURL)
		}
	}()
}

func notifyRelay(host, pdsHost string) {
	body, _ := json.Marshal(map[string]string{"hostname": pdsHost})
	req, err := http.NewRequest(http.MethodPost, ensureScheme(host)+"/xrpc/com.atproto.sync.requestCrawl", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	_, _ = client.Do(req)
}

func ensureScheme(host string) string {
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return host
	}
	return "https://" + host
}

// EmitAccount publishes an #account firehose event (status change).
func (m *Manager) EmitAccount(ctx context.Context, did string, active bool, status *string) error {
	return m.emitter.Publish(ctx, "#account", func(seq int64) (firehose.Marshaler, error) {
		return &atproto.SyncSubscribeRepos_Account{
			Seq:    seq,
			Did:    did,
			Active: active,
			Status: status,
			Time:   firehoseTime(),
		}, nil
	})
}

// EmitIdentity publishes an #identity firehose event (handle change).
func (m *Manager) EmitIdentity(ctx context.Context, did, handle string) error {
	return m.emitter.Publish(ctx, "#identity", func(seq int64) (firehose.Marshaler, error) {
		return &atproto.SyncSubscribeRepos_Identity{
			Seq:    seq,
			Did:    did,
			Handle: &handle,
			Time:   firehoseTime(),
		}, nil
	})
}

// DeleteAccount removes all repo data for an account (accounts row is deleted
// by the caller, cascading auth_sessions and blob metadata).
func (m *Manager) DeleteAccount(ctx context.Context, did string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.repos, did)
	if m.blobs != nil {
		if err := m.blobs.DeleteForDID(ctx, did); err != nil {
			return err
		}
	}
	for _, stmt := range []string{
		"DELETE FROM repo_commits WHERE did = ?",
		"DELETE FROM repo_records WHERE did = ?",
		"DELETE FROM repo_block_revs WHERE did = ?",
	} {
		if _, err := m.store.DB.ExecContext(ctx, stmt, did); err != nil {
			return err
		}
	}
	return nil
}

// Sentinel errors surfaced to XRPC handlers for correct status codes.
var (
	ErrSwapCommitMismatch = errors.New("swapCommit mismatch")
	ErrSwapRecordMismatch = errors.New("swapRecord mismatch")
	ErrRecordExists       = errors.New("record already exists")
	ErrRecordNotFound     = errors.New("record not found")
)

type WriteResult struct {
	URI       string
	CID       string
	CommitCID string
	Rev       string
}

func (m *Manager) CreateAccount(ctx context.Context, did string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, err := m.open(ctx, did)
	if err != nil {
		return err
	}
	m.recBS.Begin()
	res, err := m.commit(ctx, did, r)
	if err != nil {
		return err
	}

	if err := m.emitCommit(ctx, did, res, nil); err != nil {
		return err
	}

	var handle string
	if err := m.store.DB.QueryRowContext(ctx,
		"SELECT handle FROM accounts WHERE did = ?", did).Scan(&handle); err != nil {
		return err
	}
	return m.emitter.Publish(ctx, "#identity", func(seq int64) (firehose.Marshaler, error) {
		return &atproto.SyncSubscribeRepos_Identity{
			Seq:    seq,
			Did:    did,
			Handle: &handle,
			Time:   firehoseTime(),
		}, nil
	})
}

// Op is a single mutation in a repo write.
type Op struct {
	Action     string // "create" | "update" | "delete"
	Collection string
	Rkey       string // optional for create
	Rec        atrepo.CborMarshaler
	RecordJSON []byte
	SwapRecord *string // compare-and-swap on the current record CID (update/delete)
}

// OpResult is the per-op outcome of a write.
type OpResult struct {
	Action string
	Rkey   string
	CID    string
}

func (m *Manager) CreateRecord(ctx context.Context, did, collection string, rec atrepo.CborMarshaler, recordJSON []byte) (*WriteResult, error) {
	return m.singleWrite(ctx, did, Op{Action: "create", Collection: collection, Rec: rec, RecordJSON: recordJSON})
}

func (m *Manager) PutRecord(ctx context.Context, did, collection, rkey string, rec atrepo.CborMarshaler, recordJSON []byte) (*WriteResult, error) {
	return m.singleWrite(ctx, did, Op{Action: "update", Collection: collection, Rkey: rkey, Rec: rec, RecordJSON: recordJSON})
}

func (m *Manager) DeleteRecord(ctx context.Context, did, collection, rkey string) (*WriteResult, error) {
	return m.singleWrite(ctx, did, Op{Action: "delete", Collection: collection, Rkey: rkey})
}

func (m *Manager) singleWrite(ctx context.Context, did string, op Op) (*WriteResult, error) {
	res, results, err := m.applyWrites(ctx, did, []Op{op}, nil)
	if err != nil {
		return nil, err
	}
	wr := &WriteResult{CommitCID: res.commitCID.String(), Rev: res.rev}
	if len(results) == 1 {
		wr.CID = results[0].CID
		if results[0].Action != "delete" {
			wr.URI = fmt.Sprintf("at://%s/%s/%s", did, op.Collection, results[0].Rkey)
		}
	}
	return wr, nil
}

// ApplyWrites applies a batch of mutations atomically (single commit) and
// returns the commit CID, rev, and per-op results.
func (m *Manager) ApplyWrites(ctx context.Context, did string, ops []Op, swapCommit *string) (string, string, []OpResult, error) {
	res, results, err := m.applyWrites(ctx, did, ops, swapCommit)
	if err != nil {
		return "", "", nil, err
	}
	return res.commitCID.String(), res.rev, results, nil
}

func (m *Manager) applyWrites(ctx context.Context, did string, ops []Op, swapCommit *string) (*commitResult, []OpResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, err := m.open(ctx, did)
	if err != nil {
		return nil, nil, err
	}

	if swapCommit != nil {
		head, err := m.headCID(ctx, did)
		if err != nil {
			return nil, nil, err
		}
		if head != *swapCommit {
			return nil, nil, fmt.Errorf("%w: current head is %q", ErrSwapCommitMismatch, head)
		}
	}

	m.recBS.Begin()

	type indexWrite struct {
		collection, rkey string
		cid              cid.Cid
		json             []byte
	}
	var fhOps []*atproto.SyncSubscribeRepos_RepoOp
	var results []OpResult
	var upserts []indexWrite
	var deletes [][2]string

	// cur tracks in-batch record state so ops within the same batch see each
	// other's changes (the DB index isn't updated until after the commit).
	cur := make(map[string]cid.Cid)

	for _, op := range ops {
		switch op.Action {
		case "create":
			if op.Rkey == "" {
				recordCID, rkey, err := r.CreateRecord(ctx, op.Collection, op.Rec)
				if err != nil {
					return nil, nil, fmt.Errorf("create record: %w", err)
				}
				upserts = append(upserts, indexWrite{op.Collection, rkey, recordCID, op.RecordJSON})
				link := lexutil.LexLink(recordCID)
				fhOps = append(fhOps, &atproto.SyncSubscribeRepos_RepoOp{Action: "create", Path: op.Collection + "/" + rkey, Cid: &link})
				results = append(results, OpResult{Action: "create", Rkey: rkey, CID: recordCID.String()})
			} else {
				if _, exists, err := m.batchRecordCID(ctx, cur, did, op.Collection, op.Rkey); err != nil {
					return nil, nil, err
				} else if exists {
					return nil, nil, fmt.Errorf("%w: %s/%s", ErrRecordExists, op.Collection, op.Rkey)
				}
				recordCID, err := r.PutRecord(ctx, op.Collection+"/"+op.Rkey, op.Rec)
				if err != nil {
					return nil, nil, fmt.Errorf("create record: %w", err)
				}
				cur[op.Collection+"/"+op.Rkey] = recordCID
				upserts = append(upserts, indexWrite{op.Collection, op.Rkey, recordCID, op.RecordJSON})
				link := lexutil.LexLink(recordCID)
				fhOps = append(fhOps, &atproto.SyncSubscribeRepos_RepoOp{Action: "create", Path: op.Collection + "/" + op.Rkey, Cid: &link})
				results = append(results, OpResult{Action: "create", Rkey: op.Rkey, CID: recordCID.String()})
			}
		case "update":
			prev, exists, err := m.batchRecordCID(ctx, cur, did, op.Collection, op.Rkey)
			if err != nil {
				return nil, nil, err
			}
			if op.SwapRecord != nil && (!exists || prev.String() != *op.SwapRecord) {
				return nil, nil, fmt.Errorf("%w: %s/%s", ErrSwapRecordMismatch, op.Collection, op.Rkey)
			}
			recordCID, err := r.PutRecord(ctx, op.Collection+"/"+op.Rkey, op.Rec)
			if err != nil {
				return nil, nil, fmt.Errorf("update record: %w", err)
			}
			cur[op.Collection+"/"+op.Rkey] = recordCID
			upserts = append(upserts, indexWrite{op.Collection, op.Rkey, recordCID, op.RecordJSON})
			link := lexutil.LexLink(recordCID)
			action := "create"
			var prevLink *lexutil.LexLink
			if exists {
				action = "update"
				p := lexutil.LexLink(prev)
				prevLink = &p
			}
			fhOps = append(fhOps, &atproto.SyncSubscribeRepos_RepoOp{Action: action, Path: op.Collection + "/" + op.Rkey, Cid: &link, Prev: prevLink})
			results = append(results, OpResult{Action: "update", Rkey: op.Rkey, CID: recordCID.String()})
		case "delete":
			prev, exists, err := m.batchRecordCID(ctx, cur, did, op.Collection, op.Rkey)
			if err != nil {
				return nil, nil, err
			}
			if !exists {
				return nil, nil, fmt.Errorf("%w: %s/%s", ErrRecordNotFound, op.Collection, op.Rkey)
			}
			if op.SwapRecord != nil && prev.String() != *op.SwapRecord {
				return nil, nil, fmt.Errorf("%w: %s/%s", ErrSwapRecordMismatch, op.Collection, op.Rkey)
			}
			if err := r.DeleteRecord(ctx, op.Collection+"/"+op.Rkey); err != nil {
				return nil, nil, fmt.Errorf("delete record: %w", err)
			}
			cur[op.Collection+"/"+op.Rkey] = cid.Undef
			deletes = append(deletes, [2]string{op.Collection, op.Rkey})
			p := lexutil.LexLink(prev)
			fhOps = append(fhOps, &atproto.SyncSubscribeRepos_RepoOp{Action: "delete", Path: op.Collection + "/" + op.Rkey, Prev: &p})
			results = append(results, OpResult{Action: "delete", Rkey: op.Rkey})
		default:
			return nil, nil, fmt.Errorf("unknown write action %q", op.Action)
		}
	}

	res, err := m.commit(ctx, did, r)
	if err != nil {
		return nil, nil, err
	}

	for _, iw := range upserts {
		if err := m.upsertRecord(ctx, did, iw.collection, iw.rkey, iw.cid, iw.json); err != nil {
			return nil, nil, err
		}
	}
	for _, d := range deletes {
		if _, err := m.store.DB.ExecContext(ctx,
			"DELETE FROM repo_records WHERE did = ? AND collection = ? AND rkey = ?",
			did, d[0], d[1]); err != nil {
			return nil, nil, err
		}
	}

	if err := m.emitCommit(ctx, did, res, fhOps); err != nil {
		return nil, nil, err
	}

	return res, results, nil
}

// recordCID returns the current record CID (and whether it exists) for a path.
func (m *Manager) recordCID(ctx context.Context, did, collection, rkey string) (cid.Cid, bool, error) {
	var cidBytes []byte
	err := m.store.DB.QueryRowContext(ctx,
		"SELECT record_cid FROM repo_records WHERE did = ? AND collection = ? AND rkey = ?",
		did, collection, rkey).Scan(&cidBytes)
	if err == sql.ErrNoRows {
		return cid.Undef, false, nil
	}
	if err != nil {
		return cid.Undef, false, err
	}
	c, err := cid.Cast(cidBytes)
	if err != nil {
		return cid.Undef, false, err
	}
	return c, true, nil
}

// batchRecordCID is recordCID with in-batch visibility via the pending map.
// An entry with cid.Undef means "deleted in this batch".
func (m *Manager) batchRecordCID(ctx context.Context, cur map[string]cid.Cid, did, collection, rkey string) (cid.Cid, bool, error) {
	key := collection + "/" + rkey
	if c, ok := cur[key]; ok {
		return c, c.Defined(), nil
	}
	c, exists, err := m.recordCID(ctx, did, collection, rkey)
	if err != nil {
		return cid.Undef, false, err
	}
	cur[key] = c
	return c, exists, nil
}

// headCID returns the current head commit CID string ("" if none).
func (m *Manager) headCID(ctx context.Context, did string) (string, error) {
	var cidBytes []byte
	err := m.store.DB.QueryRowContext(ctx,
		"SELECT cid FROM repo_commits WHERE did = ? ORDER BY rev DESC LIMIT 1", did).Scan(&cidBytes)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	c, err := cid.Cast(cidBytes)
	if err != nil {
		return "", err
	}
	return c.String(), nil
}

func (m *Manager) GetRecord(ctx context.Context, did, collection, rkey string) (string, []byte, error) {
	var cidBytes, value []byte
	err := m.store.DB.QueryRowContext(ctx,
		"SELECT record_cid, value FROM repo_records WHERE did = ? AND collection = ? AND rkey = ?",
		did, collection, rkey).Scan(&cidBytes, &value)
	if err != nil {
		return "", nil, err
	}
	c, err := cid.Cast(cidBytes)
	if err != nil {
		return "", nil, err
	}
	return c.String(), value, nil
}

type ListItem struct {
	RKey  string
	CID   string
	Value []byte
}

func (m *Manager) ListRecords(ctx context.Context, did, collection, cursor string, limit int) ([]ListItem, *string, error) {
	query := "SELECT rkey, record_cid, value FROM repo_records WHERE did = ? AND collection = ?"
	args := []any{did, collection}
	if cursor != "" {
		query += " AND rkey > ?"
		args = append(args, cursor)
	}
	query += " ORDER BY rkey ASC LIMIT ?"
	args = append(args, limit+1)

	rows, err := m.store.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	var items []ListItem
	for rows.Next() {
		var rkey string
		var cidBytes, value []byte
		if err := rows.Scan(&rkey, &cidBytes, &value); err != nil {
			return nil, nil, err
		}
		c, err := cid.Cast(cidBytes)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, ListItem{RKey: rkey, CID: c.String(), Value: value})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	var next *string
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1].RKey
		next = &last
	}
	return items, next, nil
}

// ListRecordsDesc is ListRecords in reverse key order (newest TID first),
// used by feed-style reads.
func (m *Manager) ListRecordsDesc(ctx context.Context, did, collection, cursor string, limit int) ([]ListItem, *string, error) {
	query := "SELECT rkey, record_cid, value FROM repo_records WHERE did = ? AND collection = ?"
	args := []any{did, collection}
	if cursor != "" {
		query += " AND rkey < ?"
		args = append(args, cursor)
	}
	query += " ORDER BY rkey DESC LIMIT ?"
	args = append(args, limit+1)

	rows, err := m.store.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	var items []ListItem
	for rows.Next() {
		var rkey string
		var cidBytes, value []byte
		if err := rows.Scan(&rkey, &cidBytes, &value); err != nil {
			return nil, nil, err
		}
		c, err := cid.Cast(cidBytes)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, ListItem{RKey: rkey, CID: c.String(), Value: value})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	var next *string
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1].RKey
		next = &last
	}
	return items, next, nil
}

func (m *Manager) Collections(ctx context.Context, did string) ([]string, error) {
	rows, err := m.store.DB.QueryContext(ctx,
		"SELECT DISTINCT collection FROM repo_records WHERE did = ? ORDER BY collection", did)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

func (m *Manager) upsertRecord(ctx context.Context, did, collection, rkey string, recordCID cid.Cid, recordJSON []byte) error {
	_, err := m.store.DB.ExecContext(ctx,
		`INSERT INTO repo_records (did, collection, rkey, record_cid, value) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(did, collection, rkey) DO UPDATE SET record_cid = excluded.record_cid, value = excluded.value`,
		did, collection, rkey, recordCID.Bytes(), recordJSON)
	return err
}

// Head returns the current head commit CID and rev for a repo.
func (m *Manager) Head(ctx context.Context, did string) (cid.Cid, string, error) {
	var cidBytes []byte
	var rev string
	if err := m.store.DB.QueryRowContext(ctx,
		"SELECT cid, rev FROM repo_commits WHERE did = ? ORDER BY rev DESC LIMIT 1", did).Scan(&cidBytes, &rev); err != nil {
		return cid.Undef, "", err
	}
	head, err := cid.Cast(cidBytes)
	if err != nil {
		return cid.Undef, "", err
	}
	return head, rev, nil
}

// RepoCids returns the block CIDs for a repo, either all of them or only
// those written after `since` (inclusive of the head commit block).
func (m *Manager) RepoCids(ctx context.Context, did, since string) ([]cid.Cid, error) {
	query := "SELECT cid FROM repo_block_revs WHERE did = ?"
	args := []any{did}
	if since != "" {
		query += " AND rev > ?"
		args = append(args, since)
	}
	rows, err := m.store.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []cid.Cid
	for rows.Next() {
		var cidBytes []byte
		if err := rows.Scan(&cidBytes); err != nil {
			return nil, err
		}
		c, err := cid.Cast(cidBytes)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RecordBlock returns the raw DAG-CBOR bytes of a record, from the record CID
// recorded in the repo_records index.
func (m *Manager) RecordBlock(ctx context.Context, did, collection, rkey string) (cid.Cid, []byte, error) {
	var cidBytes []byte
	if err := m.store.DB.QueryRowContext(ctx,
		"SELECT record_cid FROM repo_records WHERE did = ? AND collection = ? AND rkey = ?",
		did, collection, rkey).Scan(&cidBytes); err != nil {
		return cid.Undef, nil, err
	}
	c, err := cid.Cast(cidBytes)
	if err != nil {
		return cid.Undef, nil, err
	}
	blk, err := m.bs.Get(ctx, c)
	if err != nil {
		return cid.Undef, nil, err
	}
	return c, blk.RawData(), nil
}

// RepoInfo is a row of com.atproto.sync.listRepos.
type RepoInfo struct {
	Did    string
	Head   string
	Rev    string
	Active bool
}

func (m *Manager) ListRepos(ctx context.Context, cursor string, limit int) ([]RepoInfo, *string, error) {
	query := `SELECT a.did, a.deactivated_at, c.cid, c.rev
		FROM accounts a
		LEFT JOIN repo_commits c ON c.did = a.did AND c.rev = (SELECT MAX(rev) FROM repo_commits cc WHERE cc.did = a.did)
		WHERE a.did > ?
		ORDER BY a.did ASC LIMIT ?`
	rows, err := m.store.DB.QueryContext(ctx, query, cursor, limit+1)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []RepoInfo
	for rows.Next() {
		var did string
		var deactivated sql.NullString
		var head, rev []byte
		if err := rows.Scan(&did, &deactivated, &head, &rev); err != nil {
			return nil, nil, err
		}
		info := RepoInfo{Did: did, Active: !deactivated.Valid}
		if head != nil {
			h, err := cid.Cast(head)
			if err != nil {
				return nil, nil, err
			}
			info.Head = h.String()
			info.Rev = string(rev)
		}
		out = append(out, info)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	var next *string
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1].Did
		next = &last
	}
	return out, next, nil
}

func (m *Manager) signingKey(ctx context.Context, did string) (atcrypto.PrivateKey, error) {
	var encKey string
	if err := m.store.DB.QueryRowContext(ctx,
		"SELECT signing_key FROM accounts WHERE did = ?", did).Scan(&encKey); err != nil {
		return nil, err
	}
	raw, err := m.store.Box.Decrypt(encKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt signing key: %w", err)
	}
	return atcrypto.ParsePrivateBytesP256(raw)
}

func (m *Manager) open(ctx context.Context, did string) (*atrepo.Repo, error) {
	if r, ok := m.repos[did]; ok {
		return r, nil
	}

	var commitCIDBytes []byte
	err := m.store.DB.QueryRowContext(ctx,
		"SELECT cid FROM repo_commits WHERE did = ? ORDER BY rev DESC LIMIT 1", did).Scan(&commitCIDBytes)

	var r *atrepo.Repo
	switch {
	case err == sql.ErrNoRows:
		r = atrepo.NewRepo(ctx, did, m.recBS)
	case err != nil:
		return nil, err
	default:
		head, cerr := cid.Cast(commitCIDBytes)
		if cerr != nil {
			return nil, cerr
		}
		r, err = atrepo.OpenRepo(ctx, m.recBS, head)
		if err != nil {
			return nil, fmt.Errorf("open repo: %w", err)
		}
	}

	m.repos[did] = r
	return r, nil
}

func (m *Manager) commit(ctx context.Context, did string, r *atrepo.Repo) (*commitResult, error) {
	oldSC := r.SignedCommit()
	var prevData *cid.Cid
	if oldSC.Data.Defined() {
		d := oldSC.Data
		prevData = &d
	}
	prevRev := oldSC.Rev

	key, err := m.signingKey(ctx, did)
	if err != nil {
		return nil, fmt.Errorf("load signing key: %w", err)
	}
	signer := func(_ context.Context, _ string, msg []byte) ([]byte, error) {
		return key.HashAndSign(msg)
	}

	commitCID, rev, err := r.Commit(ctx, signer)
	if err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	sc := r.SignedCommit()
	var prev []byte
	if sc.Prev != nil {
		prev = sc.Prev.Bytes()
	}
	if _, err := m.store.DB.ExecContext(ctx,
		`INSERT INTO repo_commits (did, rev, cid, prev_cid, data_root, sig, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		did, rev, commitCID.Bytes(), prev, sc.Data.Bytes(), sc.Sig, time.Now().Format(time.RFC3339)); err != nil {
		return nil, fmt.Errorf("persist commit: %w", err)
	}

	for _, c := range m.recBS.Drain() {
		if _, err := m.store.DB.ExecContext(ctx,
			`INSERT INTO repo_block_revs (did, cid, rev) VALUES (?, ?, ?)
			 ON CONFLICT(did, cid) DO UPDATE SET rev = excluded.rev`,
			did, c.Bytes(), rev); err != nil {
			return nil, fmt.Errorf("persist block rev: %w", err)
		}
	}

	return &commitResult{commitCID: commitCID, rev: rev, prevRev: prevRev, prevData: prevData}, nil
}

type commitResult struct {
	commitCID cid.Cid
	rev       string
	prevRev   string
	prevData  *cid.Cid
}

// emitCommit publishes a #commit firehose event for the just-written commit.
func (m *Manager) emitCommit(ctx context.Context, did string, res *commitResult, ops []*atproto.SyncSubscribeRepos_RepoOp) error {
	var blocks bytes.Buffer
	if err := m.WriteRepoCAR(ctx, &blocks, did, res.prevRev); err != nil {
		return err
	}

	var prevData *lexutil.LexLink
	if res.prevData != nil {
		pd := lexutil.LexLink(*res.prevData)
		prevData = &pd
	}
	var since *string
	if res.prevRev != "" {
		s := res.prevRev
		since = &s
	}

	if err := m.emitter.Publish(ctx, "#commit", func(seq int64) (firehose.Marshaler, error) {
		return &atproto.SyncSubscribeRepos_Commit{
			Seq:      seq,
			Repo:     did,
			Commit:   lexutil.LexLink(res.commitCID),
			Rev:      res.rev,
			Since:    since,
			PrevData: prevData,
			Ops:      ops,
			Blocks:   lexutil.LexBytes(blocks.Bytes()),
			Time:     firehoseTime(),
		}, nil
	}); err != nil {
		return err
	}
	m.notifyRelays()
	return nil
}

func firehoseTime() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}
