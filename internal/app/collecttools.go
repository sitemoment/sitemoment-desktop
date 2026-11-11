// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/sitemoment/sitemoment-desktop/internal/agent"
	"github.com/sitemoment/sitemoment-desktop/internal/collect"
)

// CollectorTools hands the collector runtime to the agent: every enabled
// collector becomes a tool, and run errors are wrapped so the agent can
// explain them (and suggest the paid Google results instead).
type CollectorTools struct {
	Catalog *collect.Catalog
	Runner  interface {
		Run(ctx context.Context, id string, input map[string]any) (collect.Result, error)
	}
}

// Tools lists the enabled collectors as agent tools.
func (c *CollectorTools) Tools() []agent.CollectorTool {
	var out []agent.CollectorTool
	for _, col := range c.Catalog.Enabled() {
		out = append(out, agent.CollectorTool{ID: col.ID, Tool: col.Tool, Name: col.Name, Description: col.Description, Params: col.Input})
	}
	return out
}

// Run runs collector id with input.
func (c *CollectorTools) Run(ctx context.Context, id string, input map[string]any) (agent.CollectorResult, error) {
	r, err := c.Runner.Run(ctx, id, input)
	switch {
	case err == nil:
		return agent.CollectorResult{Data: r.Data, Cached: r.Cached, FetchedAt: r.FetchedAt}, nil
	case errors.Is(err, collect.ErrDailyLimit):
		return agent.CollectorResult{}, fmt.Errorf("%w: %s", agent.ErrCollectorLimit, collect.Describe(err))
	case errors.Is(err, collect.ErrHumanTimeout), errors.Is(err, collect.ErrClosed):
		return agent.CollectorResult{}, fmt.Errorf("%w: %s", agent.ErrCollectorHuman, collect.Describe(err))
	case errors.Is(err, context.Canceled):
		return agent.CollectorResult{}, err
	default:
		return agent.CollectorResult{}, fmt.Errorf("%s: %w", collect.Describe(err), err)
	}
}
