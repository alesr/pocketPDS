# Getting started

## Build

    go build ./cmd/pocketpds

Requires Go 1.26+.

## Run

    POCKETPDS_DB=./pocketpds.db ./pocketpds -seed

`-seed` creates a dev account (`dev.example.com` / `password`) if none exists.
The server listens on `127.0.0.1:3000` by default.

## Try it

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

    # export the repo as a CAR (what a relay does)
    curl -s "$BASE/xrpc/com.atproto.sync.getRepo?did=did:web:dev.example.com" -o repo.car

## Configuration

All settings come from environment variables.

| Variable | Default | Description |
|---|---|---|
| `POCKETPDS_LISTEN` | `127.0.0.1:3000` | HTTP listen address |
| `POCKETPDS_DB` | `pocketpds.db` | SQLite database path |
| `POCKETPDS_DATA_DIR` | `./data` | Blob storage (`<dir>/blobs/`) |
| `POCKETPDS_SERVICE_DID` | *(empty)* | Service DID, reported by `describeServer` |
| `POCKETPDS_PUBLIC_URL` | `http://127.0.0.1:3000` | Public URL, used in DID documents |
| `POCKETPDS_SECRET` | *(empty, dev fallback)* | Encrypts account keys and signs JWTs. Set it before exposing the instance. |
| `POCKETPDS_DID_METHOD` | `web` | `web` (did:web) or `plc` (did:plc via plc.directory) |
| `POCKETPDS_PLC_URL` | `https://plc.directory` | PLC directory endpoint |
| `POCKETPDS_INVITE_REQUIRED` | `false` | Require an invite code to create an account |
| `POCKETPDS_ADMIN_TOKEN` | *(empty, disabled)* | Token for `/admin` |
| `POCKETPDS_TRUST_PROXY` | `false` | Trust `X-Forwarded-For` from the proxy |
| `POCKETPDS_BLOB_SIZE_LIMIT` | `5242880` | Max blob size in bytes (default 5 MiB) |
| `POCKETPDS_APPVIEW_PROXY` | `https://public.api.bsky.app` | Public AppView for network reads; empty disables them |
| `POCKETPDS_SMTP_HOST` | *(empty, log-only)* | SMTP host for email |
| `POCKETPDS_SMTP_PORT` | `587` | SMTP port |
| `POCKETPDS_SMTP_USER` / `_PASS` | *(empty)* | SMTP credentials |
| `POCKETPDS_SMTP_FROM` | *(empty)* | SMTP From address |

## CLI

    pocketpds serve [-seed]        # default; a leading -flag implies serve
    pocketpds accounts             # list accounts
    pocketpds accounts delete <h>  # delete an account
    pocketpds version

## Docker

    docker compose -f build/docker-compose.yml up --build

## Running for real

For DNS, HTTPS, `did:web` resolution, and relay onboarding, see
[../build/README.md](../build/README.md).
