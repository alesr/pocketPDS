# PocketPDS

[![CI](https://github.com/alesr/pocketPDS/actions/workflows/ci.yml/badge.svg)](https://github.com/alesr/pocketPDS/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A single-binary Personal Data Server for the AT Protocol. One Go binary, one
SQLite file, no external services.

## What it does

- Accounts with `did:web` or `did:plc` identities and P-256 keys.
- Signed repos (Merkle Search Tree) over `createRecord` / `putRecord` /
  `deleteRecord` / `applyWrites`, with `swapCommit` / `swapRecord` CAS.
- The full `com.atproto.sync` surface: CAR export (full and incremental),
  `getRecord`, `getBlocks`, `listRepos`, `listBlobs`, and the `subscribeRepos`
  firehose.
- A minimal single-user AppView for `app.bsky.*` reads, with proxied network
  reads.
- Blobs, app passwords, invite codes, email, account lifecycle, and a `/admin`
  dashboard with a setup wizard.
- A Bluesky bridge that publishes and archives records against a linked
  bsky.social account.

## Install

Build from source (Go 1.26+):

```bash
go build ./cmd/pocketpds
POCKETPDS_SECRET=<long-random-string> POCKETPDS_DB=./pocketpds.db ./pocketpds -seed
```

Or run the container:

```bash
docker run -d --name pocketpds \
  -e POCKETPDS_SECRET=<long-random-string> \
  -p 3000:3000 \
  -v pocketpds-data:/data \
  ghcr.io/alesr/pocketpds:latest
```

`POCKETPDS_SECRET` is required.

## Docs

- [Getting started](docs/getting-started.md) : build, run, API examples, config.
- [Self-host runbook](build/README.md) : Tailscale, Cloudflare Tunnel, DNS, and
  the setup wizard.

## Design

Built on `bluesky-social/indigo`. Writes go through its `repo` package, which
builds the Merkle Search Tree and signs commits. Reads and sync go through
`atproto/repo` and `atproto/identity`. Blocks are stored as DAG-CBOR in a
SQLite blockstore, with a denormalized `repo_records` index backing
`getRecord` and `listRecords`.

## License

[MIT](LICENSE)
