# Contributing

Thanks for your interest in PocketPDS. Contributions are welcome.

## Setup

- Go 1.26 or later.
- No CGO, no external services required for local development.

## Layout

- `cmd/pocketpds` — entrypoint (CLI + server).
- `internal/api` — XRPC handlers.
- `internal/admin` — embedded admin dashboard.
- `internal/blob` — filesystem blob storage.
- `internal/config` — environment configuration.
- `internal/crypto` — AES-256-GCM box for encrypting keys at rest.
- `internal/db` — SQLite storage and migrations.
- `internal/email` — SMTP sender (log-only fallback).
- `internal/firehose` — event emitter and DAG-CBOR framing.
- `internal/identity` — `did:web` key/document generation.
- `internal/plc` — `did:plc` client.
- `internal/repo` — the indigo write path, MST/commit management, CAR sync.
- `internal/server` — HTTP mux, middleware, rate limiting.
- `internal/xrpc` — XRPC envelope helpers.

## Conventions

- XRPC helpers live in `internal/xrpc`; errors use the `{"error","message"}` envelope.
- Repo writes go through the top-level `github.com/bluesky-social/indigo/repo`
  package; `atproto/repo` is read/verify only.
- Add a new `migration` entry in `internal/db/db.go` for schema changes; never
  edit an existing migration.
- Keep dependencies minimal: prefer the standard library and code already in the
  module graph.

## Testing

Integration tests round-trip produced commits/CARs/firehose frames through
indigo's own read/verify path (`internal/repo/manager_test.go`,
`internal/firehose`, `internal/plc`).
