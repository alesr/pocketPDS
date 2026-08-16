package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alesr/pocketPDS/internal/api"
	"github.com/alesr/pocketPDS/internal/blob"
	"github.com/alesr/pocketPDS/internal/config"
	"github.com/alesr/pocketPDS/internal/db"
	"github.com/alesr/pocketPDS/internal/repo"
	"github.com/ipfs/go-cid"
)

const postCollection = "app.bsky.feed.post"
const profileCollection = "app.bsky.actor.profile"

// Service bridges the local PDS with a bsky.social account: publishing local
// records to bsky.social and archiving bsky.social records into the local repo.
type Service struct {
	cfg   *config.Config
	store *db.Store
	mgr   *repo.Manager
	blobs *blob.Store
}

func New(cfg *config.Config, store *db.Store, mgr *repo.Manager, blobs *blob.Store) *Service {
	return &Service{cfg: cfg, store: store, mgr: mgr, blobs: blobs}
}

// Report summarizes a sync run.
type Report struct {
	Published int      `json:"published"`
	Archived  int      `json:"archived"`
	Errors    []string `json:"errors"`
}

// SetConfig stores the bsky.social handle and app password (encrypted).
func (s *Service) SetConfig(ctx context.Context, handle, appPassword string) error {
	if handle = strings.TrimSpace(handle); handle != "" {
		if err := s.SetHandle(ctx, handle); err != nil {
			return err
		}
	}
	if appPassword != "" {
		if err := s.SetPassword(ctx, appPassword); err != nil {
			return err
		}
	}
	if handle == "" && appPassword == "" {
		return fmt.Errorf("nothing to set")
	}
	return nil
}

// SetHandle stores the bsky.social handle.
func (s *Service) SetHandle(ctx context.Context, handle string) error {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return fmt.Errorf("handle is required")
	}
	_, err := s.store.DB.ExecContext(ctx,
		"INSERT INTO bridge_config (key, value) VALUES ('handle', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", handle)
	return err
}

// SetPassword stores the bsky.social app password (encrypted at rest).
func (s *Service) SetPassword(ctx context.Context, appPassword string) error {
	if appPassword == "" {
		return fmt.Errorf("app password is required")
	}
	enc, err := s.store.Box.Encrypt([]byte(appPassword))
	if err != nil {
		return err
	}
	_, err = s.store.DB.ExecContext(ctx,
		"INSERT INTO bridge_config (key, value) VALUES ('app_password', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", enc)
	return err
}

// Config returns the configured handle and whether an app password is set.
func (s *Service) Config(ctx context.Context) (handle string, passwordSet bool, err error) {
	var v string
	if err := s.store.DB.QueryRowContext(ctx, "SELECT value FROM bridge_config WHERE key = 'handle'").Scan(&v); err == nil {
		handle = v
	}
	var enc string
	if err := s.store.DB.QueryRowContext(ctx, "SELECT value FROM bridge_config WHERE key = 'app_password'").Scan(&enc); err == nil && enc != "" {
		passwordSet = true
	}
	return handle, passwordSet, nil
}

