# Self-hosting PocketPDS

Runbook for a single self-hosted PDS. This is the setup behind `pds.alesr.me`:
a Raspberry Pi on Tailscale, exposed over a Cloudflare Tunnel, managed with
systemd and the admin wizard.

## Prerequisites

- A box to run it on (a Raspberry Pi or any small VPS).
- A domain you control (`alesr.me` here).
- A Cloudflare account with that domain on it (for the tunnel and DNS).

## Access

Tailscale is for operator access only. The box joins the tailnet and you SSH to
its tailnet IP (`100.x.x.x`). It is not part of the public path; the PDS is
reached over the Cloudflare Tunnel, so there is no port forwarding and no
public IP on the box.

## Run it

From source, with the systemd unit:

```bash
go build -o /usr/local/bin/pocketpds ./cmd/pocketpds
sudo cp build/pocketpds.service /etc/systemd/system/pocketpds.service
sudo systemctl enable --now pocketpds
```

Edit `build/pocketpds.service` first to set `POCKETPDS_SECRET`,
`POCKETPDS_PUBLIC_URL`, and `POCKETPDS_ADMIN_TOKEN`.

Or run the Docker image:

```yaml
services:
  pocketpds:
    image: ghcr.io/alesr/pocketpds:latest
    restart: unless-stopped
    ports:
      - "127.0.0.1:3000:3000"
    environment:
      POCKETPDS_PUBLIC_URL: https://pds.alesr.me
      POCKETPDS_SECRET: <long-random-string>
      POCKETPDS_DID_METHOD: web
      POCKETPDS_ADMIN_TOKEN: <admin-token>
    volumes:
      - ./data:/data
```

## Public HTTPS with Cloudflare Tunnel

No public IP or port forwarding. Install the tunnel client (cloudflared) on the
box:

```bash
sudo pocketpds tunnel install
```

Then open the admin wizard at `http://<tailnet-ip>:3000/admin`, sign in with
`POCKETPDS_ADMIN_TOKEN`, and walk through it:

1. **Domain**: the hostname you want, e.g. `pds.alesr.me`.
2. **DNS**: paste a Cloudflare API token. The wizard provisions the Cloudflare
   Tunnel (a CNAME to `<id>.cfargotunnel.com`) and the `_atproto` TXT record
   for handle resolution. HTTPS is handled by Cloudflare.
3. **Restart**: the settings are persisted; restart the service.
4. **Account**: create the first account (handle + password).
5. **Verify**: confirm the DID document and handle resolve.

The wizard also has a **Bluesky bridge** section to link a bsky.social account
and set a custom domain handle (which adds its own `_atproto` TXT record via
the same Cloudflare token).

## Create the account

If you skip the wizard, create the account directly:

```bash
curl -s -X POST https://pds.alesr.me/xrpc/com.atproto.server.createAccount \
  -H 'Content-Type: application/json' \
  -d '{"handle":"pds.alesr.me","password":"<password>","email":"you@example.com"}'
```

Returns a `did:web:pds.alesr.me` DID. The DID document is served at
`https://pds.alesr.me/.well-known/did.json`.

## Verify

```bash
./verify.sh pds.alesr.me https://pds.alesr.me
```

## Relay

For posts to appear on bsky.app, a relay has to crawl your PDS. The protocol
mechanism (`com.atproto.sync.requestCrawl`) is implemented. The remaining step
is onboarding with a relay. bsky.network's policy on self-hosted PDSes has
varied over time, so check its current guidance. Alternative relays may accept
self-hosted PDSes directly.

## Backups

- Back up `pocketpds.db` and the blob directory (`POCKETPDS_DATA_DIR`).
- Keep the domain, `POCKETPDS_SECRET`, and the `did:plc` recovery key safe
  (`pocketpds accounts recover <handle>`). `did:web` has no recovery key; your
  identity is the domain.
