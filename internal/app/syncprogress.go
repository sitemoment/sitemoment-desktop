// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/gasync"
	"github.com/sitemoment/sitemoment-desktop/internal/gscsync"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// progressMonths is how much history the progress table measures "days
// covered" against — the same default as gscsync/gasync's Syncer.Months.
const progressMonths = 16

// Chinese state labels shown in the sync progress table (SyncService.Progress).
const (
	stateSyncing     = "同步中"
	stateBackfilling = "回填中"
	stateDone        = "已完成"
	statePausedLabel = "已暂停"
	stateFailedLabel = "失败"
)

// gscSliceLabels and gaSliceLabels name gscsync.Slices and gasync.Slices in
// Chinese for the progress table.
var (
	gscSliceLabels = map[string]string{"date": "按日期", "query": "查询词", "page": "页面"}
	gaSliceLabels  = map[string]string{"date": "按日期", "landing": "着陆页", "channel": "渠道"}
)

// SliceProgress is one source's one locally kept slice's coverage over the
// last progressMonths months.
type SliceProgress struct {
	Slice       string `json:"slice"`
	Label       string `json:"label"`
	DaysCovered int    `json:"daysCovered"`
	DaysInRange int    `json:"daysInRange"`
	NewestDay   string `json:"newestDay,omitempty"`
	LastFetched int64  `json:"lastFetched,omitempty"`
	State       string `json:"state"`
}

// SourceProgress is one site's one data source's (Search Console, GA4,
// Bing) progress.
type SourceProgress struct {
	Source         string          `json:"source"` // "gsc" | "ga" | "bing"
	Label          string          `json:"label"`
	State          string          `json:"state"`
	Error          string          `json:"error,omitempty"`
	NextBackfillAt int64           `json:"nextBackfillAt,omitempty"`
	Slices         []SliceProgress `json:"slices,omitempty"`
}

// SiteProgress is one site's sync progress table row: every data source it
// is bound to, each with its slices.
type SiteProgress struct {
	SiteID  int64            `json:"siteId"`
	Host    string           `json:"host"`
	Sources []SourceProgress `json:"sources"`
}

// Progress builds the sync progress table (进度表): for every site and
// every source it is bound to — Search Console and GA4 broken down by
// slice, Bing as one row — how much of the last progressMonths months is
// locally covered, the newest synced day, when it was last fetched, and a
// state (同步中 / 回填中 / 已完成 / 已暂停 / 失败, with the error for a
// failed site). It reads the sync_days ledger plus SyncService's own
// in-memory run state; call again after a "sync:status" or "sync:paused"
// event to refresh.
func (s *SyncService) Progress(ctx context.Context) ([]SiteProgress, error) {
	sites, err := s.Store.Sites(ctx)
	if err != nil {
		return nil, err
	}
	paused := s.Paused()
	now := time.Now()
	out := make([]SiteProgress, 0, len(sites))
	for _, site := range sites {
		s.mu.Lock()
		status := s.status[site.ID]
		backfillLeft := s.backfillLeft[site.ID]
		s.mu.Unlock()
		running := status.State == "running"
		failed := status.State == "failed"

		sp := SiteProgress{SiteID: site.ID, Host: site.Host}

		if site.GSCProperty != "" {
			through := gscsync.Through(now)
			from := through.AddDate(0, -progressMonths, 0)
			var names []string
			for _, sl := range gscsync.Slices {
				names = append(names, sl.Name)
			}
			slices, err := s.sliceProgress(ctx, site.ID, "gsc", names, gscSliceLabels, from, through)
			if err != nil {
				return nil, err
			}
			src := SourceProgress{Source: "gsc", Label: "Search Console", Slices: slices}
			src.State, src.Error = sourceState(paused, running, failed, status.Error, anySliceBackfilling(slices))
			if backfillLeft && src.State == stateBackfilling {
				src.NextBackfillAt = status.At + int64(backfillLoopInterval.Seconds())
			}
			sp.Sources = append(sp.Sources, src)
		}

		if site.GAProperty != "" {
			through := gasync.Through(now)
			from := through.AddDate(0, -progressMonths, 0)
			var names []string
			for _, sl := range gasync.Slices {
				names = append(names, sl.Name)
			}
			slices, err := s.sliceProgress(ctx, site.ID, "ga", names, gaSliceLabels, from, through)
			if err != nil {
				return nil, err
			}
			src := SourceProgress{Source: "ga", Label: "GA4", Slices: slices}
			src.State, src.Error = sourceState(paused, running, failed, status.Error, anySliceBackfilling(slices))
			sp.Sources = append(sp.Sources, src)
		}

		if site.BingSite != "" {
			last, err := s.Store.LastFetched(ctx, site.ID, "bing", "fetch")
			if err != nil {
				return nil, err
			}
			// Bing always re-fetches its whole (short) history at once
			// (bingsync), so there is no partial backfill: either it has
			// never been fetched yet, or it is fully up to date.
			src := SourceProgress{Source: "bing", Label: "Bing"}
			src.State, src.Error = sourceState(paused, running, failed, status.Error, last == 0)
			src.Slices = []SliceProgress{{Slice: "fetch", Label: "流量与查询", LastFetched: last, State: src.State}}
			sp.Sources = append(sp.Sources, src)
		}

		out = append(out, sp)
	}
	return out, nil
}

// sourceState picks one of the 5 Chinese progress states for a source from
// the site-wide run flags, falling back to backfilling (a coverage gap
// remains) or done.
func sourceState(paused, running, failed bool, errMsg string, backfilling bool) (state, errOut string) {
	switch {
	case paused:
		return statePausedLabel, ""
	case running:
		return stateSyncing, ""
	case failed:
		return stateFailedLabel, errMsg
	case backfilling:
		return stateBackfilling, ""
	default:
		return stateDone, ""
	}
}

// anySliceBackfilling reports whether any slice still has a coverage gap.
func anySliceBackfilling(slices []SliceProgress) bool {
	for _, sl := range slices {
		if sl.DaysCovered < sl.DaysInRange {
			return true
		}
	}
	return false
}

// sliceProgress reports, for each of a source's slices, how many days of
// [from, through] its ledger (sync_days) covers, the newest synced day,
// and when it was last fetched (the latest fetched_at over the whole
// ledger, not just the range, so a tail refresh still counts as recent).
func (s *SyncService) sliceProgress(ctx context.Context, siteID int64, source string, slices []string, labels map[string]string, from, through time.Time) ([]SliceProgress, error) {
	rangeDays := int(through.Sub(from).Hours()/24) + 1
	fromStr, throughStr := from.Format(store.DayLayout), through.Format(store.DayLayout)
	out := make([]SliceProgress, 0, len(slices))
	for _, name := range slices {
		done, err := s.Store.SyncDays(ctx, siteID, source, name)
		if err != nil {
			return nil, err
		}
		var covered int
		var newest string
		var lastFetched int64
		for day, at := range done {
			if day >= fromStr && day <= throughStr {
				covered++
				if day > newest {
					newest = day
				}
			}
			if at > lastFetched {
				lastFetched = at
			}
		}
		sl := SliceProgress{Slice: name, Label: labels[name], DaysCovered: covered, DaysInRange: rangeDays, NewestDay: newest, LastFetched: lastFetched}
		if covered < rangeDays {
			sl.State = stateBackfilling
		} else {
			sl.State = stateDone
		}
		out = append(out, sl)
	}
	return out, nil
}
