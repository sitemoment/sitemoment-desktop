// SPDX-License-Identifier: AGPL-3.0-or-later
package tasks

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// sliceOf is the Search Console slice a GSC condition reads.
func sliceOf(c Condition) string {
	switch {
	case c.Kind == KindPosition || c.Scope == "query":
		return "query"
	case c.Scope == "page":
		return "page"
	}
	return "date"
}

// measure sums the condition's subject over [from, to].
func (s *Service) measure(ctx context.Context, siteID int64, c Condition, from, to string) (store.GSCStat, error) {
	switch sliceOf(c) {
	case "query":
		return s.Store.GSCKeyStat(ctx, siteID, "query", c.Keyword, from, to)
	case "page":
		return s.Store.GSCKeyStat(ctx, siteID, "page", c.URL, from, to)
	}
	t, err := s.Store.GSCTotals(ctx, siteID, from, to)
	return store.GSCStat{Clicks: t.Clicks, Impressions: t.Impressions, CTR: t.CTR, Position: t.Position}, err
}

// value picks the condition's number; false when it has none (a position without impressions).
func value(c Condition, st store.GSCStat) (float64, bool) {
	m := c.Metric
	if c.Kind == KindPosition {
		m = "position"
	}
	switch m {
	case "clicks":
		return st.Clicks, true
	case "impressions":
		return st.Impressions, true
	case "ctr":
		return st.CTR, st.Impressions > 0
	default:
		return math.Round(st.Position*10) / 10, st.Impressions > 0
	}
}

func day(t time.Time) string { return t.Format(store.DayLayout) }

// baseline measures a Search Console condition over the window before now (nil: no data).
func (s *Service) baseline(ctx context.Context, site store.Site, c Condition, now time.Time) *float64 {
	if c.Kind != KindPosition && c.Kind != KindMetric {
		return nil
	}
	latest, err := s.Store.LatestDay(ctx, site.ID, "gsc", sliceOf(c))
	if err != nil || latest == "" {
		return nil
	}
	to := day(now.AddDate(0, 0, -1))
	if latest < to {
		to = latest
	}
	end, _ := time.Parse(store.DayLayout, to)
	st, err := s.measure(ctx, site.ID, c, day(end.AddDate(0, 0, -(c.Window-1))), to)
	if err != nil {
		return nil
	}
	if v, ok := value(c, st); ok {
		return &v
	}
	return nil
}

// check evaluates one condition of an open task.
func (s *Service) check(ctx context.Context, site store.Site, t store.Task, c Condition) Outcome {
	switch c.Kind {
	case KindPosition, KindMetric:
		return s.checkGSC(ctx, site, t, c)
	case KindStatus:
		return s.checkStatus(ctx, t, c)
	case KindIndexed:
		return s.checkIndexed(ctx, site, t, c)
	}
	return Outcome{State: StatePending, Note: "未知的条件类型"}
}

// gscFinalDelay: the newest synced days can still be revised by Google, so
// they are excluded from every window a condition measures.
// gscMinDays: a GSC condition stays pending until it has this many full,
// usable days of data after the task started (or the whole window, if
// that is shorter).
const (
	gscFinalDelay = 2
	gscMinDays    = 7
)

