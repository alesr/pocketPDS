# Changelog

## Unreleased

- Identity: `did:web` and `did:plc` account creation, `updateHandle` (PLC).
- Auth: `createSession`/`refreshSession`/`deleteSession`, app passwords, argon2id
  passwords, JWT access tokens, encrypted keys at rest, rate limiting.
- Repository: `createRecord`/`putRecord`/`deleteRecord`/`applyWrites`, `getRecord`,
  `listRecords`, `describeRepo`, `swapCommit`/`swapRecord` compare-and-swap.
- Sync: `getRepo` (full + incremental), `getCheckout`, `getRecord`, `getBlocks`,
  `getLatestCommit`, `listRepos`, `listBlobs`, `getHostStatus`, `getRepoStatus`.
- Firehose: `subscribeRepos` (`#commit`, `#identity`, `#account` events).
- AppView: single-user `app.bsky.*` reads (profile, feed, thread, timeline, graph)
  with viewer state and proxied network reads.
- Bluesky bridge: publish and archive records to a linked bsky.social account.
- Blobs: `uploadBlob`, `sync.getBlob`.
- Account lifecycle: deactivate/activate/delete, `checkAccountStatus`.
- Signups: invite codes, email verification and password reset (SMTP).
- Admin: web dashboard at `/admin`.
- Packaging: Docker, docker-compose, systemd unit.
