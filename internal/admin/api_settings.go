package admin

import (
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/alesr/pocketPDS/internal/config"
	"github.com/alesr/pocketPDS/internal/xrpc"
)

// settingsPayload is the wire shape for editable runtime settings.
type settingsPayload struct {
	PublicURL      string `json:"publicUrl"`
	ServiceDID     string `json:"serviceDid"`
	DIDMethod      string `json:"didMethod"`
	InviteRequired bool   `json:"inviteRequired"`
	AdminToken     string `json:"adminToken"`
	SMTPHost       string `json:"smtpHost"`
	SMTPPort       string `json:"smtpPort"`
	SMTPUser       string `json:"smtpUser"`
	SMTPPass       string `json:"smtpPass"`
	SMTPFrom       string `json:"smtpFrom"`
}

func (p settingsPayload) toMap() map[string]string {
	return map[string]string{
		config.SettingPublicURL:      p.PublicURL,
		config.SettingServiceDID:     p.ServiceDID,
		config.SettingDIDMethod:      p.DIDMethod,
		config.SettingInviteRequired: strconv.FormatBool(p.InviteRequired),
		config.SettingAdminToken:     p.AdminToken,
		config.SettingSMTPHost:       p.SMTPHost,
		config.SettingSMTPPort:       p.SMTPPort,
		config.SettingSMTPUser:       p.SMTPUser,
		config.SettingSMTPPass:       p.SMTPPass,
		config.SettingSMTPFrom:       p.SMTPFrom,
	}
}

func settingsFromConfig(c *config.Config) settingsPayload {
	return settingsPayload{
		PublicURL:      c.PublicURL,
		ServiceDID:     c.ServiceDID,
		DIDMethod:      c.DIDMethod,
		InviteRequired: c.InviteRequired,
		AdminToken:     c.AdminToken,
		SMTPHost:       c.SMTPHost,
		SMTPPort:       c.SMTPPort,
		SMTPUser:       c.SMTPUser,
		SMTPPass:       c.SMTPPass,
		SMTPFrom:       c.SMTPFrom,
	}
}

// settingsGet returns the configured settings (persisted values merged over the
// current effective config), plus metadata for the UI.
func (h *Handler) settingsGet(w http.ResponseWriter, r *http.Request) {
	persisted, err := h.store.LoadSettings(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	effective := settingsFromConfig(h.cfg)
	view := effective
	if p, ok := settingsFromMap(persisted, effective); ok {
		view = p
	}

	var accountCount int64
	_ = h.store.DB.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM accounts").Scan(&accountCount)

	writeJSON(w, map[string]any{
		"settings":        view,
		"secretSet":       h.cfg.Secret != "",
		"accountsExist":   accountCount > 0,
		"restartRequired": !settingsEqual(persisted, effective),
	})
}

// settingsPut validates and persists settings. Changes take effect on restart.
func (h *Handler) settingsPut(w http.ResponseWriter, r *http.Request) {
	var in settingsPayload
	if xrpc.DecodeBody(w, r, &in) != nil {
		return
	}

	pairs := in.toMap()
	for k, v := range pairs {
		if err := config.ValidateSetting(k, v); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	for k, v := range pairs {
		if err := h.store.SetSetting(r.Context(), k, v); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, map[string]any{"ok": true, "restartRequired": true})
}

// restart re-execs the current process to apply pending settings. Best-effort:
// if the process image is not re-executable (e.g. under `go run`), it fails
// silently and the UI tells the operator to restart manually.
func (h *Handler) restart(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"ok": true})
	go func() {
		time.Sleep(250 * time.Millisecond)
		// Shut the tunnel supervisor down first so the re-exec does not orphan
		// the cloudflared child process.
		if h.tunnels != nil {
			h.tunnels.Stop()
		}
		exe, err := os.Executable()
		if err != nil {
			slog.Error("restart: resolve executable", "err", err)
			return
		}
		if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
			slog.Error("restart: re-exec failed (restart manually)", "err", err)
		}
	}()
}

// settingsFromMap builds a payload from persisted settings, falling back to
// `fallback` for keys not present. Returns ok=false when nothing is persisted.
func settingsFromMap(m map[string]string, fallback settingsPayload) (settingsPayload, bool) {
	if len(m) == 0 {
		return settingsPayload{}, false
	}
	p := fallback
	if v, ok := m[config.SettingPublicURL]; ok {
		p.PublicURL = v
	}
	if v, ok := m[config.SettingServiceDID]; ok {
		p.ServiceDID = v
	}
	if v, ok := m[config.SettingDIDMethod]; ok {
		p.DIDMethod = v
	}
	if v, ok := m[config.SettingInviteRequired]; ok {
		p.InviteRequired = v == "true"
	}
	if v, ok := m[config.SettingAdminToken]; ok {
		p.AdminToken = v
	}
	if v, ok := m[config.SettingSMTPHost]; ok {
		p.SMTPHost = v
	}
	if v, ok := m[config.SettingSMTPPort]; ok {
		p.SMTPPort = v
	}
	if v, ok := m[config.SettingSMTPUser]; ok {
		p.SMTPUser = v
	}
	if v, ok := m[config.SettingSMTPPass]; ok {
		p.SMTPPass = v
	}
	if v, ok := m[config.SettingSMTPFrom]; ok {
		p.SMTPFrom = v
	}
	return p, true
}

// settingsEqual reports whether persisted settings match the effective config
// (i.e. no restart is pending).
func settingsEqual(persisted map[string]string, effective settingsPayload) bool {
	eff := effective.toMap()
	for k, v := range persisted {
		if eff[k] != v {
			return false
		}
	}
	return true
}
