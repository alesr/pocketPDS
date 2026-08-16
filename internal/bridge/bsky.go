package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const bskyPDS = "https://bsky.social"

// client is a minimal ATProto client for bsky.social.
type client struct {
	hc        *http.Client
	accessJWT string
}

func newClient() *client {
	return &client{hc: &http.Client{Timeout: 60 * time.Second}}
}

func (c *client) createSession(ctx context.Context, identifier, password string) (string, error) {
	body, _ := json.Marshal(map[string]string{"identifier": identifier, "password": password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, bskyPDS+"/xrpc/com.atproto.server.createSession", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bsky createSession: %s: %s", resp.Status, truncate(b))
	}
	var out struct {
		AccessJwt string `json:"accessJwt"`
		Did       string `json:"did"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", err
	}
	c.accessJWT = out.AccessJwt
	return out.Did, nil
}

func (c *client) do(ctx context.Context, method, nsid string, body any, query url.Values, out any) error {
	u := bskyPDS + "/xrpc/" + nsid
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.accessJWT != "" {
		req.Header.Set("Authorization", "Bearer "+c.accessJWT)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bsky %s: %s: %s", nsid, resp.Status, truncate(b))
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// createRecord writes a record to the bsky.social repo, returning its URI.
func (c *client) createRecord(ctx context.Context, repo, collection string, record map[string]any) (string, error) {
	var out struct {
		URI string `json:"uri"`
	}
	err := c.do(ctx, http.MethodPost, "com.atproto.repo.createRecord",
		map[string]any{"repo": repo, "collection": collection, "record": record}, nil, &out)
	return out.URI, err
}

// putRecord writes a record at a specific rkey (e.g. "self" for profiles).
func (c *client) putRecord(ctx context.Context, repo, collection, rkey string, record map[string]any) (string, error) {
	var out struct {
		URI string `json:"uri"`
	}
	err := c.do(ctx, http.MethodPost, "com.atproto.repo.putRecord",
		map[string]any{"repo": repo, "collection": collection, "rkey": rkey, "record": record}, nil, &out)
	return out.URI, err
}

func (c *client) uploadBlob(ctx context.Context, mime string, data []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, bskyPDS+"/xrpc/com.atproto.repo.uploadBlob", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mime)
	req.Header.Set("Authorization", "Bearer "+c.accessJWT)
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bsky uploadBlob: %s: %s", resp.Status, truncate(b))
	}
	var out struct {
		Blob struct {
			Ref struct {
				Link string `json:"$link"`
			} `json:"ref"`
		} `json:"blob"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", err
	}
	return out.Blob.Ref.Link, nil
}

// listRecords pages through a collection of a repo on bsky.social.
func (c *client) listRecords(ctx context.Context, repo, collection, cursor string, limit int) ([]remoteRecord, *string, error) {
	q := url.Values{"repo": {repo}, "collection": {collection}, "limit": {fmt.Sprint(limit)}}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	var out struct {
		Records []struct {
			URI   string          `json:"uri"`
			CID   string          `json:"cid"`
			Value json.RawMessage `json:"value"`
		} `json:"records"`
		Cursor *string `json:"cursor"`
	}
	if err := c.do(ctx, http.MethodGet, "com.atproto.repo.listRecords", nil, q, &out); err != nil {
		return nil, nil, err
	}
	recs := make([]remoteRecord, 0, len(out.Records))
	for _, r := range out.Records {
		recs = append(recs, remoteRecord{URI: r.URI, CID: r.CID, Value: r.Value})
	}
	return recs, out.Cursor, nil
}

// getRecord fetches a single record from bsky.social.
func (c *client) getRecord(ctx context.Context, repo, collection, rkey string) (json.RawMessage, string, error) {
	q := url.Values{"repo": {repo}, "collection": {collection}, "rkey": {rkey}}
	var out struct {
		CID   string          `json:"cid"`
		Value json.RawMessage `json:"value"`
	}
	if err := c.do(ctx, http.MethodGet, "com.atproto.repo.getRecord", nil, q, &out); err != nil {
		return nil, "", err
	}
	return out.Value, out.CID, nil
}

// getBlob downloads a blob from bsky.social, returning its bytes and mime type.
func (c *client) getBlob(ctx context.Context, did, cidStr string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bskyPDS+"/xrpc/com.atproto.sync.getBlob", nil)
	if err != nil {
		return nil, "", err
	}
	q := req.URL.Query()
	q.Set("did", did)
	q.Set("cid", cidStr)
	req.URL.RawQuery = q.Encode()
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("bsky getBlob: %s: %s", resp.Status, truncate(b))
	}
	b, err := io.ReadAll(resp.Body)
	return b, resp.Header.Get("Content-Type"), err
}

type remoteRecord struct {
	URI   string
	CID   string
	Value json.RawMessage
}

func truncate(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "..."
	}
	return string(b)
}
