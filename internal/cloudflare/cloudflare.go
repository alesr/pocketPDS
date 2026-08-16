package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const apiBase = "https://api.cloudflare.com/client/v4"

// Client is a minimal Cloudflare DNS client using only the stdlib.
type Client struct {
	token string
	http  *http.Client
}

func New(token string) *Client {
	return &Client{token: token, http: &http.Client{Timeout: 15 * time.Second}}
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type apiResponse struct {
	Success bool            `json:"success"`
	Errors  []apiError      `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type zone struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Account struct {
		ID string `json:"id"`
	} `json:"account"`
}

type dnsRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	var env apiResponse
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("cloudflare: unexpected response: %s", string(data))
	}
	if !env.Success {
		if len(env.Errors) > 0 {
			return fmt.Errorf("cloudflare: %s (code %d)", env.Errors[0].Message, env.Errors[0].Code)
		}
		return fmt.Errorf("cloudflare: request failed")
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("cloudflare: decode result: %w", err)
		}
	}
	return nil
}

// ZoneID resolves a zone name (e.g. "example.com") to its zone ID.
func (c *Client) ZoneID(ctx context.Context, name string) (string, error) {
	info, err := c.ZoneInfo(ctx, name)
	if err != nil {
		return "", err
	}
	return info.ID, nil
}

// ZoneInfo resolves a zone name to its zone ID and owning account ID.
func (c *Client) ZoneInfo(ctx context.Context, name string) (ZoneInfo, error) {
	var zones []zone
	if err := c.do(ctx, http.MethodGet, "/zones?name="+name, nil, &zones); err != nil {
		return ZoneInfo{}, err
	}
	for _, z := range zones {
		if z.Name == name {
			return ZoneInfo{ID: z.ID, AccountID: z.Account.ID}, nil
		}
	}
	return ZoneInfo{}, fmt.Errorf("cloudflare: zone %q not found", name)
}

// ZoneInfo identifies a zone and the account that owns it.
type ZoneInfo struct {
	ID        string
	AccountID string
}

// Tunnel is a Cloudflare Tunnel (Argo) as returned by the API.
type Tunnel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// CredentialsFile is populated when the tunnel is created.
	CredentialsFile TunnelCredentials `json:"credentials_file"`
}

// TunnelCredentials is the local credential material cloudflared needs to
// authenticate a locally-managed tunnel.
type TunnelCredentials struct {
	AccountTag   string `json:"AccountTag"`
	TunnelID     string `json:"TunnelID"`
	TunnelName   string `json:"TunnelName"`
	TunnelSecret string `json:"TunnelSecret"`
}

// CreateTunnel provisions a new locally-managed Cloudflare Tunnel in the given
// account. The returned credentials are written to disk so cloudflared can run.
func (c *Client) CreateTunnel(ctx context.Context, accountID, name string) (Tunnel, error) {
	var t Tunnel
	err := c.do(ctx, http.MethodPost, "/accounts/"+accountID+"/cfd_tunnel",
		map[string]any{"name": name, "config_src": "local"}, &t)
	if err != nil {
		return Tunnel{}, err
	}
	return t, nil
}

// UpsertDNS creates or updates a DNS record of the given type and name.
func (c *Client) UpsertDNS(ctx context.Context, zoneID, typ, name, content string, proxied bool) error {
	var existing []dnsRecord
	path := fmt.Sprintf("/zones/%s/dns_records?type=%s&name=%s", zoneID, typ, name)
	if err := c.do(ctx, http.MethodGet, path, nil, &existing); err != nil {
		return err
	}

	rec := map[string]any{
		"type":    typ,
		"name":    name,
		"content": content,
		"ttl":     1,
		"proxied": proxied,
	}

	if len(existing) > 0 {
		id := existing[0].ID
		return c.do(ctx, http.MethodPut, "/zones/"+zoneID+"/dns_records/"+id, rec, nil)
	}
	return c.do(ctx, http.MethodPost, "/zones/"+zoneID+"/dns_records", rec, nil)
}

// DetectPublicIP returns the server's public IPv4 address using Cloudflare's
// trace endpoint, falling back to ipify.
func DetectPublicIP(ctx context.Context) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	for _, url := range []string{"https://1.1.1.1/cdn-cgi/trace", "https://api.ipify.org"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			continue
		}
		res, err := client.Do(req)
		if err != nil {
			continue
		}
		data, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()

		body := string(data)
		if strings.Contains(url, "cdn-cgi/trace") {
			for _, line := range strings.Split(body, "\n") {
				if strings.HasPrefix(line, "ip=") {
					return strings.TrimPrefix(line, "ip="), nil
				}
			}
			continue
		}
		if ip := strings.TrimSpace(body); ip != "" {
			return ip, nil
		}
	}
	return "", fmt.Errorf("could not determine public IP")
}
