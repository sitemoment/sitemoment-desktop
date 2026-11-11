// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"errors"

	"github.com/sitemoment/sitemoment-desktop/internal/collect"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// ErrRunOver means the run asked about is no longer waiting in a window.
var ErrRunOver = errors.New("这次采集已经结束了")

// CollectorService serves the Collectors page: the built-in and user
// collectors, recent runs, and runs waiting for a human check.
type CollectorService struct {
	Catalog *collect.Catalog
	Runner  *collect.Runner
	Browser interface{ Reveal(window string) bool }
}

// List returns every collector found by the last scan.
func (s *CollectorService) List(ctx context.Context) ([]collect.CollectorView, error) {
	return s.Catalog.List(), nil
}

// Rescan reads the user's collector directory again.
func (s *CollectorService) Rescan(ctx context.Context) ([]collect.CollectorView, error) {
	if err := s.Catalog.Reload(ctx); err != nil {
		return nil, err
	}
	return s.Catalog.List(), nil
}

// Approve lets a user collector read the hosts it lists (this version,
// this sha256 of its package). version, hosts and sha must match exactly
// what the Collectors page showed the user for id: see Catalog.Approve.
func (s *CollectorService) Approve(ctx context.Context, id, version string, hosts []string, sha string) ([]collect.CollectorView, error) {
	if err := s.Catalog.Approve(ctx, id, version, hosts, sha); err != nil {
		return nil, err
	}
	return s.Catalog.List(), nil
}

// Dir is where the user puts their own collectors.
func (s *CollectorService) Dir(ctx context.Context) (string, error) { return s.Catalog.Dir, nil }

// Runs lists the latest runs.
func (s *CollectorService) Runs(ctx context.Context, limit int) ([]collect.RunView, error) {
	return s.Runner.Runs(ctx, "", limit)
}

// NeedsHuman lists runs waiting for a person to complete a check.
func (s *CollectorService) NeedsHuman(ctx context.Context) ([]collect.RunView, error) {
	return s.Runner.Runs(ctx, store.RunNeedHuman, 50)
}

// Reveal brings the window of a run waiting for a human to the front.
func (s *CollectorService) Reveal(ctx context.Context, runID int64) error {
	w, ok := s.Runner.Window(runID)
	if !ok || !s.Browser.Reveal(w) {
		return ErrRunOver
	}
	return nil
}

// Stats is the collector queue and today's counts.
func (s *CollectorService) Stats(ctx context.Context) (collect.Stats, error) {
	return s.Runner.Stats(ctx)
}
