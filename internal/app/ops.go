// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/google"
	"github.com/sitemoment/sitemoment-desktop/internal/gscsync"
	"github.com/sitemoment/sitemoment-desktop/internal/health"
	"github.com/sitemoment/sitemoment-desktop/internal/report"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
	"github.com/sitemoment/sitemoment-desktop/internal/tasks"
)

const (
	// checkHour is when the daily health check runs (local time).
	checkHour = 8
	// reportEvery is how often a site gets a weekly report (a little
	// tolerance so a report made at 08:05 is due again at 08:03 a week later).
	reportEvery = 7*24*time.Hour - 12*time.Hour
	// autoAcceptKey: tasks the agent proposes open without the user's confirmation.
	autoAcceptKey = "tasks.autoAccept"
)

// HealthStatus is sent as event "health:status".
type HealthStatus struct {
	SiteID  int64  `json:"siteId"`
	State   string `json:"state"` // running | done | failed
	CrawlID int64  `json:"crawlId,omitempty"`
	Score   int    `json:"score,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Ops ties health checks, tasks, reports and notices together: the daily
// schedule, evaluation after syncs and checks, and the in-app notices.
// It is not a Wails service (HealthService, TaskService and ReportService
// wrap it for the UI).
type Ops struct {
	Store   *store.Store
	Checker *health.Checker
	Tasks   *tasks.Service
	Emit    func(name string, data any)
	Now     func() time.Time
	// Ctx is what background checks run under (main.go: cancelled on quit).
	Ctx context.Context

	mu      sync.Mutex
	running map[int64]bool
}

func (o *Ops) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o *Ops) ctx() context.Context {
	if o.Ctx != nil {
		return o.Ctx
	}
	return context.Background()
}

func (o *Ops) emit(name string, data any) {
	if o.Emit != nil {
		o.Emit(name, data)
	}
}

// notify stores an in-app notice and sends it as event "notice:new".
func (o *Ops) notify(ctx context.Context, n store.Notice) {
	n.CreatedAt = o.now().Unix()
	n, err := o.Store.AddNotice(context.WithoutCancel(ctx), n)
	if err != nil {
		log.Printf("app: notice: %v", err)
		return
	}
	o.emit("notice:new", n)
}

// ErrChecking means the site's health check is already running.
var ErrChecking = errors.New("这个站点正在体检")

// CheckSite runs one health check of a site now (one per site at a time),
// then notifies, evaluates the site's tasks and, for scheduled checks,
// makes the weekly report when one is due.
func (o *Ops) CheckSite(ctx context.Context, siteID int64, origin string) error {
	o.mu.Lock()
	if o.running == nil {
		o.running = map[int64]bool{}
	}
	if o.running[siteID] {
		o.mu.Unlock()
		return ErrChecking
	}
	o.running[siteID] = true
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		delete(o.running, siteID)
		o.mu.Unlock()
	}()

	site, err := o.Store.Site(ctx, siteID)
	if err != nil {
		return err
	}
	o.emit("health:status", HealthStatus{SiteID: siteID, State: "running"})
	watch, err := o.Tasks.Watched(ctx, siteID)
	if err != nil {
		return err
	}
	cr, err := o.Checker.Check(ctx, site, origin, watch)
	if err != nil {
		o.emit("health:status", HealthStatus{SiteID: siteID, State: "failed", CrawlID: cr.ID, Error: err.Error()})
		if ctx.Err() == nil {
			o.notify(ctx, store.Notice{SiteID: siteID, Kind: "health_failed", Title: site.Host + " 体检失败", Body: err.Error(), Ref: fmt.Sprintf("crawl:%d", cr.ID)})
		}
	} else {
		o.emit("health:status", HealthStatus{SiteID: siteID, State: "done", CrawlID: cr.ID, Score: cr.Score})
		if issues, err := o.Store.CrawlIssues(ctx, cr.ID); err == nil {
			var fresh []string
			for _, is := range issues {
				if is.New && is.Severity == "p0" {
					fresh = append(fresh, is.Title)
				}
			}
			if len(fresh) > 0 {
				o.notify(ctx, store.Notice{SiteID: siteID, Kind: "health_issues", Title: fmt.Sprintf("%s 体检发现新的 P0 问题（健康分 %d）", site.Host, cr.Score),
					Body: strings.Join(fresh, "；"), Ref: fmt.Sprintf("crawl:%d", cr.ID)})
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	o.EvaluateTasks(ctx, siteID)
	if origin == "schedule" {
		o.weeklyReport(ctx, site)
	}
	return err
}

// EvaluateTasks checks the site's open tasks and notifies about those that
// passed or failed (SyncService.After and CheckSite call it).
func (o *Ops) EvaluateTasks(ctx context.Context, siteID int64) {
	done, err := o.Tasks.Evaluate(ctx, siteID)
	if err != nil {
		log.Printf("app: evaluate tasks of site %d: %v", siteID, err)
	}
	for _, v := range done {
		o.taskNotice(ctx, v)
	}
	o.emit("tasks:changed", map[string]int64{"siteId": siteID})
}

func (o *Ops) taskNotice(ctx context.Context, v tasks.View) {
	n := store.Notice{SiteID: v.SiteID, Ref: fmt.Sprintf("task:%d", v.ID), Body: strings.Join(v.Labels, "；")}
	switch v.Status {
	case tasks.StatusPassed:
		n.Kind, n.Title = "task_passed", "任务已验收："+v.Title
	case tasks.StatusFailed:
		n.Kind, n.Title = "task_failed", "任务到期未达标："+v.Title
	default:
		return
	}
	o.notify(ctx, n)
}

func (o *Ops) weeklyReport(ctx context.Context, site store.Site) {
	last, err := o.Store.LastReportAt(ctx, site.ID, "weekly")
	if err != nil || (last > 0 && o.now().Sub(time.Unix(last, 0)) < reportEvery) {
		return
	}
	r, err := report.Build(ctx, o.Store, site, o.now(), "weekly")
	if err != nil {
		log.Printf("app: weekly report of %s: %v", site.Host, err)
		return
	}
	o.notify(ctx, store.Notice{SiteID: site.ID, Kind: "report_ready", Title: "周报已生成：" + r.Title, Ref: fmt.Sprintf("report:%d", r.ID)})
	o.emit("reports:changed", map[string]int64{"siteId": site.ID})
}

// Propose creates a task the agent suggested: open right away when the
// user turned on auto-accept, else waiting for confirmation (agent.Tasks).
func (o *Ops) Propose(ctx context.Context, p tasks.Proposal) (tasks.View, error) {
	auto, err := o.Store.Setting(ctx, autoAcceptKey)
	if err != nil {
		return tasks.View{}, err
	}
	p.Open = auto == "1"
	v, err := o.Tasks.Propose(ctx, p)
	if err != nil {
		return v, err
	}
	n := store.Notice{SiteID: v.SiteID, Kind: "task_proposed", Title: "Agent 建议了一个任务：" + v.Title, Body: strings.Join(v.Labels, "；"), Ref: fmt.Sprintf("task:%d", v.ID)}
	if v.Status == tasks.StatusOpen {
		n.Kind, n.Title = "task_opened", "Agent 加了一个任务："+v.Title
	}
	o.notify(ctx, n)
	o.emit("tasks:changed", map[string]int64{"siteId": v.SiteID})
	return v, nil
}

// List returns a site's tasks, newest first (agent.Tasks).
func (o *Ops) List(ctx context.Context, siteID int64) ([]tasks.View, error) {
	list, err := o.Store.Tasks(ctx, siteID)
	if err != nil {
		return nil, err
	}
	out := make([]tasks.View, len(list))
	for i, t := range list {
		out[i] = tasks.ViewOf(t)
	}
	return out, nil
}

// lastSlot is the most recent daily check time at or before now.
func lastSlot(now time.Time) time.Time {
	s := time.Date(now.Year(), now.Month(), now.Day(), checkHour, 0, 0, 0, now.Location())
	if now.Before(s) {
		s = s.AddDate(0, 0, -1)
	}
	return s
}

// RunDue checks, one after another, every site whose last check started
// before today's 08:00 (yesterday's before 08:00): missed checks run as soon
// as the app is open.
func (o *Ops) RunDue(ctx context.Context) {
	slot := lastSlot(o.now()).Unix()
	sites, err := o.Store.Sites(ctx)
	if err != nil {
		log.Printf("app: list sites for health checks: %v", err)
		return
	}
	for _, site := range sites {
		if ctx.Err() != nil {
			return
		}
		last, err := o.Store.LastCrawlAt(ctx, site.ID)
		if err != nil || last >= slot {
			continue
		}
		if err := o.CheckSite(ctx, site.ID, "schedule"); err != nil && !errors.Is(err, ErrChecking) && ctx.Err() == nil {
			log.Printf("app: health check of %s: %v", site.Host, err)
		}
	}
}

// RunOpsLoop runs due health checks now and then every interval until ctx
// ends (a short interval: the laptop may sleep through 08:00). A function,
// not a method, so it is not exposed to the frontend.
func RunOpsLoop(ctx context.Context, o *Ops, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		o.RunDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// GSCInspector runs URL Inspection with the site's Google account (health.Inspector).
type GSCInspector struct {
	GSC    *google.GSC
	Tokens gscsync.TokenSource
}

// Inspect asks Search Console about one page of the site's property.
func (g GSCInspector) Inspect(ctx context.Context, site store.Site, pageURL string) (google.Inspection, error) {
	if site.GoogleAccount == "" || site.GSCProperty == "" {
		return google.Inspection{}, errors.New("没有绑定 Search Console")
	}
	token, err := g.Tokens.AccessToken(ctx, site.GoogleAccount)
	if err != nil {
		return google.Inspection{}, err
	}
	return g.GSC.Inspect(ctx, token, site.GSCProperty, pageURL)
}
