package plc

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/bluesky-social/indigo/atproto/atcrypto"
)

func TestOperationSignAndDID(t *testing.T) {
	t.Parallel()
	signing, err := atcrypto.GeneratePrivateKeyP256()
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := atcrypto.GeneratePrivateKeyP256()
	if err != nil {
		t.Fatal(err)
	}

	signingPub, _ := signing.PublicKey()
	recoveryPub, _ := recovery.PublicKey()
	op := NewAtproto(signingPub.DIDKey(), "alice.test", "https://pds.example.com", []string{recoveryPub.DIDKey()}, nil)

	if err := op.Sign(recovery); err != nil {
		t.Fatal(err)
	}

	// Signature must verify against the recovery key (SHA-256 + sign).
	unsigned := *op
	unsigned.Sig = ""
	var buf bytes.Buffer
	if err := unsigned.MarshalCBOR(&buf); err != nil {
		t.Fatal(err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(op.Sig)
	if err != nil {
		t.Fatal(err)
	}
	if err := recoveryPub.HashAndVerify(buf.Bytes(), sig); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}

	// DID format: did:plc: + 24 base32 chars, deterministic.
	did1, err := op.DID()
	if err != nil {
		t.Fatal(err)
	}
	did2, err := op.DID()
	if err != nil {
		t.Fatal(err)
	}
	if did1 != did2 {
		t.Fatalf("DID derivation not deterministic: %s vs %s", did1, did2)
	}
	if len(did1) != len("did:plc:")+24 || !strings.HasPrefix(did1, "did:plc:") {
		t.Fatalf("unexpected DID: %q", did1)
	}
}
