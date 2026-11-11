// SPDX-License-Identifier: AGPL-3.0-or-later
package cloud

import (
	"context"
	"net/http"
)

// ResetAccess drops the cached access token, so the next call refreshes it
// (after the hosted model answered 401).
func (c *Client) ResetAccess() {
	c.mu.Lock()
	c.access = ""
	c.mu.Unlock()
}

// SignedIn reports whether a SiteMoment sign-in is stored on this computer.
// It does not call the server; a revoked sign-in shows up on the next call.
func (c *Client) SignedIn() bool {
	_, err := c.Secrets.Get(refreshKey)
	return err == nil
}

// LLMURL is the hosted model's OpenAI-compatible chat completions endpoint.
func (c *Client) LLMURL() string { return c.base() + "/v1/desktop/llm/chat/completions" }

// LLMModels lists the hosted models the account may use.
func (c *Client) LLMModels(ctx context.Context) ([]string, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.authed(ctx, http.MethodGet, "/v1/desktop/llm/models", nil, &out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}
