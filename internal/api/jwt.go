package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const accessTokenTTL = 2 * time.Hour

type claims struct {
	Sub string `json:"sub"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
}

// mintAccessJWT creates a short-lived HS256 access token carrying the DID.
func mintAccessJWT(key []byte, did string) (string, error) {
	now := time.Now()
	c := claims{Sub: did, Iat: now.Unix(), Exp: now.Add(accessTokenTTL).Unix()}
	body, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString(body)
	signingInput := header + "." + payload

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig, nil
}

// parseAccessJWT verifies an HS256 access token and returns the DID.
func parseAccessJWT(key []byte, token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("malformed token")
	}
	signingInput := parts[0] + "." + parts[1]

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(signingInput))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", fmt.Errorf("invalid signature")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	var c claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return "", err
	}
	if time.Now().Unix() > c.Exp {
		return "", fmt.Errorf("token expired")
	}
	if c.Sub == "" {
		return "", fmt.Errorf("missing subject")
	}
	return c.Sub, nil
}
