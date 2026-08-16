package admin

import (
	"net/http"
)

// overview aggregates high-level instance stats for the dashboard.
func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	db := h.store.DB

	var total, active int64
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM accounts").Scan(&total)
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM accounts WHERE deactivated_at IS NULL").Scan(&active)

	var records int64
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM repo_records").Scan(&records)

	var blobCount, blobBytes int64
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(size),0) FROM blobs").Scan(&blobCount, &blobBytes)

	var blockCount, blockBytes int64
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(size),0) FROM repo_blocks").Scan(&blockCount, &blockBytes)

	var relays, firehoseEvents int64
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM relays").Scan(&relays)
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM firehose_events").Scan(&firehoseEvents)

	var pageCount, pageSize int64
	_ = db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount)
	_ = db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize)

	writeJSON(w, map[string]any{
		"version":        version,
		"serviceDid":     h.cfg.EffectiveServiceDID(),
		"didMethod":      h.cfg.DIDMethod,
		"publicUrl":      h.cfg.PublicURL,
		"listenAddr":     h.cfg.ListenAddr,
		"dbPath":         h.cfg.DatabasePath,
		"dataDir":        h.cfg.DataDir,
		"inviteRequired": h.cfg.InviteRequired,
		"secretSet":      h.cfg.Secret != "",
		"accounts":       map[string]any{"total": total, "active": active},
		"records":        records,
		"blobs":          map[string]any{"count": blobCount, "bytes": blobBytes},
		"blocks":         map[string]any{"count": blockCount, "bytes": blockBytes},
		"dbSizeBytes":    pageCount * pageSize,
		"relays":         relays,
		"firehoseEvents": firehoseEvents,
	})
}

// diagnostics reports server-side config and local state; the setup wizard
// combines this with browser-side reachability checks.
func (h *Handler) diagnostics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var one int
	dbOK := h.store.DB.QueryRowContext(ctx, "SELECT 1").Scan(&one) == nil

	var accountCount int64
	_ = h.store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM accounts").Scan(&accountCount)

	writeJSON(w, map[string]any{
		"version":         version,
		"publicUrl":       h.cfg.PublicURL,
		"publicUrlHttps":  h.cfg.PublicURL != "" && len(h.cfg.PublicURL) >= 5 && h.cfg.PublicURL[:5] == "https",
		"secretSet":       h.cfg.Secret != "",
		"serviceDidSet":   h.cfg.EffectiveServiceDID() != "",
		"didMethod":       h.cfg.DIDMethod,
		"inviteRequired":  h.cfg.InviteRequired,
		"dbOk":            dbOK,
		"accountCount":    accountCount,
		"tunnelInstalled": h.tunnels != nil && h.tunnels.Installed(),
		"tunnelReady":     h.tunnels != nil && h.tunnels.Ready(),
		"tunnelRunning":   h.tunnels != nil && h.tunnels.Running(),
	})
}
