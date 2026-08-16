package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

// Box derives encryption and MAC keys from a secret and encrypts/decrypts
// account private keys at rest.
type Box struct {
	encKey []byte
	macKey []byte
}

func NewBox(secret string) *Box {
	salt := []byte("pocketpds:v1")
	return &Box{
		encKey: argon2.IDKey([]byte(secret), salt, 1, 64*1024, 4, 32),
		macKey: argon2.IDKey([]byte(secret), append(salt, []byte(":mac")...), 1, 64*1024, 4, 32),
	}
}

// HMACKey returns the key used to sign access JWTs.
func (b *Box) HMACKey() []byte { return b.macKey }

// Encrypt seals plaintext with AES-256-GCM and returns a "enc:v1:"-prefixed
// base64 string.
func (b *Box) Encrypt(plain []byte) (string, error) {
	block, err := aes.NewCipher(b.encKey)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, nonce, plain, nil)
	return "enc:v1:" + base64.RawStdEncoding.EncodeToString(nonce) + ":" +
		base64.RawStdEncoding.EncodeToString(ct), nil
}

// Decrypt reverses Encrypt. Returns an error if the input is not a valid
// encrypted blob.
func (b *Box) Decrypt(s string) ([]byte, error) {
	var ok bool
	s, ok = cutPrefix(s, "enc:v1:")
	if !ok {
		return nil, fmt.Errorf("not an encrypted value")
	}
	nonceB64, ctB64, found := splitOnce(s, ":")
	if !found {
		return nil, fmt.Errorf("malformed encrypted value")
	}
	nonce, err := base64.RawStdEncoding.DecodeString(nonceB64)
	if err != nil {
		return nil, err
	}
	ct, err := base64.RawStdEncoding.DecodeString(ctB64)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(b.encKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, err
	}
	return plain, nil
}

// SecureEqual reports whether two byte slices are equal in constant time.
func SecureEqual(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }

func cutPrefix(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return s, false
}

func splitOnce(s, sep string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == sep[0] && i+len(sep) <= len(s) && s[i:i+len(sep)] == sep {
			return s[:i], s[i+len(sep):], true
		}
	}
	return s, "", false
}
