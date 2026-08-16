package repo

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/ipfs/go-cid"
	car "github.com/ipld/go-car"
)

// WriteRepoCAR writes a CAR (rooted at the repo head commit) containing all
// blocks for the repo, or only those written after `since` for incremental
// sync.
func (m *Manager) WriteRepoCAR(ctx context.Context, w io.Writer, did, since string) error {
	head, _, err := m.Head(ctx, did)
	if err != nil {
		return err
	}
	cids, err := m.RepoCids(ctx, did, since)
	if err != nil {
		return err
	}

	if err := writeCarHeader(w, []cid.Cid{head}); err != nil {
		return err
	}
	for _, c := range cids {
		blk, err := m.bs.Get(ctx, c)
		if err != nil {
			return err
		}
		if err := writeCarBlock(w, blk.Cid().Bytes(), blk.RawData()); err != nil {
			return err
		}
	}
	return nil
}

// WriteRecordCAR writes a single-block CAR containing a record, rooted at the
// record CID.
func (m *Manager) WriteRecordCAR(ctx context.Context, w io.Writer, did, collection, rkey string) error {
	recCID, data, err := m.RecordBlock(ctx, did, collection, rkey)
	if err != nil {
		return err
	}
	if err := writeCarHeader(w, []cid.Cid{recCID}); err != nil {
		return err
	}
	return writeCarBlock(w, recCID.Bytes(), data)
}

// WriteBlocksCAR writes a CAR containing the requested blocks with no root.
func (m *Manager) WriteBlocksCAR(ctx context.Context, w io.Writer, cids []cid.Cid) error {
	if err := writeCarHeader(w, nil); err != nil {
		return err
	}
	for _, c := range cids {
		blk, err := m.bs.Get(ctx, c)
		if err != nil {
			return fmt.Errorf("block %s: %w", c, err)
		}
		if err := writeCarBlock(w, blk.Cid().Bytes(), blk.RawData()); err != nil {
			return err
		}
	}
	return nil
}

func writeCarHeader(w io.Writer, roots []cid.Cid) error {
	return car.WriteHeader(&car.CarHeader{Roots: roots, Version: 1}, w)
}

func writeCarBlock(w io.Writer, cidBytes, data []byte) error {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], uint64(len(cidBytes)+len(data)))
	if _, err := w.Write(buf[:n]); err != nil {
		return err
	}
	if _, err := w.Write(cidBytes); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	return nil
}
