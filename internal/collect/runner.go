// SPDX-License-Identifier: AGPL-3.0-or-later
package collect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// ErrDailyLimit means the collector has used up today's runs.
var ErrDailyLimit = errors.New("collect: the collector's daily limit is used up")

// Loader runs one page (Browser).
type Loader interface {
	Load(ctx context.Context, p Page) (json.RawMessage, error)
}

// PostFunc rewrites a collector's result before it is stored.
type PostFunc func(ctx context.Context, data json.RawMessage) (json.RawMessage, error)

// Result is a collector's answer.
type Result struct {
	RunID     int64           `json:"runId"`
	Collector string          `json:"collector"`
	Data      json.RawMessage `json:"data"`
	Cached    bool            `json:"cached"`
	FetchedAt int64           `json:"fetchedAt"`
}

// RunView is a run as the Collectors page shows it.
type RunView struct {
	ID         int64  `json:"id"`
	Collector  string `json:"collector"`
	Input      string `json:"input"`
	Status     string `json:"status"`
	Error      string `json:"error"`
	CreatedAt  int64  `json:"createdAt"`
	FinishedAt int64  `json:"finishedAt"`
}

// Stats is the collector queue for the status bar.
type Stats struct {
	Running int        `json:"running"`
	Queued  int        `json:"queued"`
	Today   []DayCount `json:"today"`
}

// DayCount is how many real runs a collector made today (local time).
type DayCount struct {
	Collector string `json:"collector"`
	Name      string `json:"name"`
	Count     int    `json:"count"`
	Limit     int    `json:"limit"`
}

