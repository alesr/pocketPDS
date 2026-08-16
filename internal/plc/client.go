package plc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/bluesky-social/indigo/atproto/atcrypto"
)

// Client talks to a PLC directory (default https://plc.directory).
type Client struct {
	url    string
	client *http.Client
}

func NewClient(url string) *Client {
	if url == "" {
		url = "https://plc.directory"
	}
	return &Client{url: url, client: &http.Client{Timeout: 30 * time.Second}}
}

// Create generates a new did:plc identity: builds and signs a genesis op with
// the recovery key, submits it, and returns the derived DID and signed op.
func (c *Client) Create(ctx context.Context, signingKey, recoveryKey atcrypto.PrivateKey, handle, pds string) (string, *Operation, error) {
	signingPub, err := signingKey.PublicKey()
	if err != nil {
		return "", nil, err
	}
	recoveryPub, err := recoveryKey.PublicKey()
	if err != nil {
		return "", nil, err
	}

	op := NewAtproto(signingPub.DIDKey(), handle, pds, []string{recoveryPub.DIDKey()}, nil)
	if err := op.Sign(recoveryKey); err != nil {
		return "", nil, fmt.Errorf("sign genesis op: %w", err)
	}
	did, err := op.DID()
	if err != nil {
		return "", nil, err
	}
	if err := c.Submit(ctx, did, op); err != nil {
		return "", nil, err
	}
	return did, op, nil
}

// Submit posts a signed operation to the directory.
func (c *Client) Submit(ctx context.Context, did string, op *Operation) error {
	body, err := json.Marshal(op)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/"+did, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("plc directory rejected operation: %s: %s", resp.Status, string(b))
	}
	return nil
}

// ResolveDidDoc fetches the rendered DID document for a DID.
func (c *Client) ResolveDidDoc(ctx context.Context, did string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"/"+did, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("plc resolve: %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// LatestCID returns the most recent operation CID for a DID, to use as prev.
func (c *Client) LatestCID(ctx context.Context, did string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"/"+did+"/log/audit", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("plc audit log: %s", resp.Status)
	}
	var log []struct {
		CID string `json:"cid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&log); err != nil {
		return "", err
	}
	if len(log) == 0 {
		return "", fmt.Errorf("empty audit log")
	}
	return log[len(log)-1].CID, nil
}
