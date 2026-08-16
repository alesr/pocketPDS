package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/alesr/pocketPDS/internal/bridge"
	"github.com/alesr/pocketPDS/internal/config"
	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/repo"
	"github.com/alesr/pocketPDS/internal/tunnel"
	"github.com/alesr/pocketPDS/internal/xrpc"
)

const (
	version    = "0.1.0"
	cookieName = "pocketpds_admin"
)

//go:embed static
var staticFS embed.FS

// Handler serves the embedded operator console under /admin and its JSON API.
type Handler struct {
	cfg     *config.Config
	store   *db.Store
	mgr     *repo.Manager
	tunnels *tunnel.Manager
	bridge  *bridge.Service
	token   string
	fsys    fs.FS

	loginLimiter *rateLimiter
}

func New(cfg *config.Config, store *db.Store, mgr *repo.Manager, tunnels *tunnel.Manager, bridgeSvc *bridge.Service) *Handler {
	fsys, _ := fs.Sub(staticFS, "static")
	return &Handler{
		cfg:          cfg,
		store:        store,
		mgr:          mgr,
		tunnels:      tunnels,
		bridge:       bridgeSvc,
		token:        cfg.AdminToken,
		fsys:         fsys,
		loginLimiter: newRateLimiter(5, 15*time.Second),
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	// Static shell + assets (no secrets; served without auth). Registered as
	// explicit paths — a "/admin/" subtree pattern would clash with the
	// "GET /{handle}/did.json" route in the server package.
	mux.HandleFunc("GET /admin", h.index)
	mux.HandleFunc("GET /admin/app.js", h.file("app.js"))
	mux.HandleFunc("GET /admin/styles.css", h.file("styles.css"))
	mux.HandleFunc("GET /admin/vendor/preact.umd.js", h.file("vendor/preact.umd.js"))
	mux.HandleFunc("GET /admin/vendor/hooks.umd.js", h.file("vendor/hooks.umd.js"))
	mux.HandleFunc("GET /admin/vendor/htm.umd.js", h.file("vendor/htm.umd.js"))

	// Session endpoints.
	mux.HandleFunc("POST /admin/api/login", h.login)
	mux.HandleFunc("POST /admin/api/logout", h.logout)
	mux.HandleFunc("GET /admin/api/me", h.me)

	// Overview / diagnostics.
	mux.HandleFunc("GET /admin/api/overview", h.auth(h.overview))
	mux.HandleFunc("GET /admin/api/diagnostics", h.auth(h.diagnostics))

	// Settings + restart.
	mux.HandleFunc("GET /admin/api/settings", h.auth(h.settingsGet))
	mux.HandleFunc("PUT /admin/api/settings", h.auth(h.settingsPut))
	mux.HandleFunc("POST /admin/api/restart", h.auth(h.restart))
	mux.HandleFunc("POST /admin/api/cloudflare/apply", h.auth(h.cloudflareApply))
	mux.HandleFunc("GET /admin/api/bridge", h.auth(h.bridgeGet))
	mux.HandleFunc("PUT /admin/api/bridge", h.auth(h.bridgePut))
	mux.HandleFunc("POST /admin/api/bridge/sync", h.auth(h.bridgeSync))
	mux.HandleFunc("POST /admin/api/bridge/dns", h.auth(h.bridgeDNS))

	// Accounts.
	mux.HandleFunc("GET /admin/api/accounts", h.auth(h.listAccounts))
	mux.HandleFunc("GET /admin/api/accounts/{did}", h.auth(h.accountDetail))
	mux.HandleFunc("GET /admin/api/accounts/{did}/records", h.auth(h.accountRecords))
	mux.HandleFunc("GET /admin/api/accounts/{did}/commits", h.auth(h.accountCommits))
	mux.HandleFunc("GET /admin/api/accounts/{did}/blobs", h.auth(h.accountBlobs))
	mux.HandleFunc("GET /admin/api/accounts/{did}/sessions", h.auth(h.accountSessions))
	mux.HandleFunc("GET /admin/api/accounts/{did}/appPasswords", h.auth(h.appPasswordList))
	mux.HandleFunc("POST /admin/api/accounts/{did}/appPasswords", h.auth(h.appPasswordCreate))
	mux.HandleFunc("POST /admin/api/accounts/{did}/appPasswords/revoke", h.auth(h.appPasswordRevoke))
	mux.HandleFunc("POST /admin/api/accounts/{did}/deactivate", h.auth(h.deactivate))
	mux.HandleFunc("POST /admin/api/accounts/{did}/activate", h.auth(h.activate))
	mux.HandleFunc("POST /admin/api/accounts/{did}/delete", h.auth(h.deleteAccount))

	// Blobs + relays + invite codes + email tokens.
	mux.HandleFunc("GET /admin/api/blobs", h.auth(h.listBlobs))
	mux.HandleFunc("GET /admin/api/relays", h.auth(h.listRelays))
	mux.HandleFunc("POST /admin/api/relays/crawl", h.auth(h.requestCrawl))
	mux.HandleFunc("GET /admin/api/inviteCodes", h.auth(h.listInviteCodes))
	mux.HandleFunc("POST /admin/api/inviteCodes", h.auth(h.createInviteCodes))
	mux.HandleFunc("GET /admin/api/emailTokens", h.auth(h.listEmailTokens))
	mux.HandleFunc("GET /admin/api/events", h.auth(h.eventsStream))
}

// --- auth ---

// auth gates a handler on the admin session. The httpOnly cookie is checked
// first; an Authorization: Bearer header is accepted as a fallback for
// non-browser clients (curl, scripts).
func (h *Handler) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.authenticated(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !h.sameOrigin(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (h *Handler) authenticated(r *http.Request) bool {
	if h.token == "" {
		return false
	}
	if c, err := r.Cookie(cookieName); err == nil && tokenEqual(c.Value, h.token) {
		return true
	}
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && tokenEqual(tok, h.token)
}

// sameOrigin is a CSRF second layer (SameSite=Strict already blocks cross-site
// cookie sends). Requests with no Origin/Referer (curl) are allowed.
func (h *Handler) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}

func (h *Handler) secure() bool {
	return strings.HasPrefix(h.cfg.PublicURL, "https://")
}

func (h *Handler) setSessionCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/admin",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   h.secure(),
	})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if xrpc.DecodeBody(w, r, &in) != nil {
		return
	}
	if in.Token == "" {
		http.Error(w, "token required", http.StatusBadRequest)
		return
	}
	if !h.loginLimiter.allow(clientIP(r)) {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	if h.token == "" || !tokenEqual(in.Token, h.token) {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	h.setSessionCookie(w, h.token, 7*24*3600)
	writeJSON(w, map[string]any{"ok": true})
}

func (h *Handler) logout(w http.ResponseWriter, _ *http.Request) {
	h.setSessionCookie(w, "", -1)
	writeJSON(w, map[string]any{"ok": true})
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	if !h.authenticated(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "version": version})
}

