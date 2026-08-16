# PocketPDS

A single-binary **Personal Data Server (PDS)** for the [AT Protocol](https://atproto.com) — the decentralized network behind Bluesky — written in Go. It stores everything in one SQLite file plus a blob directory, runs from a static binary, and fits comfortably on a micro-VPS or Raspberry Pi (~30 MB idle).

> **Status: experimental.** You can create accounts, write records, serve them to relays, and stream the firehose, and the data round-trips through the official `indigo` libraries. It is *not* yet hardened for production (see [Limitations](#limitations)).

## Features

**Identity.** Accounts get a `did:web` or `did:plc` DID backed by generated P-256 keys. The PDS hosts DID documents at `.well-known/did.json` (and `/{handle}/did.json`) so a reverse proxy can expose them for resolution, and supports `updateHandle` for changing an account's handle.

**Signed repositories.** Records live in a Merkle Search Tree (MST) with signed commits, the same structure relays expect. Writes go through the standard `com.atproto.repo.createRecord` / `putRecord` / `deleteRecord` / `applyWrites` endpoints, including `swapCommit`/`swapRecord` compare-and-swap for atomic read-modify-write updates.

**Relay sync and firehose.** The full `com.atproto.sync.*` surface — full and incremental `getRepo` CARs, `getRecord`, `getBlocks`, `listRepos`/`listBlobs` — lets a relay crawl your accounts. `subscribeRepos` streams live DAG-CBOR events (`#commit`, `#identity`, `#account`) that are wire-compatible with indigo, so standard relays can consume them.

**Accounts, auth, and blobs.** App passwords, invite codes, email verification and password reset (SMTP, or log-only without a host), and account deactivate/activate/delete. Media uploads are content-addressed on the filesystem. A minimal web dashboard at `/admin` covers account and invite management.

**Zero external services.** A pure-Go SQLite driver (`modernc.org/sqlite`) means no CGO, no Redis, no Postgres — just the binary and a data directory.

## Quick start

Requires Go 1.26+ (or use [Docker](#docker)).

```bash
git clone https://github.com/alesr/pocketPDS
cd pocketPDS
go build ./cmd/pocketpds

# Seed a dev account (dev.example.com / password) and start serving.
POCKETPDS_DB=./data.db ./pocketpds -seed
```

Then talk to it over XRPC:

```bash
BASE=http://127.0.0.1:3000

# log in
TOKEN=$(curl -s -X POST $BASE/xrpc/com.atproto.server.createSession \
  -H 'Content-Type: application/json' \
  -d '{"identifier":"dev.example.com","password":"password"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["accessJwt"])')

# create a post
curl -s -X POST $BASE/xrpc/com.atproto.repo.createRecord \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"repo":"dev.example.com","collection":"app.bsky.feed.post","record":{"$type":"app.bsky.feed.post","text":"hello world","createdAt":"2026-08-15T00:00:00.000Z"}}'

# read it back
curl -s "$BASE/xrpc/com.atproto.repo.listRecords?repo=dev.example.com&collection=app.bsky.feed.post"

# download the repo as a CAR (what a relay does)
curl -s "$BASE/xrpc/com.atproto.sync.getRepo?did=did:web:dev.example.com" -o repo.car
```

## Configuration

All settings come from environment variables:

| Variable | Default | Description |
|---|---|---|
| `POCKETPDS_LISTEN` | `127.0.0.1:3000` | HTTP listen address |
| `POCKETPDS_DB` | `pocketpds.db` | Path to the SQLite database file |
| `POCKETPDS_DATA_DIR` | `./data` | Directory for blob storage (`<dir>/blobs/`) |
| `POCKETPDS_SERVICE_DID` | *(empty)* | The service's own DID, reported by `describeServer` |
| `POCKETPDS_PUBLIC_URL` | `http://127.0.0.1:3000` | Public URL of this PDS, used in DID documents |
| `POCKETPDS_SECRET` | *(empty — dev fallback)* | Secret used to encrypt account keys at rest and sign access JWTs. **Set this before exposing the instance.** |
| `POCKETPDS_DID_METHOD` | `web` | `web` (`did:web`) or `plc` (`did:plc` via plc.directory) |
| `POCKETPDS_PLC_URL` | `https://plc.directory` | PLC directory endpoint (for `did:plc`) |
| `POCKETPDS_INVITE_REQUIRED` | `false` | Require an invite code to create an account (`true`) |
| `POCKETPDS_ADMIN_TOKEN` | *(empty — disabled)* | Token for the `/admin` dashboard |
| `POCKETPDS_SMTP_HOST` | *(empty — log-only)* | SMTP host for email (verification/reset) |
| `POCKETPDS_SMTP_PORT` | `587` | SMTP port |
| `POCKETPDS_SMTP_USER` / `_PASS` | *(empty)* | SMTP credentials |
| `POCKETPDS_SMTP_FROM` | *(empty)* | SMTP `From` address |

## CLI

```
pocketpds serve [-seed]        # run the server (default; a leading -flag implies serve)
pocketpds accounts             # list accounts
pocketpds accounts delete <h>  # delete an account
pocketpds version
```

## Docker

```bash
docker compose -f build/docker-compose.yml up --build
```

Edit `build/docker-compose.yml` to set `POCKETPDS_PUBLIC_URL`, `POCKETPDS_SECRET`, and
`POCKETPDS_ADMIN_TOKEN`. A systemd unit is provided at
[`build/pocketpds.service`](build/pocketpds.service), and a full self-host
runbook (DNS, reverse proxy, `did:web`, relay onboarding) at
[`build/README.md`](build/README.md) with [`build/verify.sh`](build/verify.sh).

## API surface

Routes are standard XRPC under `/xrpc/{nsid}`:

- **Server/auth** — `createSession`, `refreshSession`, `getSession`, `deleteSession`, `createAccount`, `deactivateAccount`, `activateAccount`, `deleteAccount`, `checkAccountStatus`, `describeServer`, `createAppPassword`, `listAppPasswords`, `revokeAppPassword`, `createInviteCodes`, `getAccountInviteCodes`, `requestEmailConfirmation`, `confirmEmail`, `requestPasswordReset`, `resetPassword`
- **Identity** — `resolveHandle`, `resolveDid`, `updateHandle`
- **Repo** — `createRecord`, `putRecord`, `deleteRecord`, `applyWrites`, `getRecord`, `listRecords`, `describeRepo`, `uploadBlob`
- **Sync** — `getLatestCommit`, `getRepo`, `getCheckout`, `getRecord`, `getBlocks`, `getBlob`, `listRepos`, `listBlobs`, `getHostStatus`, `getRepoStatus`, `subscribeRepos` (streaming), `notifyOfUpdate`, `requestCrawl`

Plus `GET /xrpc/_health`, the DID document routes (`/.well-known/did.json`,
`/.well-known/atproto-did`, `/{handle}/did.json`), and the admin dashboard
(`/admin`).

## How it works

PocketPDS is built on the official [`bluesky-social/indigo`](https://github.com/bluesky-social/indigo) Go libraries:

- **Writes** use indigo's top-level `repo` package (`NewRepo`, `CreateRecord`, `Commit`) to build Merkle Search Trees and sign commits with each account's P-256 key.
- **Reads/sync** use indigo's `atproto/repo` and `atproto/identity` packages.
- The MST is stored as DAG-CBOR blocks in a SQLite blockstore; a denormalized `repo_records` index powers `getRecord`/`listRecords`; `repo_block_revs` tracks which commit introduced each block so `getRepo?since=rev` can serve incremental CARs.
- The firehose serializes DAG-CBOR frames wire-compatible with indigo's own event stream.

For a deeper dive into the design and the gotchas, see [`AGENTS.md`](AGENTS.md).

## Development

```bash
go build ./...
go vet ./...
go test ./...
```

Integration tests round-trip produced commits and CARs through indigo's own read/verify path (`internal/repo/manager_test.go`).

## Limitations

- **`did:web` resolution is up to you.** Accounts get a `did:web:<handle>` DID, but for anything on the network to resolve it you must serve the DID document at `https://<handle>/.well-known/did.json` (the PDS serves it; point your domain at it via a reverse proxy).
- **Not yet implemented:** `did:plc` recovery-key custody (keys are generated and held by the PDS), `#sync` firehose events, `listMissingBlobs`, `listReposByCollection`, `importRepo`.

## License

[MIT](LICENSE)
