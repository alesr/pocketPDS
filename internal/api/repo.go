package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/repo"
	"github.com/alesr/pocketPDS/internal/xrpc"
	"github.com/bluesky-social/indigo/atproto/atdata"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	cbg "github.com/whyrusleeping/cbor-gen"
)

// rawCbor is a CborMarshaler that emits pre-encoded DAG-CBOR bytes, used for
// unknown lexicon records when validate=false.
type rawCbor []byte

func (r rawCbor) MarshalCBOR(w io.Writer) error {
	_, err := w.Write(r)
	return err
}

// recordMarshaler builds a CBOR marshaler for a record: the typed lexicon path
// for known types, a generic path for unknown types when validate=false.
func recordMarshaler(recordJSON []byte, validate *bool) (cbg.CBORMarshaler, error) {
	typ, err := lexutil.TypeExtract(recordJSON)
	if err != nil {
		return nil, fmt.Errorf("record missing $type: %w", err)
	}
	if _, err := lexutil.NewFromType(typ); err == nil {
		var ltd lexutil.LexiconTypeDecoder
		if err := ltd.UnmarshalJSON(recordJSON); err != nil {
			return nil, fmt.Errorf("invalid record: %w", err)
		}
		return ltd.Val, nil
	}
	if validate == nil || *validate {
		return nil, fmt.Errorf("unsupported lexicon type %q", typ)
	}
	obj, err := atdata.UnmarshalJSON(recordJSON)
	if err != nil {
		return nil, fmt.Errorf("invalid record: %w", err)
	}
	cborBytes, err := atdata.MarshalCBOR(obj)
	if err != nil {
		return nil, err
	}
	return rawCbor(cborBytes), nil
}

// RecordMarshaler converts raw record JSON into a CBOR marshaler suitable for
// writing into a repo, for code outside this package (e.g. the bridge).
func RecordMarshaler(recordJSON []byte) (cbg.CBORMarshaler, error) {
	return recordMarshaler(recordJSON, nil)
}

// requireRepoMatch validates that the repo field names the authenticated DID.
func requireRepoMatch(r *http.Request, store *db.Store, did, repo string) error {
	if repo == "" {
		return fmt.Errorf("repo is required")
	}
	resolved, err := resolveLocalDid(r.Context(), store, repo)
	if err != nil {
		return fmt.Errorf("repo not found")
	}
	if resolved != did {
		return fmt.Errorf("repo does not match authenticated account")
	}
	return nil
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repo.ErrSwapCommitMismatch), errors.Is(err, repo.ErrSwapRecordMismatch):
		xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidSwap", err.Error())
	case errors.Is(err, repo.ErrRecordExists), errors.Is(err, repo.ErrRecordNotFound):
		xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
	default:
		xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}

type createRecordInput struct {
	Repo       string          `json:"repo"`
	Collection string          `json:"collection"`
	Rkey       *string         `json:"rkey"`
	Record     json.RawMessage `json:"record"`
	SwapCommit *string         `json:"swapCommit"`
	Validate   *bool           `json:"validate"`
}

func HandleCreateRecord(store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		var in createRecordInput
		if err := json.Unmarshal(body, &in); err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		if err := requireRepoMatch(r, store, did, in.Repo); err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		if len(in.Record) == 0 {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", "record is required")
			return
		}
		if err := validateRecordType(in.Record, in.Collection); err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		collection, err := syntax.ParseNSID(in.Collection)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		rec, err := recordMarshaler(in.Record, in.Validate)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}

		op := repo.Op{Action: "create", Collection: collection.String(), Rec: rec, RecordJSON: in.Record}
		if in.Rkey != nil {
			rkey, err := syntax.ParseRecordKey(*in.Rkey)
			if err != nil {
				xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
				return
			}
			op.Rkey = rkey.String()
		}

		commitCID, rev, results, err := mgr.ApplyWrites(r.Context(), did, []repo.Op{op}, in.SwapCommit)
		if err != nil {
			writeError(w, err)
			return
		}

		xrpc.WriteJSON(w, map[string]any{
			"uri":    fmt.Sprintf("at://%s/%s/%s", did, collection, results[0].Rkey),
			"cid":    results[0].CID,
			"commit": map[string]string{"cid": commitCID, "rev": rev},
		})
	}
}

type putRecordInput struct {
	Repo       string          `json:"repo"`
	Collection string          `json:"collection"`
	Rkey       string          `json:"rkey"`
	Record     json.RawMessage `json:"record"`
	SwapCommit *string         `json:"swapCommit"`
	SwapRecord *string         `json:"swapRecord"`
	Validate   *bool           `json:"validate"`
}

