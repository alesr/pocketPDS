package bridge

import (
	"encoding/json"
	"strings"
)

// parseRecord decodes a raw record JSON value into a mutable map.
func parseRecord(value []byte) (map[string]any, error) {
	var rec map[string]any
	if err := json.Unmarshal(value, &rec); err != nil {
		return nil, err
	}
	if rec == nil {
		rec = map[string]any{}
	}
	return rec, nil
}

// rewriteURIs replaces the DID in every at:// URI within a record, so that
// reply/quote references point at the correct account on the destination.
func rewriteURIs(node any, fromDID, toDID string) {
	from := "at://" + fromDID + "/"
	to := "at://" + toDID + "/"
	rewriteURIsPrefix(node, from, to)
}

func rewriteURIsPrefix(node any, from, to string) {
	switch v := node.(type) {
	case map[string]any:
		for k, val := range v {
			if s, ok := val.(string); ok && strings.HasPrefix(s, from) {
				v[k] = strings.Replace(s, from, to, 1)
				continue
			}
			rewriteURIsPrefix(val, from, to)
		}
	case []any:
		for _, val := range v {
			rewriteURIsPrefix(val, from, to)
		}
	}
}

// walkBlobs visits every blob reference ({$type:"blob"}) in a record, calling
// fn with the blob object so its $link may be read or replaced.
func walkBlobs(node any, fn func(blob map[string]any) error) error {
	switch v := node.(type) {
	case map[string]any:
		if t, _ := v["$type"].(string); t == "blob" {
			return fn(v)
		}
		for _, val := range v {
			if err := walkBlobs(val, fn); err != nil {
				return err
			}
		}
	case []any:
		for _, val := range v {
			if err := walkBlobs(val, fn); err != nil {
				return err
			}
		}
	}
	return nil
}

// blobLink returns the CID of a blob reference, or "" if absent.
func blobLink(blob map[string]any) string {
	ref, _ := blob["ref"].(map[string]any)
	link, _ := ref["$link"].(string)
	return link
}

func setBlobLink(blob map[string]any, link string) {
	if ref, ok := blob["ref"].(map[string]any); ok {
		ref["$link"] = link
	}
}
