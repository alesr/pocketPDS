package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/alesr/pocketPDS/internal/config"
	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/identity"
	"github.com/alesr/pocketPDS/internal/plc"
	"github.com/alesr/pocketPDS/internal/repo"
	"github.com/alesr/pocketPDS/internal/xrpc"
	atproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/atcrypto"
	"github.com/bluesky-social/indigo/atproto/syntax"
)

func HandleCreateAccount(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in atproto.ServerCreateAccount_Input
		if xrpc.DecodeBody(w, r, &in) != nil {
			return
		}

		handle, err := syntax.ParseHandle(in.Handle)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidHandle", err.Error())
			return
		}

		var exists int
		if err := store.DB.QueryRowContext(r.Context(),
			"SELECT COUNT(*) FROM accounts WHERE handle = ?", handle.String()).Scan(&exists); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		if exists > 0 {
			xrpc.WriteXRPCError(w, http.StatusConflict, "HandleAlreadyExists", "handle is already taken")
			return
		}

		if cfg.InviteRequired {
			if in.InviteCode == nil || *in.InviteCode == "" {
				xrpc.WriteXRPCError(w, http.StatusBadRequest, "InviteCodeRequired", "an invite code is required to create an account")
				return
			}
			var usedBy *string
			var disabledAt *string
			if err := store.DB.QueryRowContext(r.Context(),
				"SELECT used_by, disabled_at FROM invite_codes WHERE code = ?", *in.InviteCode).Scan(&usedBy, &disabledAt); err != nil {
				xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidInviteCode", "invalid invite code")
				return
			}
			if usedBy != nil || disabledAt != nil {
				xrpc.WriteXRPCError(w, http.StatusBadRequest, "InviteCodeInvalid", "invite code is not available")
				return
			}
		}

		password := ""
		if in.Password != nil {
			password = *in.Password
		}
		email := ""
		if in.Email != nil {
			email = *in.Email
		}

		var did string
		var didDocJSON []byte
		var signingKey, recoveryKey atcrypto.PrivateKeyExportable

		switch cfg.DIDMethod {
		case "plc":
			signingKey, err = atcrypto.GeneratePrivateKeyP256()
			if err != nil {
				xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
				return
			}
			recoveryKey, err = atcrypto.GeneratePrivateKeyP256()
			if err != nil {
				xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
				return
			}
			plcClient := plc.NewClient(cfg.PlcURL)
			did, _, err = plcClient.Create(r.Context(), signingKey, recoveryKey, handle.String(), cfg.PublicURL)
			if err != nil {
				xrpc.WriteXRPCError(w, http.StatusBadGateway, "IdentityFailure", "failed to create PLC DID: "+err.Error())
				return
			}
			didDocJSON, err = plcClient.ResolveDidDoc(r.Context(), did)
			if err != nil {
				xrpc.WriteXRPCError(w, http.StatusBadGateway, "IdentityFailure", "failed to resolve PLC DID doc: "+err.Error())
				return
			}
		default: // "web"
			keys, err := identity.CreateDidWeb(handle.String(), cfg.PublicURL)
			if err != nil {
				xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
				return
			}
			did = keys.Did
			signingKey = keys.SigningKey
			recoveryKey = keys.RecoveryKey
			didDocJSON, err = json.Marshal(keys.DidDoc)
			if err != nil {
				xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
				return
			}
		}

		passwordHash, err := db.HashPassword(password)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		signingEnc, err := store.Box.Encrypt(signingKey.Bytes())
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		recoveryEnc, err := store.Box.Encrypt(recoveryKey.Bytes())
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}

		if _, err := store.DB.ExecContext(r.Context(),
			`INSERT INTO accounts (did, handle, email, password_hash, recovery_key, signing_key, tid_clock, created_at, did_doc)
			 VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?)`,
			did, handle.String(), email, passwordHash, recoveryEnc, signingEnc,
			time.Now().Format(time.RFC3339), string(didDocJSON)); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}

		if err := mgr.CreateAccount(r.Context(), did); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}

		if cfg.InviteRequired && in.InviteCode != nil && *in.InviteCode != "" {
			_, _ = store.DB.ExecContext(r.Context(),
				"UPDATE invite_codes SET used_by = ?, used_at = ? WHERE code = ?",
				did, time.Now().Format(time.RFC3339), *in.InviteCode)
		}

		access, refresh, err := mintSession(r.Context(), store, did, "")
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}

		var didDoc any
		_ = json.Unmarshal(didDocJSON, &didDoc)
		xrpc.WriteJSON(w, atproto.ServerCreateAccount_Output{
			AccessJwt:  access,
			RefreshJwt: refresh,
			Did:        did,
			DidDoc:     &didDoc,
			Handle:     handle.String(),
		})
	}
}

func HandleUpdateHandle(cfg *config.Config, store *db.Store, mgr *repo.Manager) http.HandlerFunc {
	type input struct {
		Handle string `json:"handle"`
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
		handle, err := syntax.ParseHandle(in.Handle)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidHandle", err.Error())
			return
		}

		if !stringsHasPrefix(did, "did:plc:") {
			xrpc.WriteXRPCError(w, http.StatusBadRequest, "InvalidRequest", "handle changes are only supported for did:plc accounts")
			return
		}

		var recoveryEnc string
		if err := store.DB.QueryRowContext(r.Context(),
			"SELECT recovery_key FROM accounts WHERE did = ?", did).Scan(&recoveryEnc); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		recoveryRaw, err := store.Box.Decrypt(recoveryEnc)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		recoveryKey, err := atcrypto.ParsePrivateBytesP256(recoveryRaw)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}

		plcClient := plc.NewClient(cfg.PlcURL)
		prev, err := plcClient.LatestCID(r.Context(), did)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadGateway, "IdentityFailure", err.Error())
			return
		}

		var signingEnc string
		if err := store.DB.QueryRowContext(r.Context(),
			"SELECT signing_key FROM accounts WHERE did = ?", did).Scan(&signingEnc); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		signingRaw, err := store.Box.Decrypt(signingEnc)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		signingKey, err := atcrypto.ParsePrivateBytesP256(signingRaw)
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		signingPub, err := signingKey.PublicKey()
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		recoveryPub, err := recoveryKey.PublicKey()
		if err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}

		op := plc.NewAtproto(signingPub.DIDKey(), handle.String(), cfg.PublicURL, []string{recoveryPub.DIDKey()}, &prev)
		if err := op.Sign(recoveryKey); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}
		if err := plcClient.Submit(r.Context(), did, op); err != nil {
			xrpc.WriteXRPCError(w, http.StatusBadGateway, "IdentityFailure", err.Error())
			return
		}

		if _, err := store.DB.ExecContext(r.Context(),
			"UPDATE accounts SET handle = ? WHERE did = ?", handle.String(), did); err != nil {
			xrpc.WriteXRPCError(w, http.StatusInternalServerError, "InternalError", err.Error())
			return
		}

		_ = mgr.EmitIdentity(r.Context(), did, handle.String())

		xrpc.WriteJSON(w, map[string]any{})
	}
}

func stringsHasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
