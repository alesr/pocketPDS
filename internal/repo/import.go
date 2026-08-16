package repo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	atrepo "github.com/bluesky-social/indigo/repo"
	cid "github.com/ipfs/go-cid"
	cbor "github.com/ipfs/go-ipld-cbor"
	car "github.com/ipld/go-car"
)

// ImportRepo ingests a repo CAR (the format produced by
// com.atproto.sync.getRepo) for an existing account. It writes the blocks, the
// head commit, the record index, and the block-revision map so the repo is
// fully readable via both the sync and repo endpoints.
func (m *Manager) ImportRepo(ctx context.Context, did string, r io.Reader) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cr, err := car.NewCarReader(r)
	if err != nil {
		return fmt.Errorf("read car: %w", err)
	}
	if len(cr.Header.Roots) != 1 {
		return fmt.Errorf("expected a single car root, got %d", len(cr.Header.Roots))
	}
	head := cr.Header.Roots[0]

	var cids []cid.Cid
	for {
		blk, err := cr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read car block: %w", err)
		}
		cids = append(cids, blk.Cid())
		if err := m.bs.Put(ctx, blk); err != nil {
			return fmt.Errorf("write block: %w", err)
		}
	}

	rp, err := atrepo.OpenRepo(ctx, m.bs, head)
	if err != nil {
		return fmt.Errorf("open imported repo: %w", err)
	}
	if rp.RepoDid() != did {
		return fmt.Errorf("repo did %q does not match account %q", rp.RepoDid(), did)
	}
	sc := rp.SignedCommit()

	type record struct {
		collection, rkey string
		recordCID        cid.Cid
		json             []byte
	}
	var records []record
	if err := rp.ForEach(ctx, "", func(k string, v cid.Cid) error {
		collection, rkey, _ := strings.Cut(k, "/")
		if collection == "" || rkey == "" {
			return nil
		}
		blk, err := m.bs.Get(ctx, v)
		if err != nil {
			return err
		}
		value, err := recordJSON(blk.RawData())
		if err != nil {
			return err
		}
		records = append(records, record{collection, rkey, v, value})
		return nil
	}); err != nil {
		return fmt.Errorf("walk records: %w", err)
	}

	var prev []byte
	if sc.Prev != nil {
		prev = sc.Prev.Bytes()
	}

	tx, err := m.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range []string{
		"DELETE FROM repo_commits WHERE did = ?",
		"DELETE FROM repo_records WHERE did = ?",
		"DELETE FROM repo_block_revs WHERE did = ?",
	} {
		if _, err := tx.ExecContext(ctx, stmt, did); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO repo_commits (did, rev, cid, prev_cid, data_root, sig, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		did, sc.Rev, head.Bytes(), prev, sc.Data.Bytes(), sc.Sig, time.Now().Format(time.RFC3339)); err != nil {
		return err
	}

	for _, rec := range records {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO repo_records (did, collection, rkey, record_cid, value) VALUES (?, ?, ?, ?, ?)`,
			did, rec.collection, rec.rkey, rec.recordCID.Bytes(), rec.json); err != nil {
			return err
		}
	}

	for _, c := range cids {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO repo_block_revs (did, cid, rev) VALUES (?, ?, ?)
			 ON CONFLICT(did, cid) DO UPDATE SET rev = excluded.rev`,
			did, c.Bytes(), sc.Rev); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	delete(m.repos, did)
	return nil
}

// recordJSON converts a DAG-CBOR record into the JSON form served by
// getRecord/listRecords. It maps CBOR links to {"$link": ...} and byte strings
// to {"$bytes": ...}, matching the lexicon JSON representation.
func recordJSON(data []byte) ([]byte, error) {
	var obj any
	if err := cbor.DecodeInto(data, &obj); err != nil {
		return nil, err
	}
	return json.Marshal(toJSON(obj))
}

func toJSON(v any) any {
	switch x := v.(type) {
	case cid.Cid:
		return map[string]any{"$link": x.String()}
	case map[string]any:
		for k, val := range x {
			x[k] = toJSON(val)
		}
		return x
	case []any:
		for i, val := range x {
			x[i] = toJSON(val)
		}
		return x
	case []byte:
		return map[string]any{"$bytes": base64.RawStdEncoding.EncodeToString(x)}
	default:
		return v
	}
}
