// SPDX-License-Identifier: AGPL-3.0-or-later
package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/rank"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// Task statuses.
const (
	StatusProposed  = "proposed"
	StatusOpen      = "open"
	StatusPassed    = "passed"
	StatusFailed    = "failed"
	StatusDismissed = "dismissed"
)

const (
	defaultDays = 28
	maxDays     = 180
	maxTitle    = 120
	maxDetail   = 2000
	// maxOpenTasks caps how many proposed/open tasks a site can have at once.
	maxOpenTasks = 30
)

// Service creates and checks tasks.
type Service struct {
	Store *store.Store
	// Fetcher checks url_status conditions (nil: they stay pending).
	Fetcher *rank.Fetcher
	Now     func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Proposal is a new task.
type Proposal struct {
	SiteID, SessionID int64
	Title, Detail     string
	// Days until the task is due (1–180, default 28), counted from when it opens.
	Days       int
	Conditions []Condition
	// Open skips the user's confirmation.
	Open bool
}

// View is a task for the UI and the agent.
type View struct {
	store.Task
	Conds    []Condition `json:"conds"`
	Outcomes []Outcome   `json:"outcomes"`
	// Labels describe the conditions in words.
	Labels []string `json:"labels"`
}

// ViewOf decodes a task's conditions and results.
func ViewOf(t store.Task) View {
	v := View{Task: t, Conds: []Condition{}, Outcomes: []Outcome{}, Labels: []string{}}
	json.Unmarshal([]byte(t.Conditions), &v.Conds)
	if t.Results != "" {
		json.Unmarshal([]byte(t.Results), &v.Outcomes)
	}
	for _, c := range v.Conds {
		v.Labels = append(v.Labels, Describe(c))
	}
	return v
}

// Propose stores a task (proposed, or open with its baseline and first check when p.Open).
func (s *Service) Propose(ctx context.Context, p Proposal) (View, error) {
	site, err := s.Store.Site(ctx, p.SiteID)
	if err != nil {
		return View{}, err
	}
	if open, err := s.Store.Tasks(ctx, site.ID, StatusProposed, StatusOpen); err != nil {
		return View{}, err
	} else if len(open) >= maxOpenTasks {
		return View{}, fmt.Errorf("%w: 这个站点的进行中任务已经有 %d 个，请先完成或删除一些", ErrCondition, maxOpenTasks)
	}
	p.Title = strings.TrimSpace(p.Title)
	if p.Title == "" || len([]rune(p.Title)) > maxTitle {
		return View{}, fmt.Errorf("%w: 标题要 1–%d 个字", ErrCondition, maxTitle)
	}
	if r := []rune(strings.TrimSpace(p.Detail)); len(r) > maxDetail {
		p.Detail = string(r[:maxDetail])
	}
	if p.Days == 0 {
		p.Days = defaultDays
	}
	if p.Days < 1 || p.Days > maxDays {
		return View{}, fmt.Errorf("%w: days 是 1–%d 天", ErrCondition, maxDays)
	}
	conds, err := Validate(p.Conditions, site)
	if err != nil {
		return View{}, err
	}
	raw, _ := json.Marshal(conds)
	t, err := s.Store.AddTask(ctx, store.Task{SiteID: site.ID, SessionID: p.SessionID, Title: p.Title, Detail: strings.TrimSpace(p.Detail),
		Status: StatusProposed, Conditions: string(raw), Days: p.Days, CreatedAt: s.now().Unix()})
	if err != nil || !p.Open {
		return ViewOf(t), err
	}
	return s.Confirm(ctx, t.ID)
}

// ErrState means the task is not in a status that allows the action.
var ErrState = errors.New("tasks: the task cannot do that in its current status")

// Confirm opens a proposed task: its deadline starts now, the baselines
// (the window before today) are measured and the conditions checked once.
func (s *Service) Confirm(ctx context.Context, id int64) (View, error) {
	t, err := s.Store.Task(ctx, id)
	if err != nil {
		return View{}, err
	}
	if t.Status != StatusProposed {
		return View{}, ErrState
	}
	site, err := s.Store.Site(ctx, t.SiteID)
	if err != nil {
		return View{}, err
	}
	now := s.now()
	t.Status, t.StartedAt, t.DueAt = StatusOpen, now.Unix(), now.AddDate(0, 0, t.Days).Unix()
	v := ViewOf(t)
	outs := make([]Outcome, len(v.Conds))
	for i, c := range v.Conds {
		outs[i].Baseline = s.baseline(ctx, site, c, now)
	}
	raw, _ := json.Marshal(outs)
	t.Results = string(raw)
	if ok, err := s.Store.UpdateTask(ctx, t, StatusProposed); err != nil || !ok {
		if err == nil {
			err = ErrState
		}
		return View{}, err
	}
	t, _, err = s.evaluate(ctx, t, site)
	return ViewOf(t), err
}

// Dismiss drops a proposed or open task.
func (s *Service) Dismiss(ctx context.Context, id int64) error {
	t, err := s.Store.Task(ctx, id)
	if err != nil {
		return err
	}
	if t.Status != StatusProposed && t.Status != StatusOpen {
		return ErrState
	}
	from := t.Status
	t.Status, t.DoneAt = StatusDismissed, s.now().Unix()
	ok, err := s.Store.UpdateTask(ctx, t, from)
	if err == nil && !ok {
		err = ErrState
	}
	return err
}

// Check evaluates one open task now.
func (s *Service) Check(ctx context.Context, id int64) (View, error) {
	t, err := s.Store.Task(ctx, id)
	if err != nil {
		return View{}, err
	}
	if t.Status != StatusOpen {
		return ViewOf(t), nil
	}
	site, err := s.Store.Site(ctx, t.SiteID)
	if err != nil {
		return View{}, err
	}
	t, _, err = s.evaluate(ctx, t, site)
	return ViewOf(t), err
}

// Evaluate checks every open task of a site and returns those that just
// passed or failed.
func (s *Service) Evaluate(ctx context.Context, siteID int64) ([]View, error) {
	site, err := s.Store.Site(ctx, siteID)
	if err != nil {
		return nil, err
	}
	open, err := s.Store.Tasks(ctx, siteID, StatusOpen)
	if err != nil {
		return nil, err
	}
	var done []View
	for _, t := range open {
		t, changed, err := s.evaluate(ctx, t, site)
		if err != nil {
			return done, err
		}
		if changed {
			done = append(done, ViewOf(t))
		}
	}
	return done, nil
}

// Watched lists the page URLs open tasks wait to see indexed (the health
// check inspects them first).
func (s *Service) Watched(ctx context.Context, siteID int64) ([]string, error) {
	open, err := s.Store.Tasks(ctx, siteID, StatusOpen)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, t := range open {
		for _, c := range ViewOf(t).Conds {
			if c.Kind == KindIndexed {
				out = append(out, c.URL)
			}
		}
	}
	return out, nil
}

// evaluate checks an open task's conditions, keeping their baselines, and
// closes it when all pass (passed) or it is due (failed).
func (s *Service) evaluate(ctx context.Context, t store.Task, site store.Site) (store.Task, bool, error) {
	v := ViewOf(t)
	now := s.now()
	outs := make([]Outcome, len(v.Conds))
	all := true
	for i, c := range v.Conds {
		o := s.check(ctx, site, t, c)
		o.At = now.Unix()
		if i < len(v.Outcomes) {
			o.Baseline = v.Outcomes[i].Baseline
		}
		outs[i] = o
		all = all && o.State == StatePass
	}
	raw, _ := json.Marshal(outs)
	t.Results, t.CheckedAt = string(raw), now.Unix()
	switch {
	case all:
		t.Status, t.DoneAt = StatusPassed, now.Unix()
	case now.Unix() >= t.DueAt:
		t.Status, t.DoneAt = StatusFailed, now.Unix()
	}
	ok, err := s.Store.UpdateTask(ctx, t, StatusOpen)
	if err != nil || !ok {
		return t, false, err // dismissed meanwhile: leave it
	}
	return t, t.Status != StatusOpen, nil
}
