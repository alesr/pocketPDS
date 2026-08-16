package plc

import (
	"bytes"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"io"
	"strings"

	"github.com/bluesky-social/indigo/atproto/atcrypto"
	cbor "github.com/ipfs/go-ipld-cbor"
)

// Service is a DID document service entry.
type Service struct {
	Type     string `json:"type"`
	Endpoint string `json:"endpoint"`
}

// Operation is a did:plc operation. The DAG-CBOR encoding (MarshalCBOR) uses
// go-ipld-cbor's RFC-7049 canonical map key ordering, matching the reference
// @ipld/dag-cbor encoder, so signatures and DID derivation interoperate.
type Operation struct {
	Type                string             `json:"type"`
	VerificationMethods map[string]string  `json:"verificationMethods"`
	RotationKeys        []string           `json:"rotationKeys"`
	AlsoKnownAs         []string           `json:"alsoKnownAs"`
	Services            map[string]Service `json:"services"`
	Prev                *string            `json:"prev"`
	Sig                 string             `json:"sig,omitempty"`
}

// NewAtproto creates an unsigned atproto-compatible operation.
func NewAtproto(signingKey, handle, pds string, rotationKeys []string, prev *string) *Operation {
	return &Operation{
		Type:                "plc_operation",
		VerificationMethods: map[string]string{"atproto": signingKey},
		RotationKeys:        rotationKeys,
		AlsoKnownAs:         []string{ensureAtprotoPrefix(handle)},
		Services:            map[string]Service{"atproto_pds": {Type: "AtprotoPersonalDataServer", Endpoint: ensureHTTPPrefix(pds)}},
		Prev:                prev,
	}
}

// Sign sets Sig by ECDSA-SHA256 signing the unsigned DAG-CBOR encoding.
func (o *Operation) Sign(key atcrypto.PrivateKey) error {
	o.Sig = ""
	var buf bytes.Buffer
	if err := o.MarshalCBOR(&buf); err != nil {
		return err
	}
	sig, err := key.HashAndSign(buf.Bytes())
	if err != nil {
		return err
	}
	o.Sig = base64.RawURLEncoding.EncodeToString(sig)
	return nil
}

// DID derives the did:plc identifier from the signed operation.
func (o *Operation) DID() (string, error) {
	var buf bytes.Buffer
	if err := o.MarshalCBOR(&buf); err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	b32 := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:])
	return "did:plc:" + strings.ToLower(b32)[:24], nil
}

func (o *Operation) MarshalCBOR(w io.Writer) error {
	m := map[string]any{
		"type":                o.Type,
		"verificationMethods": map[string]any{"atproto": o.VerificationMethods["atproto"]},
		"rotationKeys":        toStringAny(o.RotationKeys),
		"alsoKnownAs":         toStringAny(o.AlsoKnownAs),
		"prev":                nilOrString(o.Prev),
	}
	if svc, ok := o.Services["atproto_pds"]; ok {
		m["services"] = map[string]any{
			"atproto_pds": map[string]any{"type": svc.Type, "endpoint": svc.Endpoint},
		}
	}
	if o.Sig != "" {
		m["sig"] = o.Sig
	}
	b, err := cbor.DumpObject(m)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

func toStringAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

func nilOrString(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func ensureHTTPPrefix(s string) string {
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return s
	}
	return "https://" + s
}

func ensureAtprotoPrefix(s string) string {
	if strings.HasPrefix(s, "at://") {
		return s
	}
	return "at://" + strings.TrimPrefix(strings.TrimPrefix(s, "http://"), "https://")
}
