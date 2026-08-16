package admin

import "net/http"

// listRelays returns relays registered via notifyOfUpdate.
func (h *Handler) listRelays(w http.ResponseWriter, r *http.Request) {
	rows, err := h.store.DB.QueryContext(r.Context(),
		"SELECT hostname, registered_at FROM relays ORDER BY registered_at DESC")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = rows.Close() }()

	var relays = make([]map[string]any, 0)
	for rows.Next() {
		var hostname, registeredAt string
		if err := rows.Scan(&hostname, &registeredAt); err != nil {
			continue
		}
		relays = append(relays, map[string]any{"hostname": hostname, "registeredAt": registeredAt})
	}
	writeJSON(w, map[string]any{"relays": relays})
}

// requestCrawl asks every registered relay to crawl this PDS.
func (h *Handler) requestCrawl(w http.ResponseWriter, _ *http.Request) {
	h.mgr.NotifyRelays()
	writeJSON(w, map[string]any{"ok": true})
}