// Runner runs enabled collectors: answers from the cache when it can,
// otherwise one run at a time per collector, spaced by the collector's
// minimum gap plus jitter, within its daily limit. Every real run is a row
// in collector_runs.
type Runner struct {
	Store   *store.Store
	Catalog *Catalog
	Browser Loader
	Post    map[string]PostFunc
	Emit    func(name string, data any)
	Now     func() time.Time
	Wait    func(ctx context.Context, d time.Duration) error
	Rand    func() float64

	mu      sync.Mutex
	locks   map[string]chan struct{}
	last    map[string]time.Time
	windows map[int64]string
	running int
	queued  int
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) wait(ctx context.Context, d time.Duration) error {
	if r.Wait != nil {
		return r.Wait(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (r *Runner) rand() float64 {
	if r.Rand != nil {
		return r.Rand()
	}
	return rand.Float64()
}

func (r *Runner) emit(name string, data any) {
	if r.Emit != nil {
		r.Emit(name, data)
	}
}

// lock serializes runs of one collector; it waits for its turn or ctx.
func (r *Runner) lock(ctx context.Context, id string) (func(), error) {
	r.mu.Lock()
	if r.locks == nil {
		r.locks, r.last, r.windows = map[string]chan struct{}{}, map[string]time.Time{}, map[int64]string{}
	}
	ch := r.locks[id]
	if ch == nil {
		ch = make(chan struct{}, 1)
		r.locks[id] = ch
	}
	r.queued++
	r.mu.Unlock()
	r.emitStats(ctx)
	defer func() {
		r.mu.Lock()
		r.queued--
		r.mu.Unlock()
	}()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// InputHash identifies a run's input for the cache: collector, version and
// the normalized input as canonical JSON.
func InputHash(c *Collector, input map[string]any) (string, string) {
	raw, _ := json.Marshal(input) // map keys are sorted
	sum := sha256.Sum256([]byte(c.ID + "\n" + c.Version + "\n" + string(raw)))
	return hex.EncodeToString(sum[:]), string(raw)
}

func startOfDay(t time.Time) int64 {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location()).Unix()
}

// Run answers collector id on input.
func (r *Runner) Run(ctx context.Context, id string, input map[string]any) (Result, error) {
	c, ok := r.Catalog.Get(id)
	if !ok {
		return Result{}, ErrUnknownCollector
	}
	in, err := c.Normalize(input)
	if err != nil {
		return Result{}, err
	}
	hash, canon := InputHash(c, in)
	if res, ok, err := r.cached(ctx, c, hash); ok || err != nil {
		return res, err
	}
	unlock, err := r.lock(ctx, c.ID)
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	// Another run of the same input may have finished while this one queued.
	if res, ok, err := r.cached(ctx, c, hash); ok || err != nil {
		return res, err
	}
	now := r.now()
	n, err := r.Store.CollectorRunsSince(ctx, c.ID, startOfDay(now))
	if err != nil {
		return Result{}, err
	}
	if n >= c.DailyLimit {
		return Result{}, ErrDailyLimit
	}
	r.mu.Lock()
	last := r.last[c.ID]
	r.mu.Unlock()
	gap := time.Duration(c.MinGapSeconds)*time.Second + time.Duration(r.rand()*float64(c.JitterSeconds)*float64(time.Second))
	if d := last.Add(gap).Sub(now); !last.IsZero() && d > 0 {
		if err := r.wait(ctx, d); err != nil {
			return Result{}, err
		}
	}
	pageURL, err := c.PageURL(in)
	if err != nil {
		return Result{}, err
	}
	return r.run(ctx, c, in, hash, canon, pageURL)
}

func (r *Runner) cached(ctx context.Context, c *Collector, hash string) (Result, bool, error) {
	run, err := r.Store.CachedCollectorRun(ctx, c.ID, hash, r.now().Unix())
	if errors.Is(err, store.ErrNotFound) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, err
	}
	return Result{RunID: run.ID, Collector: c.ID, Data: json.RawMessage(run.Result), Cached: true, FetchedAt: run.FinishedAt}, true, nil
}

func (r *Runner) run(ctx context.Context, c *Collector, in map[string]any, hash, canon, pageURL string) (Result, error) {
	db := context.WithoutCancel(ctx)
	started := r.now()
	runID, err := r.Store.InsertCollectorRun(db, c.ID, hash, canon, started.Unix())
	if err != nil {
		return Result{}, err
	}
	r.mu.Lock()
	r.last[c.ID] = started
	r.running++
	r.mu.Unlock()
	r.emitRun(db, runID)
	r.emitStats(db)
	defer func() {
		r.mu.Lock()
		r.running--
		delete(r.windows, runID)
		r.mu.Unlock()
		r.emitRun(db, runID)
		r.emitStats(db)
	}()

	data, err := r.Browser.Load(ctx, Page{
		URL: pageURL, Hosts: c.Hosts, Script: c.Script, Input: in,
		Timeout: time.Duration(c.TimeoutSeconds) * time.Second, HumanTimeout: 2 * time.Minute,
		OnHuman: func(window, reason string) {
			r.mu.Lock()
			r.windows[runID] = window
			r.mu.Unlock()
			r.Store.SetCollectorRunStatus(db, runID, store.RunNeedHuman)
			r.emitRun(db, runID)
		},
	})
	if err == nil && r.Post[c.ID] != nil {
		data, err = r.Post[c.ID](ctx, data)
	}
	finished := r.now().Unix()
	if err != nil {
		status := store.RunError
		if errors.Is(err, context.Canceled) || errors.Is(err, ErrClosed) {
			status = store.RunCancelled
		}
		r.Store.FinishCollectorRun(db, runID, status, "", Describe(err), finished, 0)
		return Result{}, err
	}
	expires := finished + int64(c.CacheHours)*3600
	if err := r.Store.FinishCollectorRun(db, runID, store.RunOK, string(data), "", finished, expires); err != nil {
		return Result{}, err
	}
	return Result{RunID: runID, Collector: c.ID, Data: data, FetchedAt: finished}, nil
}

// Window is the window a run waiting for a human is shown in.
func (r *Runner) Window(runID int64) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.windows[runID]
	return w, ok
}

func viewRun(run store.CollectorRun) RunView {
	return RunView{ID: run.ID, Collector: run.Collector, Input: run.Input, Status: run.Status, Error: run.Error, CreatedAt: run.CreatedAt, FinishedAt: run.FinishedAt}
}

func (r *Runner) emitRun(ctx context.Context, id int64) {
	if r.Emit == nil {
		return
	}
	if run, err := r.Store.CollectorRun(ctx, id); err == nil {
		r.emit("collect:run", viewRun(run))
	}
}

func (r *Runner) emitStats(ctx context.Context) {
	if r.Emit == nil {
		return
	}
	if s, err := r.Stats(ctx); err == nil {
		r.emit("collect:stats", s)
	}
}

// Runs lists recent runs, all or with status.
func (r *Runner) Runs(ctx context.Context, status string, limit int) ([]RunView, error) {
	runs, err := r.Store.CollectorRuns(ctx, status, limit)
	if err != nil {
		return nil, err
	}
	out := make([]RunView, len(runs))
	for i, run := range runs {
		out[i] = viewRun(run)
	}
	return out, nil
}

// Stats is the queue and today's counts of every enabled collector.
func (r *Runner) Stats(ctx context.Context) (Stats, error) {
	r.mu.Lock()
	s := Stats{Running: r.running, Queued: r.queued, Today: []DayCount{}}
	r.mu.Unlock()
	since := startOfDay(r.now())
	for _, c := range r.Catalog.Enabled() {
		n, err := r.Store.CollectorRunsSince(ctx, c.ID, since)
		if err != nil {
			return s, err
		}
		s.Today = append(s.Today, DayCount{Collector: c.ID, Name: c.Name, Count: n, Limit: c.DailyLimit})
	}
	return s, nil
}

// Describe is a short Chinese explanation of a run error, for the agent
// and the UI.
func Describe(err error) string {
	switch {
	case errors.Is(err, ErrDailyLimit):
		return "这个采集器今天的次数用完了"
	case errors.Is(err, ErrHumanTimeout):
		return "页面要求人工验证，2 分钟内没有完成"
	case errors.Is(err, ErrClosed):
		return "采集窗口被关闭了"
	case errors.Is(err, ErrTimeout):
		return "页面没有按时返回结果"
	case errors.Is(err, context.Canceled):
		return "已取消"
	case errors.Is(err, ErrUnknownCollector):
		return "没有这个采集器，或者它还没有被允许运行"
	}
	// Anything else may carry text from the page itself (a collector's
	// error message); cap it before it goes into the run record or the
	// UI.
	return truncRunes(err.Error(), 300)
}
