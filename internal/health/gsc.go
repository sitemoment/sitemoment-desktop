// SPDX-License-Identifier: AGPL-3.0-or-later
package health

import (
	"context"
	"fmt"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/rank"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// Drop rules: the last 28 synced days against the 28 before, from local data.
const (
	dropRatio      = -0.30
	minPrevClicks  = 20
	minPrevImpr    = 200
	gscWindowDays  = 28
	impressionDays = 28
)

// GSCWindow is the Search Console comparison of a check.
type GSCWindow struct {
	From            string  `json:"from"`
	To              string  `json:"to"`
	PrevFrom        string  `json:"prevFrom"`
	PrevTo          string  `json:"prevTo"`
	Clicks          float64 `json:"clicks"`
	Impressions     float64 `json:"impressions"`
	PrevClicks      float64 `json:"prevClicks"`
	PrevImpressions float64 `json:"prevImpressions"`
}

// change is (cur - prev) / prev, or 0 without a baseline.
func change(cur, prev float64) float64 {
	if prev <= 0 {
		return 0
	}
	return (cur - prev) / prev
}

// gscDrops compares the site's last 28 synced Search Console days with the
// 28 before. It returns nil without local data.
func gscDrops(ctx context.Context, st *store.Store, siteID int64) (*GSCWindow, []store.Issue, error) {
	latest, err := st.LatestDay(ctx, siteID, "gsc", "date")
	if err != nil || latest == "" {
		return nil, nil, err
	}
	to, err := time.Parse(store.DayLayout, latest)
	if err != nil {
		return nil, nil, err
	}
	day := func(t time.Time) string { return t.Format(store.DayLayout) }
	from := to.AddDate(0, 0, -(gscWindowDays - 1))
	prevTo := from.AddDate(0, 0, -1)
	prevFrom := prevTo.AddDate(0, 0, -(gscWindowDays - 1))
	w := &GSCWindow{From: day(from), To: latest, PrevFrom: day(prevFrom), PrevTo: day(prevTo)}
	cur, err := st.GSCTotals(ctx, siteID, w.From, w.To)
	if err != nil {
		return nil, nil, err
	}
	prev, err := st.GSCTotals(ctx, siteID, w.PrevFrom, w.PrevTo)
	if err != nil {
		return nil, nil, err
	}
	w.Clicks, w.Impressions, w.PrevClicks, w.PrevImpressions = cur.Clicks, cur.Impressions, prev.Clicks, prev.Impressions
	var issues []store.Issue
	period := fmt.Sprintf("%s 至 %s 对比 %s 至 %s", w.From, w.To, w.PrevFrom, w.PrevTo)
	if c := change(w.Clicks, w.PrevClicks); w.PrevClicks >= minPrevClicks && c <= dropRatio {
		is := newIssue(CodeClicksDrop, rank.SeverityP1, nil)
		is.Count = 1
		is.Detail = fmt.Sprintf("谷歌点击从 %.0f 降到 %.0f（%.0f%%，%s，GSC 本机数据）。", w.PrevClicks, w.Clicks, c*100, period) + is.Detail
		issues = append(issues, is)
	}
	if c := change(w.Impressions, w.PrevImpressions); w.PrevImpressions >= minPrevImpr && c <= dropRatio {
		is := newIssue(CodeImprDrop, rank.SeverityP1, nil)
		is.Count = 1
		is.Detail = fmt.Sprintf("谷歌展示从 %.0f 降到 %.0f（%.0f%%，%s，GSC 本机数据）。", w.PrevImpressions, w.Impressions, c*100, period) + is.Detail
		issues = append(issues, is)
	}
	return w, issues, nil
}
