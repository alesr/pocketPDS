# PocketPDS

Personal Data Server for the AT Protocol. One Go binary, one SQLite file, no
external services.

## Build

    go build ./cmd/pocketpds

## Run

    POCKETPDS_SECRET=<long-random-string> POCKETPDS_DB=./pocketpds.db ./pocketpds -seed

`POCKETPDS_SECRET` is required. It encrypts account keys at rest and signs
access tokens. `-seed` creates a dev account (`dev.example.com` / `password`)
if none exists.

## Docs

- [Getting started](docs/getting-started.md): build, run, API examples, config.
- [Self-host runbook](build/README.md): DNS, HTTPS, and relay onboarding.

## Design

Built on `bluesky-social/indigo`. Writes go through its `repo` package, which
builds the Merkle Search Tree and signs commits. Reads and sync go through
`atproto/repo` and `atproto/identity`. Blocks are stored as DAG-CBOR in a
SQLite blockstore, with a denormalized `repo_records` index backing
`getRecord` and `listRecords`.

## License

[MIT](LICENSE)
