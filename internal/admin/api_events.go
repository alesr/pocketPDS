package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"

	atproto "github.com/bluesky-social/indigo/api/atproto"
	cbg "github.com/whyrusleeping/cbor-gen"
)

// eventsStream is a Server-Sent Events endpoint that replays the most recent
// firehose events (decoded to JSON summaries) and then streams new ones live.
func (h *Handler) eventsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= 500 {
			limit = n
		}
	}
	var maxSeq int64
	_ = h.store.DB.QueryRowContext(r.Context(), "SELECT COALESCE(MAX(seq),0) FROM firehose_events").Scan(&maxSeq)
	cursor := maxSeq - int64(limit)
	if cursor < 0 {
		cursor = 0
	}

	frames, cleanup := h.mgr.Emitter().Subscribe(r.Context(), cursor)
	defer cleanup()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				return
			}
			evt, err := decodeEvent(frame)
			if err != nil {
				continue
			}
			data, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// decodeEvent parses a firehose frame (EventHeader + DAG-CBOR body) into a
// JSON-friendly summary, without importing indigo's events package.
func decodeEvent(frame []byte) (map[string]any, error) {
	r := bytes.NewReader(frame)
	msgType, _, err := decodeHeader(r)
	if err != nil {
		return nil, err
	}

	out := map[string]any{"type": msgType}
	switch msgType {
	case "#commit":
		var evt atproto.SyncSubscribeRepos_Commit
		if err := evt.UnmarshalCBOR(r); err != nil {
			return nil, err
		}
		out["seq"] = evt.Seq
		out["did"] = evt.Repo
		out["rev"] = evt.Rev
		out["time"] = evt.Time
		if evt.Since != nil {
			out["since"] = *evt.Since
		}
		ops := make([]map[string]any, 0, len(evt.Ops))
		for _, op := range evt.Ops {
			if op == nil {
				continue
			}
			ops = append(ops, map[string]any{"action": op.Action, "path": op.Path})
		}
		out["opCount"] = len(ops)
		out["ops"] = ops

	case "#identity":
		var evt atproto.SyncSubscribeRepos_Identity
		if err := evt.UnmarshalCBOR(r); err != nil {
			return nil, err
		}
		out["seq"] = evt.Seq
		out["did"] = evt.Did
		if evt.Handle != nil {
			out["handle"] = *evt.Handle
		}
		out["time"] = evt.Time

	case "#account":
		var evt atproto.SyncSubscribeRepos_Account
		if err := evt.UnmarshalCBOR(r); err != nil {
			return nil, err
		}
		out["seq"] = evt.Seq
		out["did"] = evt.Did
		out["active"] = evt.Active
		if evt.Status != nil {
			out["status"] = *evt.Status
		}
		out["time"] = evt.Time

	case "#info":
		var evt atproto.SyncSubscribeRepos_Info
		if err := evt.UnmarshalCBOR(r); err != nil {
			return nil, err
		}
		out["name"] = evt.Name
		if evt.Message != nil {
			out["message"] = *evt.Message
		}
	}
	return out, nil
}

// decodeHeader parses the firehose EventHeader: a CBOR map with keys "t"
// (message type) and "op" (frame kind).
func decodeHeader(r io.Reader) (string, int64, error) {
	maj, n, err := cbg.CborReadHeader(r)
	if err != nil {
		return "", 0, err
	}
	if maj != cbg.MajMap {
		return "", 0, fmt.Errorf("expected map header, got major type %d", maj)
	}

	var msgType string
	var op int64 = 1
	for i := uint64(0); i < n; i++ {
		key, err := cbg.ReadString(r)
		if err != nil {
			return "", 0, err
		}
		switch key {
		case "t":
			if msgType, err = cbg.ReadString(r); err != nil {
				return "", 0, err
			}
		case "op":
			if op, err = readInt64(r); err != nil {
				return "", 0, err
			}
		default:
			return "", 0, fmt.Errorf("unexpected event header key %q", key)
		}
	}
	return msgType, op, nil
}

func readInt64(r io.Reader) (int64, error) {
	maj, val, err := cbg.CborReadHeader(r)
	if err != nil {
		return 0, err
	}
	switch maj {
	case cbg.MajUnsignedInt:
		if val > math.MaxInt64 {
			return 0, fmt.Errorf("integer overflow")
		}
		return int64(val), nil
	case cbg.MajNegativeInt:
		return -1 - int64(val), nil
	default:
		return 0, fmt.Errorf("expected integer, got major type %d", maj)
	}
}
