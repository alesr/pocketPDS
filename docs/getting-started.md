# Getting started

## Build from source

Requires Go 1.26+.

```bash
git clone https://github.com/alesr/pocketPDS
cd pocketPDS
go build ./cmd/pocketpds
```

## Run

```bash
POCKETPDS_SECRET=<long-random-string> POCKETPDS_DB=./pocketpds.db ./pocketpds -seed
```

`POCKETPDS_SECRET` is required. It encrypts account keys at rest and signs
access tokens. `-seed` creates a dev account (`dev.example.com` / `password`)
if none exists. The server listens on `127.0.0.1:3000` by default.

## Try it

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

# export the repo as a CAR (what a relay does)
curl -s "$BASE/xrpc/com.atproto.sync.getRepo?did=did:web:dev.example.com" -o repo.car
```

## Use a client

Hand-rolling XRPC is optional. Point a client at your instance instead:
[Graysky](https://graysky.app) lets you sign in with any PDS URL and reads
profiles and feeds through PocketPDS's AppView.

## Admin panel

`/admin` has the setup wizard and the Bluesky bridge. Link a bsky.social
account and hit Sync to publish your PDS posts there and archive their posts
back into your PDS.

## Configuration

All settings come from environment variables.

| Variable | Default | Description |
|---|---|---|
| `POCKETPDS_LISTEN` | `127.0.0.1:3000` | HTTP listen address |
| `POCKETPDS_DB` | `pocketpds.db` | SQLite database path |
| `POCKETPDS_DATA_DIR` | `./data` | Blob storage (`<dir>/blobs/`) |
| `POCKETPDS_SERVICE_DID` | *(empty)* | Service DID, reported by `describeServer` |
| `POCKETPDS_PUBLIC_URL` | `http://127.0.0.1:3000` | Public URL, used in DID documents |
| `POCKETPDS_SECRET` | *(required)* | Encrypts account keys and signs JWTs |
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

```bash
pocketpds serve [-seed]        # default; a leading -flag implies serve
pocketpds accounts             # list accounts
pocketpds accounts delete <h>  # delete an account
pocketpds accounts recover <h> # print the did:plc recovery key
pocketpds version
```

## Docker

Build locally:

```bash
docker compose -f build/docker-compose.yml up --build
```

Or run the published image:

```bash
docker run -d --name pocketpds \
  -e POCKETPDS_SECRET=<long-random-string> \
  -e POCKETPDS_PUBLIC_URL=https://pds.example.com \
  -p 3000:3000 \
  -v pocketpds-data:/data \
  ghcr.io/alesr/pocketpds:latest
```

## Running for real

For Tailscale, the Cloudflare Tunnel, DNS, and relay onboarding, see
[../build/README.md](../build/README.md).
