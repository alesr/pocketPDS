# PocketPDS

Personal Data Server for the AT Protocol. One Go binary, one SQLite file, no
external services.

Status: experimental. Accounts, records, sync, and the firehose work, and the
data round-trips through indigo's own libraries. Not production-hardened yet.

## Build

    go build ./cmd/pocketpds

## Run

    POCKETPDS_DB=./pocketpds.db ./pocketpds -seed

`-seed` creates a dev account (`dev.example.com` / `password`) if none exists.

## Docs

- [Getting started](docs/getting-started.md): build, run, API examples, config.
- [Self-host runbook](build/README.md): DNS, HTTPS, and relay onboarding.

## Design

Built on `bluesky-social/indigo`. Writes go through its `repo` package, which
builds the Merkle Search Tree and signs commits. Reads and sync go through
`atproto/repo` and `atproto/identity`. Blocks are stored as DAG-CBOR in a
SQLite blockstore, with a denormalized `repo_records` index backing
`getRecord` and `listRecords`.

## Limitations

- `did:web` resolution is on you. The PDS serves the DID doc, but the domain
  has to route `/.well-known/did.json` to it over HTTPS.
- Not implemented: `did:plc` recovery-key custody, `#sync` firehose events,
  `listMissingBlobs`, `listReposByCollection`, `importRepo`.

## License

[MIT](LICENSE)