func HandlePutRecord(store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		var in putRecordInput
		if err := json.Unmarshal(body, &in); err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		if err := requireRepoMatch(r, store, did, in.Repo); err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		if len(in.Record) == 0 {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", "record is required")
			return
		}
		if err := validateRecordType(in.Record, in.Collection); err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		collection, err := syntax.ParseNSID(in.Collection)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		rkey, err := syntax.ParseRecordKey(in.Rkey)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		rec, err := recordMarshaler(in.Record, in.Validate)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}

		commitCID, rev, results, err := mgr.ApplyWrites(r.Context(), did, []repo.Op{{
			Action:     "update",
			Collection: collection.String(),
			Rkey:       rkey.String(),
			Rec:        rec,
			RecordJSON: in.Record,
			SwapRecord: in.SwapRecord,
		}}, in.SwapCommit)
		if err != nil {
			writeError(w, err)
			return
		}

		xrpc.WriteJSON(w, map[string]any{
			"uri":    fmt.Sprintf("at://%s/%s/%s", did, collection, rkey),
			"cid":    results[0].CID,
			"commit": map[string]string{"cid": commitCID, "rev": rev},
		})
	}
}

type deleteRecordInput struct {
	Repo       string  `json:"repo"`
	Collection string  `json:"collection"`
	Rkey       string  `json:"rkey"`
	SwapCommit *string `json:"swapCommit"`
	SwapRecord *string `json:"swapRecord"`
}

func HandleDeleteRecord(store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}

		var in deleteRecordInput
		if xrpc.DecodeBody(w, r, &in) != nil {
			return
		}
		if err := requireRepoMatch(r, store, did, in.Repo); err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		collection, err := syntax.ParseNSID(in.Collection)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		rkey, err := syntax.ParseRecordKey(in.Rkey)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}

		commitCID, rev, _, err := mgr.ApplyWrites(r.Context(), did, []repo.Op{{
			Action:     "delete",
			Collection: collection.String(),
			Rkey:       rkey.String(),
			SwapRecord: in.SwapRecord,
		}}, in.SwapCommit)
		if err != nil {
			writeError(w, err)
			return
		}

		xrpc.WriteJSON(w, map[string]any{
			"commit": map[string]string{"cid": commitCID, "rev": rev},
		})
	}
}

type applyWritesInput struct {
	Repo       string                 `json:"repo"`
	SwapCommit *string                `json:"swapCommit"`
	Validate   *bool                  `json:"validate"`
	Writes     []applyWritesInputElem `json:"writes"`
}

type applyWritesInputElem struct {
	Action     string          `json:"$type"`
	Collection string          `json:"collection"`
	Rkey       string          `json:"rkey"`
	Value      json.RawMessage `json:"value"`
}

func HandleApplyWrites(store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		var in applyWritesInput
		if err := json.Unmarshal(body, &in); err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		if err := requireRepoMatch(r, store, did, in.Repo); err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		if len(in.Writes) == 0 {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", "writes is required")
			return
		}

		ops := make([]repo.Op, 0, len(in.Writes))
		results := make([]map[string]any, 0, len(in.Writes))

		for _, ew := range in.Writes {
			action, err := applyWritesAction(ew.Action)
			if err != nil {
				xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
				return
			}
			collection, err := syntax.ParseNSID(ew.Collection)
			if err != nil {
				xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
				return
			}

			op := repo.Op{Action: action, Collection: collection.String()}
			if ew.Rkey != "" {
				rkey, err := syntax.ParseRecordKey(ew.Rkey)
				if err != nil {
					xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
					return
				}
				op.Rkey = rkey.String()
			}
			if action == "delete" && op.Rkey == "" {
				xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", "rkey is required")
				return
			}
			if action != "delete" {
				if len(ew.Value) == 0 {
					xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", "value is required")
					return
				}
				if err := validateRecordType(ew.Value, ew.Collection); err != nil {
					xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
					return
				}
				rec, err := recordMarshaler(ew.Value, in.Validate)
				if err != nil {
					xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
					return
				}
				op.Rec = rec
				op.RecordJSON = ew.Value
			}
			ops = append(ops, op)
		}

		commitCID, rev, opResults, err := mgr.ApplyWrites(r.Context(), did, ops, in.SwapCommit)
		if err != nil {
			writeError(w, err)
			return
		}

		for i, or := range opResults {
			elem := map[string]any{}
			switch or.Action {
			case "create":
				elem["$type"] = "com.atproto.repo.applyWrites#createResult"
				elem["uri"] = fmt.Sprintf("at://%s/%s/%s", did, ops[i].Collection, or.Rkey)
				elem["cid"] = or.CID
			case "update":
				elem["$type"] = "com.atproto.repo.applyWrites#updateResult"
				elem["uri"] = fmt.Sprintf("at://%s/%s/%s", did, ops[i].Collection, or.Rkey)
				elem["cid"] = or.CID
			case "delete":
				elem["$type"] = "com.atproto.repo.applyWrites#deleteResult"
			}
			results = append(results, elem)
		}

		xrpc.WriteJSON(w, map[string]any{
			"commit":  map[string]string{"cid": commitCID, "rev": rev},
			"results": results,
		})
	}
}

