#!/usr/bin/env bash
# Verify a self-hosted did:web PDS.
# usage: verify.sh <handle> <pds-url>
set -euo pipefail

HANDLE="${1:?usage: verify.sh <handle> <pds-url>}"
PDS="${2:?usage: verify.sh <handle> <pds-url>}"

echo "== resolveHandle ($HANDLE) =="
curl -s "https://public.api.bsky.app/xrpc/com.atproto.identity.resolveHandle?handle=${HANDLE}"
echo

DID="did:web:${HANDLE}"

echo "== resolveDid ($DID) =="
curl -s "https://public.api.bsky.app/xrpc/com.atproto.identity.resolveDid?did=${DID}"
echo

echo "== did document served by the PDS =="
curl -s "${PDS}/.well-known/did.json"
echo

echo "== getLatestCommit =="
curl -s "${PDS}/xrpc/com.atproto.sync.getLatestCommit?did=${DID}"
echo

echo "== listRepos (first page) =="
curl -s "${PDS}/xrpc/com.atproto.sync.listRepos?limit=10"
echo