func (s *Service) password(ctx context.Context) (string, error) {
	var enc string
	if err := s.store.DB.QueryRowContext(ctx, "SELECT value FROM bridge_config WHERE key = 'app_password'").Scan(&enc); err != nil {
		return "", fmt.Errorf("bridge not configured")
	}
	b, err := s.store.Box.Decrypt(enc)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Sync runs both directions: publish local records to bsky.social, then archive
// bsky.social records into the local repo.
func (s *Service) Sync(ctx context.Context) (Report, error) {
	rep := Report{}
	localDID, err := s.localDID(ctx)
	if err != nil {
		return rep, fmt.Errorf("no local account: %w", err)
	}
	handle, passwordSet, err := s.Config(ctx)
	if err != nil || handle == "" || !passwordSet {
		return rep, fmt.Errorf("bridge not configured (set handle + app password)")
	}
	password, err := s.password(ctx)
	if err != nil {
		return rep, err
	}

	c := newClient()
	if _, err := c.createSession(ctx, handle, password); err != nil {
		return rep, fmt.Errorf("bsky.social login failed: %w", err)
	}
	remoteDID, err := resolveHandle(ctx, handle)
	if err != nil {
		return rep, fmt.Errorf("resolve bsky handle: %w", err)
	}

	rep.Published, rep.Errors = s.publish(ctx, localDID, remoteDID, c)
	archived, archErrs := s.archive(ctx, localDID, remoteDID, c)
	rep.Archived = archived
	rep.Errors = append(rep.Errors, archErrs...)
	return rep, nil
}

func (s *Service) localDID(ctx context.Context) (string, error) {
	var did string
	err := s.store.DB.QueryRowContext(ctx, "SELECT did FROM accounts ORDER BY created_at LIMIT 1").Scan(&did)
	return did, err
}

func resolveHandle(ctx context.Context, handle string) (string, error) {
	u := "https://public.api.bsky.app/xrpc/com.atproto.identity.resolveHandle?handle=" + url.QueryEscape(handle)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	var out struct {
		Did string `json:"did"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", err
	}
	if out.Did == "" {
		return "", fmt.Errorf("no DID for handle %q", handle)
	}
	return out.Did, nil
}

type localItem struct {
	collection, rkey, cidStr string
	value                    []byte
}

func (s *Service) publish(ctx context.Context, localDID, remoteDID string, c *client) (int, []string) {
	count := 0
	var errs []string

	items, _, err := s.mgr.ListRecords(ctx, localDID, postCollection, "", 100000)
	if err != nil {
		return 0, []string{fmt.Sprintf("list local posts: %v", err)}
	}
	all := make([]localItem, 0, len(items)+1)
	for _, it := range items {
		all = append(all, localItem{collection: postCollection, rkey: it.RKey, cidStr: it.CID, value: it.Value})
	}
	if pcid, pval, err := s.mgr.GetRecord(ctx, localDID, profileCollection, "self"); err == nil && pcid != "" {
		all = append(all, localItem{collection: profileCollection, rkey: "self", cidStr: pcid, value: pval})
	}

	for _, it := range all {
		if s.synced(ctx, "publish", it.cidStr) {
			continue
		}
		rec, err := parseRecord(it.value)
		if err != nil {
			errs = append(errs, fmt.Sprintf("parse %s/%s: %v", it.collection, it.rkey, err))
			continue
		}
		rewriteURIs(rec, localDID, remoteDID)
		if err := s.publishBlobs(ctx, rec, localDID, c); err != nil {
			errs = append(errs, fmt.Sprintf("blobs %s/%s: %v", it.collection, it.rkey, err))
			continue
		}
		uri, err := c.createRecord(ctx, remoteDID, it.collection, rec)
		if it.collection == profileCollection {
			uri, err = c.putRecord(ctx, remoteDID, it.collection, "self", rec)
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("publish %s/%s: %v", it.collection, it.rkey, err))
			continue
		}
		s.markSynced(ctx, "publish", it.cidStr, "at://"+localDID+"/"+it.collection+"/"+it.rkey, uri)
		count++
	}
	return count, errs
}

func (s *Service) archive(ctx context.Context, localDID, remoteDID string, c *client) (int, []string) {
	count := 0
	var errs []string

	cursor := ""
	for {
		recs, next, err := c.listRecords(ctx, remoteDID, postCollection, cursor, 100)
		if err != nil {
			errs = append(errs, fmt.Sprintf("list bsky posts: %v", err))
			break
		}
		for _, r := range recs {
			if s.synced(ctx, "archive", r.CID) {
				continue
			}
			rec, err := parseRecord(r.Value)
			if err != nil {
				continue
			}
			rewriteURIs(rec, remoteDID, localDID)
			if err := s.archiveBlobs(ctx, rec, remoteDID, localDID, c); err != nil {
				errs = append(errs, fmt.Sprintf("blobs %s: %v", r.URI, err))
				continue
			}
			recordJSON, err := json.Marshal(rec)
			if err != nil {
				continue
			}
			marsh, err := api.RecordMarshaler(recordJSON)
			if err != nil {
				errs = append(errs, fmt.Sprintf("marshal %s: %v", r.URI, err))
				continue
			}
			rkey := strings.TrimPrefix(r.URI, "at://"+remoteDID+"/"+postCollection+"/")
			if _, err := s.mgr.PutRecord(ctx, localDID, postCollection, rkey, marsh, recordJSON); err != nil {
				errs = append(errs, fmt.Sprintf("archive %s: %v", r.URI, err))
				continue
			}
			s.markSynced(ctx, "archive", r.CID, "at://"+localDID+"/"+postCollection+"/"+rkey, r.URI)
			count++
		}
		if next == nil {
			break
		}
		cursor = *next
	}

	if pval, pcid, err := c.getRecord(ctx, remoteDID, profileCollection, "self"); err == nil && pcid != "" && !s.synced(ctx, "archive", pcid) {
		if rec, err := parseRecord(pval); err == nil {
			rewriteURIs(rec, remoteDID, localDID)
			if s.archiveBlobs(ctx, rec, remoteDID, localDID, c) == nil {
				if recordJSON, err := json.Marshal(rec); err == nil {
					if marsh, err := api.RecordMarshaler(recordJSON); err == nil {
						if _, err := s.mgr.PutRecord(ctx, localDID, profileCollection, "self", marsh, recordJSON); err == nil {
							s.markSynced(ctx, "archive", pcid, "at://"+localDID+"/"+profileCollection+"/self", "at://"+remoteDID+"/"+profileCollection+"/self")
							count++
						}
					}
				}
			}
		}
	}

	return count, errs
}

func (s *Service) publishBlobs(ctx context.Context, rec map[string]any, localDID string, c *client) error {
	return walkBlobs(rec, func(blob map[string]any) error {
		link := blobLink(blob)
		if link == "" {
			return nil
		}
		cv, err := cid.Decode(link)
		if err != nil {
			return err
		}
		rc, mime, _, err := s.blobs.Open(ctx, localDID, cv)
		if err != nil {
			return fmt.Errorf("blob %s not in local store: %w", link, err)
		}
		defer func() { _ = rc.Close() }()
		data, err := io.ReadAll(rc)
		if err != nil {
			return err
		}
		newLink, err := c.uploadBlob(ctx, mime, data)
		if err != nil {
			return err
		}
		if newLink != "" && newLink != link {
			setBlobLink(blob, newLink)
		}
		return nil
	})
}

func (s *Service) archiveBlobs(ctx context.Context, rec map[string]any, remoteDID, localDID string, c *client) error {
	return walkBlobs(rec, func(blob map[string]any) error {
		link := blobLink(blob)
		if link == "" {
			return nil
		}
		data, mime, err := c.getBlob(ctx, remoteDID, link)
		if err != nil {
			return err
		}
		if mime == "" {
			mime = "application/octet-stream"
		}
		_, _, err = s.blobs.Put(ctx, localDID, mime, bytes.NewReader(data))
		return err
	})
}

func (s *Service) synced(ctx context.Context, direction, sourceCID string) bool {
	var one int
	return s.store.DB.QueryRowContext(ctx,
		"SELECT 1 FROM bridge_sync WHERE direction = ? AND source_cid = ?", direction, sourceCID).Scan(&one) == nil
}

func (s *Service) markSynced(ctx context.Context, direction, sourceCID, localURI, remoteURI string) {
	_, _ = s.store.DB.ExecContext(ctx,
		"INSERT INTO bridge_sync (direction, source_cid, local_uri, remote_uri, synced_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT(direction, source_cid) DO NOTHING",
		direction, sourceCID, localURI, remoteURI, time.Now().Format(time.RFC3339))
}
