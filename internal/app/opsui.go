// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/sitemoment/sitemoment-desktop/internal/health"
	"github.com/sitemoment/sitemoment-desktop/internal/report"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
	"github.com/sitemoment/sitemoment-desktop/internal/tasks"
)

// HealthService serves the health panel, the Health page and the site list
// scores. Progress arrives as "health:status" events.
type HealthService struct{ Ops *Ops }

// HealthView is a site's latest check for the UI.
type HealthView struct {
	// Latest is the newest finished check (nil before the first one).
	Latest  *store.Crawl   `json:"latest"`
	Summary health.Summary `json:"summary"`
	Issues  []store.Issue  `json:"issues"`
	// Last is the newest check of any status (a failed or running one shows here).
	Last    *store.Crawl  `json:"last"`
	History []store.Crawl `json:"history"`
	Running bool          `json:"running"`
}

// Run starts a health check of the site in the background.
func (h *HealthService) Run(ctx context.Context, siteID int64) error {
	h.Ops.mu.Lock()
	busy := h.Ops.running[siteID]
	h.Ops.mu.Unlock()
	if busy {
		return ErrChecking
	}
	go h.Ops.CheckSite(h.Ops.ctx(), siteID, "manual")
	return nil
}

// Get returns the site's latest check, its issues and the last 30 checks.
func (h *HealthService) Get(ctx context.Context, siteID int64) (HealthView, error) {
	st := h.Ops.Store
	v := HealthView{Issues: []store.Issue{}, History: []store.Crawl{}}
	h.Ops.mu.Lock()
	v.Running = h.Ops.running[siteID]
	h.Ops.mu.Unlock()
	hist, err := st.Crawls(ctx, siteID, 30)
	if err != nil {
		return v, err
	}
	v.History = hist
	if len(hist) > 0 {
		v.Last = &hist[0]
	}
	latest, err := st.LatestCrawl(ctx, siteID)
	if errors.Is(err, store.ErrNotFound) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	v.Latest = &latest
	json.Unmarshal([]byte(latest.Summary), &v.Summary)
	v.Issues, err = st.CrawlIssues(ctx, latest.ID)
	return v, err
}

// Scores lists every site's latest score for the site list.
func (h *HealthService) Scores(ctx context.Context) ([]store.SiteHealth, error) {
	return h.Ops.Store.SiteHealths(ctx)
}

// PageList is a page of the pages table.
type PageList struct {
	Pages []store.Page `json:"pages"`
	Total int          `json:"total"`
}

// Pages lists the pages the checks remember (filter: all, error, noindex, redirect, not_indexed).
func (h *HealthService) Pages(ctx context.Context, siteID int64, filter, contains string) (PageList, error) {
	pages, total, err := h.Ops.Store.Pages(ctx, store.PageQuery{SiteID: siteID, Filter: filter, Contains: strings.TrimSpace(contains), Limit: 300})
	return PageList{Pages: pages, Total: total}, err
}

// TaskService serves the Tasks page.
type TaskService struct{ Ops *Ops }

// List returns a site's tasks, newest first.
func (s *TaskService) List(ctx context.Context, siteID int64) ([]tasks.View, error) {
	return s.Ops.List(ctx, siteID)
}

// Confirm opens a task the agent proposed.
func (s *TaskService) Confirm(ctx context.Context, id int64) (tasks.View, error) {
	v, err := s.Ops.Tasks.Confirm(ctx, id)
	if err == nil {
		s.Ops.taskNotice(ctx, v) // it may pass on its first check
	}
	return v, taskError(err)
}

// Dismiss drops a proposed or open task.
func (s *TaskService) Dismiss(ctx context.Context, id int64) error {
	return taskError(s.Ops.Tasks.Dismiss(ctx, id))
}

// Delete removes a task.
func (s *TaskService) Delete(ctx context.Context, id int64) error {
	return s.Ops.Store.DeleteTask(ctx, id)
}

// Check evaluates an open task now.
func (s *TaskService) Check(ctx context.Context, id int64) (tasks.View, error) {
	v, err := s.Ops.Tasks.Check(ctx, id)
	if err == nil {
		s.Ops.taskNotice(ctx, v)
	}
	return v, err
}

// AutoAccept reports whether tasks the agent proposes open without confirmation.
func (s *TaskService) AutoAccept(ctx context.Context) (bool, error) {
	v, err := s.Ops.Store.Setting(ctx, autoAcceptKey)
	return v == "1", err
}

// SetAutoAccept turns auto-accept on or off.
func (s *TaskService) SetAutoAccept(ctx context.Context, on bool) error {
	v := ""
	if on {
		v = "1"
	}
	return s.Ops.Store.SetSetting(ctx, autoAcceptKey, v)
}

func taskError(err error) error {
	if errors.Is(err, tasks.ErrState) {
		return errors.New("任务的状态已经变了，请刷新")
	}
	return err
}

// ReportService serves the Reports page.
type ReportService struct {
	Ops *Ops
	// Save asks the user where to save a file (main.go: the system save
	// dialog); "" means cancelled.
	Save func(name string) (string, error)
}

// List returns a site's reports without their bodies.
func (s *ReportService) List(ctx context.Context, siteID int64) ([]store.Report, error) {
	return s.Ops.Store.Reports(ctx, siteID)
}

// Get returns one report with Markdown and HTML.
func (s *ReportService) Get(ctx context.Context, id int64) (store.Report, error) {
	return s.Ops.Store.Report(ctx, id)
}

// Generate makes a report of the site now.
func (s *ReportService) Generate(ctx context.Context, siteID int64) (store.Report, error) {
	site, err := s.Ops.Store.Site(ctx, siteID)
	if err != nil {
		return store.Report{}, err
	}
	return report.Build(ctx, s.Ops.Store, site, s.Ops.now(), "manual")
}

// Delete removes a report.
func (s *ReportService) Delete(ctx context.Context, id int64) error {
	return s.Ops.Store.DeleteReport(ctx, id)
}

// Export saves a report as "html" or "md" where the user picks; it returns
// the path ("" when the user cancelled).
func (s *ReportService) Export(ctx context.Context, id int64, format string) (string, error) {
	r, err := s.Ops.Store.Report(ctx, id)
	if err != nil {
		return "", err
	}
	data := r.HTML
	if format == "md" {
		data = r.Markdown
	} else {
		format = "html"
	}
	if s.Save == nil {
		return "", errors.New("这个环境不能导出文件")
	}
	site, _ := s.Ops.Store.Site(ctx, r.SiteID)
	path, err := s.Save(fmt.Sprintf("%s-%s-%s.%s", orDefault(site.Host, "site"), "周报", r.To, format))
	if err != nil || path == "" {
		return "", err
	}
	return path, os.WriteFile(path, []byte(data), 0o644)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// NoticeService serves the in-app notices; new ones arrive as "notice:new" events.
type NoticeService struct{ Store *store.Store }

// NoticeList is the newest notices and the unread count.
type NoticeList struct {
	Items  []store.Notice `json:"items"`
	Unread int            `json:"unread"`
}

// List returns the newest 50 notices.
func (s *NoticeService) List(ctx context.Context) (NoticeList, error) {
	items, unread, err := s.Store.Notices(ctx, 50)
	return NoticeList{Items: items, Unread: unread}, err
}

// MarkRead marks one notice read (0: all).
func (s *NoticeService) MarkRead(ctx context.Context, id int64) error {
	return s.Store.MarkNoticesRead(ctx, id)
}
