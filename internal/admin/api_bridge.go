package admin

import (
	"net/http"
	"strings"

	"github.com/alesr/pocketPDS/internal/cloudflare"
	"github.com/alesr/pocketPDS/internal/xrpc"
)

// bridgeGet returns the current bsky.social bridge configuration.
func (h *Handler) bridgeGet(w http.ResponseWriter, r *http.Request) {
	handle, passwordSet, err := h.bridge.Config(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"handle":      handle,
		"passwordSet": passwordSet,
		"configured":  handle != "" && passwordSet,
	})
}

// bridgePut stores the handle and/or app password. An empty app password leaves
// the existing one unchanged.
func (h *Handler) bridgePut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Handle      string `json:"handle"`
		AppPassword string `json:"appPassword"`
	}
	if xrpc.DecodeBody(w, r, &in) != nil {
		return
	}
	if err := h.bridge.SetConfig(r.Context(), strings.TrimSpace(in.Handle), in.AppPassword); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// bridgeSync runs a publish + archive sync against bsky.social.
func (h *Handler) bridgeSync(w http.ResponseWriter, r *http.Request) {
	rep, err := h.bridge.Sync(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, rep)
}

// bridgeDNS adds the `_atproto.<domain>` TXT record required to use a custom
// domain as a bsky.social handle.
func (h *Handler) bridgeDNS(w http.ResponseWriter, r *http.Request) {
	var in struct {
		APIToken string `json:"apiToken"`
		Zone     string `json:"zone"`
		Domain   string `json:"domain"`
		Did      string `json:"did"`
	}
	if xrpc.DecodeBody(w, r, &in) != nil {
		return
	}
	in.APIToken = strings.TrimSpace(in.APIToken)
	in.Zone = strings.ToLower(strings.TrimSpace(in.Zone))
	in.Domain = strings.ToLower(strings.TrimSpace(in.Domain))
	in.Did = strings.TrimSpace(in.Did)

	if in.APIToken == "" || in.Zone == "" || in.Domain == "" || in.Did == "" {
		http.Error(w, "apiToken, zone, domain and did are required", http.StatusBadRequest)
		return
	}

	client := cloudflare.New(in.APIToken)
	info, err := client.ZoneInfo(r.Context(), in.Zone)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if err := client.UpsertDNS(r.Context(), info.ID, "TXT", "_atproto."+in.Domain, "did="+in.Did, false); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{
		"ok":         true,
		"txtRecord":  "_atproto." + in.Domain,
		"txtContent": "did=" + in.Did,
	})
}
