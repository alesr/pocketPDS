# Self-hosting PocketPDS with did:web

Runbook for a single self-hosted PDS at `pds.alesr.me` with a `did:web`
identity. No plc.directory, no bsky.social.

## Prerequisites

- A server with a public IP (a $5 VPS or a Raspberry Pi).
- A domain you control (`alesr.me` in this example).
- HTTPS in front of the PDS (Caddy or nginx with Let's Encrypt).

## 1. DNS

Point the PDS subdomain at your server:

```
pds.alesr.me.   A    <server-ip>
```

For handle resolution you need one of:

- a DNS TXT record: `_atproto.pds.alesr.me.  TXT  "did=did:web:pds.alesr.me"`, or
- nothing. PocketPDS serves `/.well-known/atproto-did` itself (preferred).

## 2. Run PocketPDS

Docker Compose:

```yaml
services:
  pocketpds:
    image: ghcr.io/alesr/pocketpds:latest
    restart: unless-stopped
    environment:
      POCKETPDS_LISTEN: 127.0.0.1:3000
      POCKETPDS_DB: /data/pocketpds.db
      POCKETPDS_DATA_DIR: /data
      POCKETPDS_PUBLIC_URL: https://pds.alesr.me
      POCKETPDS_SECRET: <long-random-string>
      POCKETPDS_DID_METHOD: web
      POCKETPDS_ADMIN_TOKEN: <admin-token>
    volumes:
      - ./data:/data
```

Or build from source and run with systemd (see `pocketpds.service`):

```bash
go build -o /usr/local/bin/pocketpds ./cmd/pocketpds
```

## 3. Reverse proxy (Caddy)

```
pds.alesr.me {
    reverse_proxy 127.0.0.1:3000
}
```

Caddy handles TLS. The PDS must be reachable over HTTPS at `pds.alesr.me`.

## 4. Create the account

```bash
curl -s -X POST https://pds.alesr.me/xrpc/com.atproto.server.createAccount \
  -H 'Content-Type: application/json' \
  -d '{"handle":"pds.alesr.me","password":"<password>","email":"you@example.com"}'
```

Returns a `did:web:pds.alesr.me` DID. The DID document is served at
`https://pds.alesr.me/.well-known/did.json`.

## 5. Verify

```bash
./verify.sh pds.alesr.me https://pds.alesr.me
```

## 6. Relay

For posts to appear on bsky.app, a relay has to crawl your PDS. The protocol
mechanism (`com.atproto.sync.requestCrawl`) is implemented. The remaining step
is onboarding with a relay. bsky.network's policy on self-hosted PDSes has
varied over time, so check its current guidance. Alternative relays may accept
self-hosted PDSes directly.

## Caveats

- `did:web` has no recovery key. Your identity is the domain. Keep the domain
  and `POCKETPDS_SECRET` safe (the secret encrypts account keys at rest).
- Back up `pocketpds.db` and the blob directory (`POCKETPDS_DATA_DIR`).
