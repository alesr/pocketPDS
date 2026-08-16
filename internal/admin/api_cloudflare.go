package admin

import (
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/alesr/pocketPDS/internal/cloudflare"
	"github.com/alesr/pocketPDS/internal/config"
	"github.com/alesr/pocketPDS/internal/xrpc"
)

// cloudflareApply provisions DNS and a Cloudflare Tunnel for a hostname: it
// creates a locally-managed tunnel (or reuses persisted credentials), writes
// the cloudflared config, upserts the CNAME + `_atproto` TXT records, and
// restarts the supervised tunnel. It then persists public_url and did_method.
func (h *Handler) cloudflareApply(w http.ResponseWriter, r *http.Request) {
	var in struct {
		APIToken string `json:"apiToken"`
		Zone     string `json:"zone"`
		Hostname string `json:"hostname"`
	}
	if xrpc.DecodeBody(w, r, &in) != nil {
		return
	}

	in.APIToken = strings.TrimSpace(in.APIToken)
	in.Zone = strings.ToLower(strings.TrimSpace(in.Zone))
	in.Hostname = strings.ToLower(strings.TrimSpace(in.Hostname))

	if in.APIToken == "" || in.Zone == "" || in.Hostname == "" {
		http.Error(w, "apiToken, zone and hostname are required", http.StatusBadRequest)
		return
	}
	if !strings.Contains(in.Hostname, ".") {
		http.Error(w, "hostname must be a fully-qualified domain", http.StatusBadRequest)
		return
	}
	if h.tunnels == nil {
		http.Error(w, "tunnel manager unavailable", http.StatusInternalServerError)
		return
	}

	client := cloudflare.New(in.APIToken)
	info, err := client.ZoneInfo(r.Context(), in.Zone)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if info.AccountID == "" {
		http.Error(w, "cloudflare: could not determine account ID (token needs account access)", http.StatusBadGateway)
		return
	}

	// Reuse persisted credentials when present, else create a new tunnel.
	creds, ok := h.tunnels.LoadCredentials()
	if !ok {
		name := strings.ReplaceAll(in.Hostname, ".", "-")
		t, err := client.CreateTunnel(r.Context(), info.AccountID, name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		creds = t.CredentialsFile
	}

	cname := creds.TunnelID + ".cfargotunnel.com"
	if err := client.UpsertDNS(r.Context(), info.ID, "CNAME", in.Hostname, cname, true); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if err := client.UpsertDNS(r.Context(), info.ID, "TXT", "_atproto."+in.Hostname, "did=did:web:"+in.Hostname, false); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	if err := h.tunnels.Provision(creds, in.Hostname, ingressPort(h.cfg.ListenAddr)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.tunnels.Restart()

	if err := h.store.SetSetting(r.Context(), config.SettingPublicURL, "https://"+in.Hostname); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.store.SetSetting(r.Context(), config.SettingDIDMethod, "web"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := map[string]any{
		"ok":            true,
		"tunnelId":      creds.TunnelID,
		"cname":         in.Hostname,
		"txtRecord":     "_atproto." + in.Hostname,
		"txtContent":    "did=did:web:" + in.Hostname,
		"tunnelRunning": h.tunnels.Running(),
	}
	if !h.tunnels.Ready() {
		resp["bootstrapCommand"] = "sudo pocketpds tunnel install"
	}

	writeJSON(w, resp)
}

// ingressPort derives the local port the tunnel should forward to from the
// PDS listen address (default 3000).
func ingressPort(addr string) int {
	if _, port, err := net.SplitHostPort(addr); err == nil {
		if p, err := strconv.Atoi(port); err == nil {
			return p
		}
	}
	return 3000
}
