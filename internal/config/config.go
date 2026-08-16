package config

import (
	"log/slog"
	"net/url"
	"os"
	"strconv"
)

const defaultBlobSizeLimit = 5 * 1024 * 1024 // 5 MiB, matching the official PDS

// defaultAppviewProxy is the public read-only AppView that network reads
// (search, remote profiles/feeds/threads) are proxied to when
// POCKETPDS_APPVIEW_PROXY is unset. Set it to "" to disable network access.
const defaultAppviewProxy = "https://public.api.bsky.app"

type Config struct {
	ListenAddr      string
	DatabasePath    string
	DataDir         string
	ServiceDID      string
	PublicURL       string
	Secret          string
	PlcURL          string
	DIDMethod       string
	InviteRequired  bool
	AdminToken      string
	TrustProxy      bool
	BlobSizeLimit   int64
	AppviewProxyURL string

	SMTPHost string
	SMTPPort string
	SMTPUser string
	SMTPPass string
	SMTPFrom string

	LogLevel slog.Level
}

func FromEnv() *Config {
	return &Config{
		ListenAddr:      getenv("POCKETPDS_LISTEN", "127.0.0.1:3000"),
		DatabasePath:    getenv("POCKETPDS_DB", "pocketpds.db"),
		DataDir:         getenv("POCKETPDS_DATA_DIR", "./data"),
		ServiceDID:      os.Getenv("POCKETPDS_SERVICE_DID"),
		PublicURL:       getenv("POCKETPDS_PUBLIC_URL", "http://127.0.0.1:3000"),
		Secret:          os.Getenv("POCKETPDS_SECRET"),
		PlcURL:          getenv("POCKETPDS_PLC_URL", "https://plc.directory"),
		DIDMethod:       getenv("POCKETPDS_DID_METHOD", "web"),
		InviteRequired:  os.Getenv("POCKETPDS_INVITE_REQUIRED") == "true",
		AdminToken:      os.Getenv("POCKETPDS_ADMIN_TOKEN"),
		TrustProxy:      os.Getenv("POCKETPDS_TRUST_PROXY") == "true",
		BlobSizeLimit:   getenvInt64("POCKETPDS_BLOB_SIZE_LIMIT", defaultBlobSizeLimit),
		AppviewProxyURL: appviewProxyFromEnv(),

		SMTPHost: os.Getenv("POCKETPDS_SMTP_HOST"),
		SMTPPort: getenv("POCKETPDS_SMTP_PORT", "587"),
		SMTPUser: os.Getenv("POCKETPDS_SMTP_USER"),
		SMTPPass: os.Getenv("POCKETPDS_SMTP_PASS"),
		SMTPFrom: getenv("POCKETPDS_SMTP_FROM", ""),

		LogLevel: slog.LevelInfo,
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func appviewProxyFromEnv() string {
	if v, ok := os.LookupEnv("POCKETPDS_APPVIEW_PROXY"); ok {
		return v
	}
	return defaultAppviewProxy
}

// EffectiveServiceDID returns the configured service DID, or derives one from
// the public URL when the DID method is web (matching the reference PDS, which
// defaults the service DID to did:web:<hostname>).
func (c *Config) EffectiveServiceDID() string {
	if c.ServiceDID != "" {
		return c.ServiceDID
	}
	if c.DIDMethod != "web" {
		return ""
	}
	u, err := url.Parse(c.PublicURL)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	host := u.Hostname()
	if port := u.Port(); port != "" {
		def := "443"
		if u.Scheme == "http" {
			def = "80"
		}
		if port != def {
			host += "%3A" + port
		}
	}
	return "did:web:" + host
}

func getenvInt64(key string, def int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return def
	}
	return n
}
