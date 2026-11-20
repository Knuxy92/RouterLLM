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

// ParseModels extracts the model ids from a /v3/config response. The live
// schema carries agents as an array of {name, models:[id]} — the chat picker's
// routable list — plus a broader data.models array of {id, …} objects that also
// includes image/video models. Older cache fixtures use agents as a name→{models}
// map. Agent ids are preferred; data.models is only a fallback. An unusable
// credential answers HTTP 200 with an empty list.
func ParseModels(body []byte) ([]string, error) {
	var payload struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode workbuddy catalog: %w", err)
	}

	roots := []json.RawMessage{body, payload.Data}
	var agentPools, modelPools [][]string
	for _, root := range roots {
		if len(root) == 0 {
			continue
		}
		var doc struct {
			Agents json.RawMessage `json:"agents"`
			Models json.RawMessage `json:"models"`
		}
		if json.Unmarshal(root, &doc) != nil {
			continue
		}
		if ids := parseAgentModels(doc.Agents); len(ids) > 0 {
			agentPools = append(agentPools, ids)
		}
		if ids := parseModelIDs(doc.Models); len(ids) > 0 {
			modelPools = append(modelPools, ids)
		}
	}

	pools := agentPools
	if len(pools) == 0 {
		pools = modelPools
	}
	if len(pools) == 0 {
		return nil, nil
	}

	best := pools[0]
	for _, ids := range pools[1:] {
		if len(ids) > len(best) {
			best = ids
		}
	}

	seen := make(map[string]bool, len(best))
	out := make([]string, 0, len(best))
	for _, id := range best {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}

	return out, nil
}

// parseAgentModels reads agents[].models in the live array shape and the older
// name→{models} map shape, returning the union of every agent's model ids.
func parseAgentModels(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}

	var ids []string
	var list []struct {
		Models []string `json:"models"`
	}
	if json.Unmarshal(raw, &list) == nil {
		for _, entry := range list {
			ids = append(ids, entry.Models...)
		}
		if len(ids) > 0 {
			return ids
		}
	}

	var byName map[string]struct {
		Models []string `json:"models"`
	}
	if json.Unmarshal(raw, &byName) == nil {
		for _, entry := range byName {
			ids = append(ids, entry.Models...)
		}
	}

	return ids
}

func parseModelIDs(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}

	var models []struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &models) != nil {
		return nil
	}

	ids := make([]string, 0, len(models))
	for _, model := range models {
		if model.ID != "" {
			ids = append(ids, model.ID)
		}
	}

	return ids
}

// CatalogClient returns a Client with the given request timeout.
func CatalogClient(timeout time.Duration) *Client {
	return &Client{HTTPClient: &http.Client{Timeout: timeout}}
}
