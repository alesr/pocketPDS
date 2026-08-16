package plc

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/bluesky-social/indigo/atproto/atcrypto"
	"github.com/stretchr/testify/require"
)

func TestOperationSignAndDID(t *testing.T) {
	t.Parallel()
	signing, err := atcrypto.GeneratePrivateKeyP256()
	require.NoError(t, err)
	recovery, err := atcrypto.GeneratePrivateKeyP256()
	require.NoError(t, err)

	signingPub, _ := signing.PublicKey()
	recoveryPub, _ := recovery.PublicKey()
	op := NewAtproto(signingPub.DIDKey(), "alice.test", "https://pds.example.com", []string{recoveryPub.DIDKey()}, nil)

	require.NoError(t, op.Sign(recovery))

	// Signature must verify against the recovery key (SHA-256 + sign).
	unsigned := *op
	unsigned.Sig = ""
	var buf bytes.Buffer
	require.NoError(t, unsigned.MarshalCBOR(&buf))
	sig, err := base64.RawURLEncoding.DecodeString(op.Sig)
	require.NoError(t, err)
	require.NoError(t, recoveryPub.HashAndVerify(buf.Bytes(), sig), "signature does not verify")

	// DID format: did:plc: + 24 base32 chars, deterministic.
	did1, err := op.DID()
	require.NoError(t, err)
	did2, err := op.DID()
	require.NoError(t, err)
	require.Equal(t, did1, did2, "DID derivation not deterministic")
	require.Len(t, did1, len("did:plc:")+24)
	require.True(t, strings.HasPrefix(did1, "did:plc:"), "unexpected DID: %q", did1)
}