// checkGSC measures from the day the task opened (at most Window days, up
// to the newest usable synced day): only data after the change counts, the
// newest gscFinalDelay days are left out as provisional, and it stays
// pending until there are at least gscMinDays of them.
func (s *Service) checkGSC(ctx context.Context, site store.Site, t store.Task, c Condition) Outcome {
	if site.GSCProperty == "" {
		return Outcome{State: StatePending, Note: "这个站点没有绑定 Search Console"}
	}
	raw, err := s.Store.LatestDay(ctx, site.ID, "gsc", sliceOf(c))
	if err != nil || raw == "" {
		return Outcome{State: StatePending, Note: "本机还没有 Search Console 数据"}
	}
	rawT, err := time.Parse(store.DayLayout, raw)
	if err != nil {
		return Outcome{State: StatePending, Note: "本机的 Search Console 数据日期有问题"}
	}
	latest := day(rawT.AddDate(0, 0, -gscFinalDelay)) // the newest days can still be revised
	start := day(time.Unix(t.StartedAt, 0))
	// Never measure past the due date: data from after it must not retroactively pass a task.
	if due := day(time.Unix(t.DueAt, 0)); t.DueAt > 0 && latest > due {
		latest = due
	}
	if latest < start {
		return Outcome{State: StatePending, Note: fmt.Sprintf("等 Search Console 同步到 %s 之后的数据（GSC 晚 2–3 天）", start)}
	}
	startT, _ := time.Parse(store.DayLayout, start)
	end, _ := time.Parse(store.DayLayout, latest)
	if minDays := min(gscMinDays, c.Window); int(end.Sub(startT).Hours()/24)+1 < minDays {
		return Outcome{State: StatePending, Note: fmt.Sprintf("任务开始后还不满 %d 天可用的 Search Console 数据，暂时不判断", minDays)}
	}
	from := day(end.AddDate(0, 0, -(c.Window - 1)))
	if from < start {
		from = start
	}
	st, err := s.measure(ctx, site.ID, c, from, latest)
	if err != nil {
		return Outcome{State: StatePending, Note: err.Error()}
	}
	cur, ok := value(c, st)
	period := fmt.Sprintf("GSC %s 至 %s", from, latest)
	if !ok {
		return Outcome{State: StatePending, Note: period + "：没有展示，还看不到排名"}
	}
	o := Outcome{State: StateUnmet, Current: &cur, Note: fmt.Sprintf("%s：%s", period, num(cur))}
	if c.Metric == "ctr" {
		o.Note = fmt.Sprintf("%s：%s%%", period, num(cur*100))
	}
	if compare(c.Op, cur, c.Value) {
		o.State = StatePass
	}
	return o
}

func (s *Service) checkStatus(ctx context.Context, t store.Task, c Condition) Outcome {
	if s.Fetcher == nil {
		return Outcome{State: StatePending, Note: "不能联网检查"}
	}
	tr, err := s.Fetcher.Trace(ctx, c.URL)
	if err != nil {
		return Outcome{State: StatePending, Note: err.Error()}
	}
	if tr.Error != "" || len(tr.Hops) == 0 {
		return Outcome{State: StateUnmet, Note: "请求失败：" + tr.Error}
	}
	first := tr.Hops[0].Status
	cur := float64(first)
	o := Outcome{State: StateUnmet, Current: &cur, Note: fmt.Sprintf("现在返回 %d", first)}
	if first >= 300 && first < 400 {
		o.Note += "，跳到 " + tr.FinalURL
	}
	if first == c.Status || (c.Status == 301 && first == 308) {
		o.State = StatePass
	}
	// A live check only ever reflects right now; after the due date it cannot
	// prove the URL was already right by the deadline, so it must not pass.
	if o.State == StatePass && t.DueAt > 0 && s.now().Unix() > t.DueAt {
		o.State = StateUnmet
		o.Note += "（检查发生在到期之后，不能证明到期前已经达标）"
	}
	return o
}

func (s *Service) checkIndexed(ctx context.Context, site store.Site, t store.Task, c Condition) Outcome {
	if site.GSCProperty == "" {
		return Outcome{State: StatePending, Note: "这个站点没有绑定 Search Console，不能检查收录"}
	}
	p, err := s.Store.Page(ctx, site.ID, c.URL)
	if errors.Is(err, store.ErrNotFound) || (err == nil && p.IndexCheckedAt == 0) {
		return Outcome{State: StatePending, Note: "等下一次体检用 Search Console 网址检查"}
	}
	if err != nil {
		return Outcome{State: StatePending, Note: err.Error()}
	}
	if p.IndexCheckedAt < t.StartedAt {
		// A verdict from before the task started proves nothing about what the task changed.
		return Outcome{State: StatePending, Note: "等任务开始之后的网址检查（上一次检查是在任务开始之前）"}
	}
	when := time.Unix(p.IndexCheckedAt, 0).Format("2006-01-02")
	if p.IndexVerdict == "PASS" {
		if t.DueAt > 0 && p.IndexCheckedAt > t.DueAt {
			// Indexed only after the deadline: does not prove it was already indexed by then.
			return Outcome{State: StateUnmet, Note: "网址检查发生在到期之后，不能证明到期前已经收录（" + when + "）"}
		}
		return Outcome{State: StatePass, Note: "谷歌已收录（网址检查 " + when + "）"}
	}
	reason := p.IndexCoverage
	if reason == "" {
		reason = p.IndexVerdict
	}
	return Outcome{State: StateUnmet, Note: reason + "（网址检查 " + when + "）"}
}
