package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/xrpc"
)

func HandleCreateInviteCodes(store *db.Store) http.HandlerFunc {
	type input struct {
		CodeCount int64 `json:"codeCount"`
		UseCount  int64 `json:"useCount"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}
		var in input
		if xrpc.DecodeBody(w, r, &in) != nil {
			return
		}
		if in.CodeCount < 1 || in.CodeCount > 100 {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", "codeCount must be 1-100")
			return
		}

		now := time.Now().Format(time.RFC3339)
		var codes []string
		for i := int64(0); i < in.CodeCount; i++ {
			code := randomInviteCode()
			if _, err := store.DB.ExecContext(r.Context(),
				"INSERT INTO invite_codes (code, created_by, created_at) VALUES (?, ?, ?)",
				code, did, now); err != nil {
				xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
				return
			}
			codes = append(codes, code)
		}

		xrpc.WriteJSON(w, map[string]any{
			"codes": []map[string]any{{"account": did, "codes": codes}},
		})
	}
}

func HandleGetAccountInviteCodes(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		did, ok := requireAuth(w, r, store)
		if !ok {
			return
		}
		rows, err := store.DB.QueryContext(r.Context(),
			"SELECT code, created_at, used_by, disabled_at FROM invite_codes WHERE created_by = ? ORDER BY created_at", did)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		defer func() { _ = rows.Close() }()

		var codes []map[string]any
		for rows.Next() {
			var code, createdAt string
			var usedBy, disabledAt *string
			if err := rows.Scan(&code, &createdAt, &usedBy, &disabledAt); err != nil {
				continue
			}
			codes = append(codes, map[string]any{
				"code":      code,
				"available": usedBy == nil && disabledAt == nil,
				"disabled":  disabledAt != nil,
				"createdBy": did,
				"createdAt": createdAt,
				"uses":      0,
			})
		}
		xrpc.WriteJSON(w, map[string]any{"codes": codes})
	}
}

func randomInviteCode() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "pocketpds-" + hex.EncodeToString(b)[:16]
}
