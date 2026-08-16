package api

import (
	"net/http"

	"github.com/alesr/pocketPDS/internal/config"
	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/xrpc"
)

func HandleHealth(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var one int
		if err := store.DB.QueryRowContext(r.Context(), "SELECT 1").Scan(&one); err != nil {
			xrpc.WriteXRPCError(w, http.StatusServiceUnavailable, "ServiceUnavailable", "db unavailable")
			return
		}
		xrpc.WriteJSON(w, map[string]any{"ok": true})
	}
}

func HandleDescribeServer(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		xrpc.WriteJSON(w, map[string]any{
			"did":                  cfg.EffectiveServiceDID(),
			"availableUserDomains": []string{},
			"inviteCodeRequired":   cfg.InviteRequired,
			"links": map[string]string{
				"privacyPolicy":  cfg.PublicURL + "/privacy",
				"termsOfService": cfg.PublicURL + "/tos",
			},
		})
	}
}
