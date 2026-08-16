package blob

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"io"
	"os"
	"path/filepath"

	"github.com/alesr/pocketPDS/internal/db"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/ipfs/go-cid"
	"github.com/multiformats/go-multihash"
)

// Store persists blobs to a content-addressed directory and keeps metadata
// in the blobs table. Backing is swappable; this is the filesystem backend.
type Store struct {
	root  string
	db    *sql.DB
	clock *syntax.TIDClock
}

func New(root string, store *db.Store) (*Store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Store{root: root, db: store.DB, clock: syntax.NewTIDClock(0)}, nil
}

func (s *Store) Put(ctx context.Context, did, mime string, r io.Reader) (cid.Cid, int64, error) {
	tmp, err := os.CreateTemp(s.root, "upload-*")
	if err != nil {
		return cid.Undef, 0, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), r)
	if err != nil {
		_ = tmp.Close()
		return cid.Undef, 0, err
	}
	if err := tmp.Close(); err != nil {
		return cid.Undef, 0, err
	}

	mh, err := multihash.Sum(h.Sum(nil), multihash.SHA2_256, -1)
	if err != nil {
		return cid.Undef, 0, err
	}
	c := cid.NewCidV1(cid.Raw, mh)

	finalPath := filepath.Join(s.root, c.String())
	if err := os.Rename(tmp.Name(), finalPath); err != nil {
		return cid.Undef, 0, err
	}

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO blobs (cid, did, size, mime_type, storage, path, created_at)
		 VALUES (?, ?, ?, ?, 'fs', ?, ?) ON CONFLICT(cid) DO NOTHING`,
		c.Bytes(), did, n, mime, finalPath, s.clock.Next().String()); err != nil {
		return cid.Undef, 0, err
	}
	return c, n, nil
}

func (s *Store) Open(ctx context.Context, did string, c cid.Cid) (io.ReadCloser, string, int64, error) {
	var path, mime string
	var size int64
	err := s.db.QueryRowContext(ctx,
		"SELECT path, mime_type, size FROM blobs WHERE cid = ? AND did = ?",
		c.Bytes(), did).Scan(&path, &mime, &size)
	if err != nil {
		return nil, "", 0, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, "", 0, err
	}
	return f, mime, size, nil
}

// DeleteForDID removes a DID's blob files and metadata rows.
func (s *Store) DeleteForDID(ctx context.Context, did string) error {
	rows, err := s.db.QueryContext(ctx, "SELECT path FROM blobs WHERE did = ?", did)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return err
		}
		paths = append(paths, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range paths {
		_ = os.Remove(p)
	}
	_, err = s.db.ExecContext(ctx, "DELETE FROM blobs WHERE did = ?", did)
	return err
}

func (s *Store) List(ctx context.Context, did, since, cursor string, limit int) ([]string, *string, error) {
	query := "SELECT cid, created_at FROM blobs WHERE did = ?"
	args := []any{did}
	if since != "" {
		query += " AND created_at > ?"
		args = append(args, since)
	}
	if cursor != "" {
		query += " AND created_at > ?"
		args = append(args, cursor)
	}
	query += " ORDER BY created_at ASC LIMIT ?"
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()

	var cids []string
	var lastCreated string
	for rows.Next() {
		var cidBytes []byte
		var created string
		if err := rows.Scan(&cidBytes, &created); err != nil {
			return nil, nil, err
		}
		c, err := cid.Cast(cidBytes)
		if err != nil {
			return nil, nil, err
		}
		cids = append(cids, c.String())
		lastCreated = created
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	var next *string
	if len(cids) > limit {
		cids = cids[:limit]
		next = &lastCreated
	}
	return cids, next, nil
}
