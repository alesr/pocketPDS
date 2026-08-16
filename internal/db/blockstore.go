package db

import (
	"context"
	"database/sql"
	"fmt"

	block "github.com/ipfs/go-block-format"
	cid "github.com/ipfs/go-cid"
)

// SQLBlockstore implements cbor.IpldBlockstore over the repo_blocks table,
// satisfying the blockstore contract required by indigo's repo package.
type SQLBlockstore struct {
	DB *sql.DB
}

func (b *SQLBlockstore) Get(ctx context.Context, c cid.Cid) (block.Block, error) {
	var data []byte
	err := b.DB.QueryRowContext(ctx, "SELECT data FROM repo_blocks WHERE cid = ?", c.Bytes()).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("block not found: %s", c)
	}
	if err != nil {
		return nil, err
	}
	return block.NewBlockWithCid(data, c)
}

func (b *SQLBlockstore) Put(ctx context.Context, blk block.Block) error {
	_, err := b.DB.ExecContext(ctx,
		"INSERT INTO repo_blocks (cid, data, size) VALUES (?, ?, ?) ON CONFLICT(cid) DO NOTHING",
		blk.Cid().Bytes(), blk.RawData(), len(blk.RawData()))
	return err
}

func (b *SQLBlockstore) Has(ctx context.Context, c cid.Cid) (bool, error) {
	var one int
	err := b.DB.QueryRowContext(ctx, "SELECT 1 FROM repo_blocks WHERE cid = ?", c.Bytes()).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}