func applyWritesAction(t string) (string, error) {
	switch t {
	case "com.atproto.repo.applyWrites#create":
		return "create", nil
	case "com.atproto.repo.applyWrites#update":
		return "update", nil
	case "com.atproto.repo.applyWrites#delete":
		return "delete", nil
	default:
		return "", fmt.Errorf("unknown applyWrites action %q", t)
	}
}

func HandleGetRecord(store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		did, err := resolveLocalDid(r.Context(), store, q.Get("repo"))
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusNotFound, "RepoNotFound", "repo not found")
			return
		}

		collection, err := syntax.ParseNSID(q.Get("collection"))
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}
		rkey, err := syntax.ParseRecordKey(q.Get("rkey"))
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}

		recordCID, value, err := mgr.GetRecord(r.Context(), did, collection.String(), rkey.String())
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusNotFound, "RecordNotFound", "record not found")
			return
		}

		xrpc.WriteJSON(w, map[string]any{
			"uri":   fmt.Sprintf("at://%s/%s/%s", did, collection, rkey),
			"cid":   recordCID,
			"value": json.RawMessage(value),
		})
	}
}

func HandleListRecords(store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		did, err := resolveLocalDid(r.Context(), store, q.Get("repo"))
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusNotFound, "RepoNotFound", "repo not found")
			return
		}

		collection, err := syntax.ParseNSID(q.Get("collection"))
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
			return
		}

		limit := 50
		if raw := q.Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 100 {
				xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", "limit must be 1-100")
				return
			}
			limit = n
		}

		items, next, err := mgr.ListRecords(r.Context(), did, collection.String(), q.Get("cursor"), limit)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}

		records := make([]map[string]any, 0, len(items))
		for _, it := range items {
			records = append(records, map[string]any{
				"uri":   fmt.Sprintf("at://%s/%s/%s", did, collection, it.RKey),
				"cid":   it.CID,
				"value": json.RawMessage(it.Value),
			})
		}

		resp := map[string]any{"records": records}
		if next != nil {
			resp["cursor"] = *next
		}
		xrpc.WriteJSON(w, resp)
	}
}

func HandleDescribeRepo(store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, err := resolveLocalDid(r.Context(), store, r.URL.Query().Get("repo"))
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusNotFound, "RepoNotFound", "repo not found")
			return
		}

		var handle, didDocJSON string
		if err := store.DB.QueryRowContext(r.Context(),
			"SELECT handle, did_doc FROM accounts WHERE did = ?", did).Scan(&handle, &didDocJSON); err != nil {
			xrpc.WriteXRPCError(w, http.StatusNotFound, "RepoNotFound", "repo not found")
			return
		}

		collections, err := mgr.Collections(r.Context(), did)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}

		handleIsCorrect := false
		if h, err := syntax.ParseHandle(handle); err == nil {
			if ident, err := dir.LookupHandle(r.Context(), h); err == nil {
				handleIsCorrect = ident.DID.String() == did
			}
		}

		var didDoc any
		_ = json.Unmarshal([]byte(didDocJSON), &didDoc)

		xrpc.WriteJSON(w, map[string]any{
			"did":             did,
			"didDoc":          didDoc,
			"handle":          handle,
			"handleIsCorrect": handleIsCorrect,
			"collections":     collections,
		})
	}
}

func validateRecordType(recordJSON []byte, collection string) error {
	typ, err := lexutil.TypeExtract(recordJSON)
	if err != nil {
		return fmt.Errorf("record missing $type: %w", err)
	}
	if typ != collection {
		return fmt.Errorf("record $type %q does not match collection %q", typ, collection)
	}
	return nil
}
