package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/agentproto"
)

// Client calls the control plane.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient returns a client for the control plane at baseURL.
func NewClient(baseURL, token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{}
	}

	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, http: httpClient}
}

// requestSlack is added to the long-poll wait to get the request timeout.
const requestSlack = 20 * time.Second

// Sync runs one sync round.
func (c *Client) Sync(ctx context.Context, req agentproto.SyncRequest) (agentproto.SyncResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return agentproto.SyncResponse{}, fmt.Errorf("marshal sync: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(req.WaitSeconds)*time.Second+requestSlack)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+agentproto.SyncPath, bytes.NewReader(body))
	if err != nil {
		return agentproto.SyncResponse{}, fmt.Errorf("build sync request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.token)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return agentproto.SyncResponse{}, fmt.Errorf("sync: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return agentproto.SyncResponse{}, fmt.Errorf("read sync response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var problem struct {
			Detail string `json:"detail"`
		}
		_ = json.Unmarshal(raw, &problem)

		return agentproto.SyncResponse{}, fmt.Errorf("sync: control plane answered %d: %s", resp.StatusCode, problem.Detail)
	}
	var out agentproto.SyncResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return agentproto.SyncResponse{}, fmt.Errorf("decode sync response: %w", err)
	}

	return out, nil
}
