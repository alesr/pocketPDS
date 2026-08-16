package firehose

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"sync"

	cbg "github.com/whyrusleeping/cbor-gen"
)

// Marshaler is the subset of cbg.CBORMarshaler the firehose needs.
type Marshaler interface {
	MarshalCBOR(w io.Writer) error
}

// opMessage is the EventHeader op for a normal message frame (matches indigo's
// events.EvtKindMessage). Error frames are never emitted by PocketPDS.
const opMessage int64 = 1

// MarshalFrame serializes a firehose frame: an EventHeader (op + type) followed
// by the DAG-CBOR event body. This is wire-compatible with indigo's
// events.XRPCStreamEvent.Serialize, so standard relays can consume it.
func MarshalFrame(msgType string, obj Marshaler) ([]byte, error) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, msgType, obj); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func WriteFrame(w io.Writer, msgType string, obj Marshaler) error {
	if err := writeHeader(w, opMessage, msgType); err != nil {
		return err
	}
	return obj.MarshalCBOR(w)
}

func writeHeader(w io.Writer, op int64, msgType string) error {
	cw := cbg.NewCborWriter(w)
	fieldCount := 2
	if msgType == "" {
		fieldCount--
	}
	if _, err := cw.Write(cbg.CborEncodeMajorType(cbg.MajMap, uint64(fieldCount))); err != nil {
		return err
	}
	if msgType != "" {
		if err := cw.WriteMajorTypeHeader(cbg.MajTextString, 1); err != nil {
			return err
		}
		if _, err := cw.WriteString("t"); err != nil {
			return err
		}
		if err := cw.WriteMajorTypeHeader(cbg.MajTextString, uint64(len(msgType))); err != nil {
			return err
		}
		if _, err := cw.WriteString(msgType); err != nil {
			return err
		}
	}
	if err := cw.WriteMajorTypeHeader(cbg.MajTextString, 2); err != nil {
		return err
	}
	if _, err := cw.WriteString("op"); err != nil {
		return err
	}
	if op >= 0 {
		if err := cw.WriteMajorTypeHeader(cbg.MajUnsignedInt, uint64(op)); err != nil {
			return err
		}
	} else {
		if err := cw.WriteMajorTypeHeader(cbg.MajNegativeInt, uint64(-op-1)); err != nil {
			return err
		}
	}
	return nil
}

// Emitter assigns stream sequence numbers, persists each frame for cursor
// replay, and broadcasts to live subscribers.
type Emitter struct {
	db   *sql.DB
	mu   sync.Mutex
	seq  int64
	subs map[int64]chan []byte
	id   int64
}

func NewEmitter(db *sql.DB) *Emitter {
	var maxSeq int64
	_ = db.QueryRow("SELECT COALESCE(MAX(seq), 0) FROM firehose_events").Scan(&maxSeq)
	return &Emitter{
		db:   db,
		seq:  maxSeq,
		subs: make(map[int64]chan []byte),
	}
}

// Publish allocates the next sequence number, builds and serializes the frame,
// persists it, and broadcasts to live subscribers atomically, so stream order
// always matches sequence order.
func (e *Emitter) Publish(ctx context.Context, msgType string, build func(seq int64) (Marshaler, error)) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.seq++
	obj, err := build(e.seq)
	if err != nil {
		e.seq--
		return err
	}
	frame, err := MarshalFrame(msgType, obj)
	if err != nil {
		e.seq--
		return err
	}
	if _, err := e.db.ExecContext(ctx, "INSERT INTO firehose_events (seq, frame) VALUES (?, ?)", e.seq, frame); err != nil {
		return err
	}

	for _, ch := range e.subs {
		select {
		case ch <- frame:
		default:
		}
	}
	return nil
}

// Subscribe returns a channel of serialized frames, replaying persisted events
// with seq > cursor before going live, plus a cleanup func.
func (e *Emitter) Subscribe(ctx context.Context, cursor int64) (<-chan []byte, func()) {
	out := make(chan []byte, 4096)
	live := make(chan []byte, 4096)

	e.mu.Lock()
	e.id++
	id := e.id
	e.subs[id] = live
	e.mu.Unlock()

	go func() {
		defer close(out)
		rows, err := e.db.QueryContext(ctx, "SELECT frame FROM firehose_events WHERE seq > ? ORDER BY seq ASC", cursor)
		if err == nil {
			for rows.Next() {
				var frame []byte
				if err := rows.Scan(&frame); err != nil {
					break
				}
				select {
				case out <- frame:
				case <-ctx.Done():
					_ = rows.Close()
					return
				}
			}
			_ = rows.Close()
		}
		for {
			select {
			case frame := <-live:
				select {
				case out <- frame:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	cleanup := func() {
		e.mu.Lock()
		delete(e.subs, id)
		e.mu.Unlock()
	}
	return out, cleanup
}