// --- static ---

func (h *Handler) index(w http.ResponseWriter, _ *http.Request) {
	b, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

// file serves a single embedded static asset with correct content type and
// range support.
func (h *Handler) file(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, h.fsys, name)
	}
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// tokenEqual compares two secrets in constant time, hashing first so that
// differing lengths do not leak timing information.
func tokenEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

// rateLimiter is a minimal per-key sliding-window limiter, scoped to the admin
// login endpoint (the server package's limiter lives in a package that imports
// admin, so it cannot be reused here).
type rateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string][]time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{limit: limit, window: window, hits: make(map[string][]time.Time)}
}

func (l *rateLimiter) allow(key string) bool {
	now := time.Now()
	cutoff := now.Add(-l.window)
	l.mu.Lock()
	defer l.mu.Unlock()

	keep := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	if len(keep) >= l.limit {
		l.hits[key] = keep
		return false
	}
	l.hits[key] = append(keep, now)
	return true
}

func clientIP(r *http.Request) string {
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		return xf
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}

// --- accounts (existing, extended) ---

type accountView struct {
	Did         string  `json:"did"`
	Handle      string  `json:"handle"`
	Email       *string `json:"email"`
	Active      bool    `json:"active"`
	CreatedAt   string  `json:"createdAt"`
	RecordCount int64   `json:"recordCount"`
	BlobCount   int64   `json:"blobCount"`
}

