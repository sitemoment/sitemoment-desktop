// SPDX-License-Identifier: AGPL-3.0-or-later
package gscsync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/google"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// TokenSource returns a Google access token for a connected account.
type TokenSource interface {
	AccessToken(ctx context.Context, account string) (string, error)
}

// Querier runs Search Analytics queries (google.GSC).
type Querier interface {
	Query(ctx context.Context, token, property string, q google.Query) ([]google.Row, error)
}

// Slice is one dimension set kept locally.
type Slice struct {
	Name       string
	Dimensions []string
	ChunkDays  int
}

// Slices are the dimension sets synced in M1 (web search only).
var Slices = []Slice{
	{Name: "date", Dimensions: []string{"date"}, ChunkDays: 90},
	{Name: "query", Dimensions: []string{"date", "query"}, ChunkDays: 1},
	{Name: "page", Dimensions: []string{"date", "page"}, ChunkDays: 1},
}

// ErrNotBound means the site has no Search Console property yet.
var ErrNotBound = errors.New("gscsync: site has no Search Console property")

// Progress is reported after every request.
type Progress struct {
	SiteID int64  `json:"siteId"`
	Slice  string `json:"slice"`
	Day    string `json:"day"`
	Done   int    `json:"done"`
	Total  int    `json:"total"`
}

// Result says how much a run did and how many spans are left.
type Result struct {
	Requests  int `json:"requests"`
	Remaining int `json:"remaining"`
}

// Syncer copies one site's Search Console data into the store.
type Syncer struct {
	Store        *store.Store
	Tokens       TokenSource
	GSC          Querier
	Now          func() time.Time // default time.Now
	Months       int              // history; default 16
	Tail         int              // newest days re-fetched; default 3
	RefreshAfter time.Duration    // tail days older than this are re-fetched; default 6h
	MaxRequests  int              // per SyncSite call; default 200
	Progress     func(Progress)
}

func or[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}

// SyncSite fetches what Plan says is missing or stale, up to MaxRequests.
func (s *Syncer) SyncSite(ctx context.Context, site store.Site) (Result, error) {
	var res Result
	if site.GSCProperty == "" || site.GoogleAccount == "" {
		return res, ErrNotBound
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	through := Through(now)
	from := through.AddDate(0, -or(s.Months, 16), 0)
	refreshBefore := now.Add(-or(s.RefreshAfter, 6*time.Hour)).Unix()
	limit := or(s.MaxRequests, 200)

	token, err := s.Tokens.AccessToken(ctx, site.GoogleAccount)
	if err != nil {
		return res, err
	}
	for _, sl := range Slices {
		done, err := s.Store.SyncDays(ctx, site.ID, "gsc", sl.Name)
		if err != nil {
			return res, err
		}
		spans := Plan(done, from, through, or(s.Tail, 3), refreshBefore, sl.ChunkDays)
		for i, sp := range spans {
			if res.Requests >= limit {
				res.Remaining += len(spans) - i
				break
			}
			start, end := sp.From.Format(store.DayLayout), sp.Through.Format(store.DayLayout)
			rows, err := s.GSC.Query(ctx, token, site.GSCProperty, google.Query{Start: start, End: end, Dimensions: sl.Dimensions})
			res.Requests++
			if err != nil && !errors.Is(err, google.ErrTruncated) {
				return res, fmt.Errorf("search console %s %s..%s: %w", sl.Name, start, end, err)
			}
			if err := s.Store.ReplaceGSC(ctx, site.ID, sl.Name, start, end, toRows(sl, rows), now.Unix()); err != nil {
				return res, err
			}
			if s.Progress != nil {
				s.Progress(Progress{SiteID: site.ID, Slice: sl.Name, Day: start, Done: i + 1, Total: len(spans)})
			}
		}
	}
	return res, nil
}

func toRows(sl Slice, rows []google.Row) []store.GSCRow {
	out := make([]store.GSCRow, 0, len(rows))
	for _, r := range rows {
		if len(r.Keys) != len(sl.Dimensions) {
			continue
		}
		row := store.GSCRow{Day: r.Keys[0], Clicks: r.Clicks, Impressions: r.Impressions, Position: r.Position}
		switch sl.Name {
		case "query":
			row.Query = r.Keys[1]
		case "page":
			row.Page = r.Keys[1]
		}
		out = append(out, row)
	}
	return out
}
