package repo

import (
	"context"
	"sync"

	"github.com/alesr/pocketPDS/internal/db"
	block "github.com/ipfs/go-block-format"
	cid "github.com/ipfs/go-cid"
)

// recordingBlockstore wraps the SQL blockstore and records every Put CID
// during a write operation, so the manager can persist per-commit block revs
// for incremental sync (getRepo?since=rev).
type recordingBlockstore struct {
	inner    *db.SQLBlockstore
	mu       sync.Mutex
	recorded map[cid.Cid]struct{}
}

func (b *recordingBlockstore) Get(ctx context.Context, c cid.Cid) (block.Block, error) {
	return b.inner.Get(ctx, c)
}

func (b *recordingBlockstore) Put(ctx context.Context, blk block.Block) error {
	if err := b.inner.Put(ctx, blk); err != nil {
		return err
	}
	b.mu.Lock()
	if b.recorded != nil {
		b.recorded[blk.Cid()] = struct{}{}
	}
	b.mu.Unlock()
	return nil
}

func (b *recordingBlockstore) Begin() {
	b.mu.Lock()
	b.recorded = make(map[cid.Cid]struct{})
	b.mu.Unlock()
}

func (b *recordingBlockstore) Drain() []cid.Cid {
	b.mu.Lock()
	m := b.recorded
	b.recorded = nil
	b.mu.Unlock()

	out := make([]cid.Cid, 0, len(m))
	for c := range m {
		out = append(out, c)
	}
	return out
}