func (h *Handler) listAccounts(w http.ResponseWriter, r *http.Request) {
	rows, err := h.store.DB.QueryContext(r.Context(),
		`SELECT a.did, a.handle, a.email, a.deactivated_at, a.created_at,
			(SELECT COUNT(*) FROM repo_records rr WHERE rr.did = a.did),
			(SELECT COUNT(*) FROM blobs b WHERE b.did = a.did)
		 FROM accounts a ORDER BY a.created_at DESC`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = rows.Close() }()

	var accounts = make([]accountView, 0)
	for rows.Next() {
		var a accountView
		var deactivated *string
		if err := rows.Scan(&a.Did, &a.Handle, &a.Email, &deactivated, &a.CreatedAt, &a.RecordCount, &a.BlobCount); err != nil {
			continue
		}
		a.Active = deactivated == nil
		accounts = append(accounts, a)
	}
	writeJSON(w, map[string]any{"accounts": accounts})
}

func (h *Handler) deactivate(w http.ResponseWriter, r *http.Request) {
	did := r.PathValue("did")
	_, err := h.store.DB.ExecContext(r.Context(),
		"UPDATE accounts SET deactivated_at = ? WHERE did = ?", time.Now().Format(time.RFC3339), did)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.mgr.EmitAccount(r.Context(), did, false, new("deactivated"))
	writeJSON(w, map[string]any{"ok": true})
}

func (h *Handler) activate(w http.ResponseWriter, r *http.Request) {
	did := r.PathValue("did")
	_, err := h.store.DB.ExecContext(r.Context(),
		"UPDATE accounts SET deactivated_at = NULL WHERE did = ?", did)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.mgr.EmitAccount(r.Context(), did, true, nil)
	writeJSON(w, map[string]any{"ok": true})
}

func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	did := r.PathValue("did")
	if err := h.mgr.DeleteAccount(r.Context(), did); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := h.store.DB.ExecContext(r.Context(), "DELETE FROM accounts WHERE did = ?", did); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.mgr.EmitAccount(r.Context(), did, false, new("deleted"))
	writeJSON(w, map[string]any{"ok": true})
}

func (h *Handler) listInviteCodes(w http.ResponseWriter, r *http.Request) {
	rows, err := h.store.DB.QueryContext(r.Context(),
		"SELECT code, created_by, created_at, used_by, disabled_at FROM invite_codes ORDER BY created_at DESC")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = rows.Close() }()

	var codes = make([]map[string]any, 0)
	for rows.Next() {
		var code, createdBy, createdAt string
		var usedBy, disabledAt *string
		if err := rows.Scan(&code, &createdBy, &createdAt, &usedBy, &disabledAt); err != nil {
			continue
		}
		codes = append(codes, map[string]any{
			"code":      code,
			"createdBy": createdBy,
			"createdAt": createdAt,
			"available": usedBy == nil && disabledAt == nil,
		})
	}
	writeJSON(w, map[string]any{"codes": codes})
}

func (h *Handler) createInviteCodes(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Count int `json:"count"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if in.Count < 1 || in.Count > 100 {
		http.Error(w, "count must be 1-100", http.StatusBadRequest)
		return
	}
	now := time.Now().Format(time.RFC3339)
	var codes []string
	for i := 0; i < in.Count; i++ {
		code := "pocketpds-" + randomHex(8)
		if _, err := h.store.DB.ExecContext(r.Context(),
			"INSERT INTO invite_codes (code, created_by, created_at) VALUES (?, 'admin', ?)",
			code, now); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		codes = append(codes, code)
	}
	writeJSON(w, map[string]any{"codes": codes})
}
