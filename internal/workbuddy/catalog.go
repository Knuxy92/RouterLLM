package workbuddy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// FetchModels pulls the live model catalog from the upstream product-config
// endpoint with the desktop identity (the UA decides which catalog answers —
// unknown ones are a hard 400, code 12403).
func (c *Client) FetchModels(ctx context.Context, realm, uid, accessToken string) ([]string, error) {
	hosts := Hosts(realm)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hosts.ChatBase+ConfigPath, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", DesktopUA(realm))
	req.Header.Set("Origin", hosts.Origin)
	req.Header.Set("Referer", hosts.Origin+"/")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("X-User-Id", uid)

	client := c.httpClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", ConfigPath, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", ConfigPath, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("workbuddy catalog returned %d: %.200s", resp.StatusCode, body)
	}

	return ParseModels(body)
}

// ParseModels extracts the model ids from a /v3/config response. The picker's
// list rides in agents[].models as bare ids — under "data" in the endpoint's
// answer, at the top level in the desktop cache file; an unusable credential
// answers HTTP 200 with an empty list.
func ParseModels(body []byte) ([]string, error) {
	var payload struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode workbuddy catalog: %w", err)
	}

	roots := []json.RawMessage{body, payload.Data}
	var pool [][]string
	for _, root := range roots {
		if len(root) == 0 {
			continue
		}
		var doc struct {
			Agents map[string]struct {
				Models []string `json:"models"`
			} `json:"agents"`
		}
		if json.Unmarshal(root, &doc) != nil {
			continue
		}
		for _, entry := range doc.Agents {
			if len(entry.Models) == 0 {
				continue
			}
			ids := make([]string, 0, len(entry.Models))
			for _, id := range entry.Models {
				if id != "" {
					ids = append(ids, id)
				}
			}
			if len(ids) > 0 {
				pool = append(pool, ids)
			}
		}
	}
	if len(pool) == 0 {
		return nil, nil
	}

	best := pool[0]
	for _, ids := range pool[1:] {
		if len(ids) > len(best) {
			best = ids
		}
	}

	seen := make(map[string]bool, len(best))
	out := make([]string, 0, len(best))
	for _, id := range best {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}

	return out, nil
}

// CatalogClient is a convenience constructor for one-shot catalog pulls.
func CatalogClient(timeout time.Duration) *Client {
	return &Client{HTTPClient: &http.Client{Timeout: timeout}}
}
