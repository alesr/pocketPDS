package identity

import (
	"fmt"

	"github.com/bluesky-social/indigo/atproto/atcrypto"
)

type Keys struct {
	SigningKey  atcrypto.PrivateKeyExportable
	RecoveryKey atcrypto.PrivateKeyExportable
	Did         string
	DidDoc      map[string]any
}

// CreateDidWeb generates keys and a did:web document for the given handle.
// The resulting DID document must be served at https://<handle>/.well-known/did.json
// (the self-hoster's responsibility) for resolution to work.
func CreateDidWeb(handle, serviceEndpoint string) (*Keys, error) {
	signing, err := atcrypto.GeneratePrivateKeyP256()
	if err != nil {
		return nil, fmt.Errorf("generate signing key: %w", err)
	}
	recovery, err := atcrypto.GeneratePrivateKeyP256()
	if err != nil {
		return nil, fmt.Errorf("generate recovery key: %w", err)
	}
	pub, err := signing.PublicKey()
	if err != nil {
		return nil, fmt.Errorf("derive public key: %w", err)
	}

	did := "did:web:" + handle
	doc := map[string]any{
		"@context":    []string{"https://www.w3.org/ns/did/v1"},
		"id":          did,
		"alsoKnownAs": []string{"at://" + handle},
		"verificationMethod": []map[string]any{{
			"id":                 did + "#atproto",
			"type":               "Multikey",
			"controller":         did,
			"publicKeyMultibase": pub.Multibase(),
		}},
		"service": []map[string]any{{
			"id":              "#atproto_pds",
			"type":            "AtprotoPersonalDataServer",
			"serviceEndpoint": serviceEndpoint,
		}},
	}

	return &Keys{
		SigningKey:  signing,
		RecoveryKey: recovery,
		Did:         did,
		DidDoc:      doc,
	}, nil
}
