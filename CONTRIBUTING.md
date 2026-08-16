# Contributing

PRs welcome.

## Setup

- Go 1.26+.
- No CGO, no external services.

## Layout

- `cmd/pocketpds` - entrypoint (CLI + server)
- `internal/api` - XRPC handlers and the single-user AppView
- `internal/admin` - embedded admin dashboard
- `internal/blob` - filesystem blob storage
- `internal/bridge` - Bluesky bridge (publish/archive to a bsky.social account)
- `internal/cloudflare` - Cloudflare API client (tunnels, DNS)
- `internal/config` - environment configuration
- `internal/crypto` - AES-256-GCM box for keys at rest
- `internal/db` - SQLite storage and migrations
- `internal/email` - SMTP sender (log-only fallback)
- `internal/firehose` - event emitter and DAG-CBOR framing
- `internal/identity` - did:web key/document generation
- `internal/plc` - did:plc client
- `internal/repo` - indigo write path, MST/commit management, CAR sync
- `internal/server` - HTTP mux, middleware, rate limiting
- `internal/tunnel` - cloudflared tunnel supervision
- `internal/xrpc` - XRPC envelope helpers
- `build/` - Docker, systemd unit, self-host runbook, pinned dev tools

## Conventions

- XRPC errors use the `{"error","message"}` envelope.
- Repo writes go through `github.com/bluesky-social/indigo/repo`; `atproto/repo`
  is read/verify only.
- Add a new `migration` entry in `internal/db/db.go` for schema changes; never
  edit an existing migration.
- Keep dependencies minimal.

## Testing

    go test ./...

Integration tests round-trip commits, CARs, and firehose frames through
indigo's read/verify path.
