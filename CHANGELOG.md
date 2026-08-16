# Changelog

## Unreleased

Initial release.

- Identity: `did:web` and `did:plc` account creation, `updateHandle` (PLC).
- Auth: `createSession`/`refreshSession`/`deleteSession`, app passwords, argon2id
  passwords, JWT access tokens, encrypted keys at rest, rate limiting.
- Repository: `createRecord`/`putRecord`/`deleteRecord`/`applyWrites`, `getRecord`,
  `listRecords`, `describeRepo`, `swapCommit`/`swapRecord` compare-and-swap.
- Sync: `getRepo` (full + incremental), `getCheckout`, `getRecord`, `getBlocks`,
  `getLatestCommit`, `listRepos`, `listBlobs`, `getHostStatus`, `getRepoStatus`.
- Firehose: `subscribeRepos` (`#commit`, `#identity`, `#account` events).
- Blobs: `uploadBlob`, `sync.getBlob`.
- Account lifecycle: deactivate/activate/delete, `checkAccountStatus`.
- Signups: invite codes, email verification and password reset (SMTP).
- Admin: minimal web dashboard at `/admin`.
- Packaging: Docker, docker-compose, systemd unit.
