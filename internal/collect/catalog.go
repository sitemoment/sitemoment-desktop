// SPDX-License-Identifier: AGPL-3.0-or-later
package collect

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// Collector statuses in the catalog.
const (
	StatusEnabled       = "enabled"
	StatusNeedsApproval = "needs_approval"
	StatusInvalid       = "invalid"
)

// CollectorView is a collector as the Collectors page shows it.
type CollectorView struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Tool          string   `json:"tool"`
	Version       string   `json:"version"`
	Hosts         []string `json:"hosts"`
	Builtin       bool     `json:"builtin"`
	Status        string   `json:"status"`
	Error         string   `json:"error"`
	DailyLimit    int      `json:"dailyLimit"`
	CacheHours    int      `json:"cacheHours"`
	MinGapSeconds int      `json:"minGapSeconds"`
}

// Catalog holds the built-in collectors (trusted) and the user's own from
// Dir, which run only after the user has approved the hosts they read.
type Catalog struct {
	Builtin fs.FS // collectors.FS
	Dir     string
	Store   *store.Store
	// Reserved are agent tool names a user collector may not take.
	Reserved map[string]bool
	Now      func() time.Time

	mu      sync.RWMutex
	views   []CollectorView
	enabled map[string]*Collector
	pending map[string]*Collector // user collectors waiting for approval
}

// ErrUnknownCollector means there is no enabled collector with that id.
var ErrUnknownCollector = errors.New("collect: no such collector")

func (c *Catalog) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Reload reads the built-in collectors and the user's directory again. A
// broken built-in is a programming error and fails the reload; a broken
// user collector is listed as invalid.
func (c *Catalog) Reload(ctx context.Context) error {
	var views []CollectorView
	enabled, pending := map[string]*Collector{}, map[string]*Collector{}
	tools := map[string]bool{}
	if c.Builtin != nil {
		dirs, err := fs.ReadDir(c.Builtin, ".")
		if err != nil {
			return err
		}
		for _, d := range dirs {
			if !d.IsDir() {
				continue
			}
			col, err := Load(c.Builtin, d.Name(), true)
			if err != nil {
				return fmt.Errorf("collect: built-in collector %s: %w", d.Name(), err)
			}
			enabled[col.ID], tools[col.Tool] = col, true
			views = append(views, viewOf(col, StatusEnabled, ""))
		}
	}
	if c.Dir != "" {
		if err := os.MkdirAll(c.Dir, 0o700); err != nil {
			return err
		}
		dirs, err := os.ReadDir(c.Dir)
		if err != nil {
			return err
		}
		var user []CollectorView
		for _, d := range dirs {
			if !d.IsDir() {
				continue
			}
			v, col := c.loadUser(ctx, d.Name(), enabled, pending, tools)
			if col != nil {
				tools[col.Tool] = true
				if v.Status == StatusEnabled {
					enabled[col.ID] = col
				} else {
					pending[col.ID] = col
				}
			}
			user = append(user, v)
		}
		sort.Slice(user, func(i, j int) bool { return user[i].ID < user[j].ID })
		views = append(views, user...)
	}
	c.mu.Lock()
	c.views, c.enabled, c.pending = views, enabled, pending
	c.mu.Unlock()
	return nil
}

// loadUser loads the user collector in directory name. Invalid ones are
// listed under the directory name.
func (c *Catalog) loadUser(ctx context.Context, name string, enabled, pending map[string]*Collector, tools map[string]bool) (CollectorView, *Collector) {
	col, err := Load(os.DirFS(filepath.Join(c.Dir, name)), ".", false)
	invalid := func(msg string) (CollectorView, *Collector) {
		v := CollectorView{ID: name, Name: name, Status: StatusInvalid, Error: msg, Hosts: []string{}}
		if col != nil {
			v = viewOf(col, StatusInvalid, msg)
			v.ID = name
		}
		return v, nil
	}
	switch {
	case err != nil:
		return invalid(err.Error())
	case col.ID != name:
		return invalid(fmt.Sprintf("目录名 %s 和 manifest 里的 id %s 不一致", name, col.ID))
	case enabled[col.ID] != nil || pending[col.ID] != nil:
		return invalid("id 和另一个采集器重复")
	case tools[col.Tool] || c.Reserved[col.Tool]:
		return invalid(fmt.Sprintf("工具名 %s 已被占用", col.Tool))
	}
	g, err := c.Store.CollectorGrant(ctx, col.ID)
	if err == nil && g.Version == col.Version && sameHosts(g.Hosts, col.Hosts) {
		return viewOf(col, StatusEnabled, ""), col
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return invalid(err.Error())
	}
	return viewOf(col, StatusNeedsApproval, ""), col
}

func sameHosts(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func viewOf(c *Collector, status, msg string) CollectorView {
	return CollectorView{ID: c.ID, Name: c.Name, Tool: c.Tool, Version: c.Version, Hosts: c.Hosts, Builtin: c.Builtin,
		Status: status, Error: msg, DailyLimit: c.DailyLimit, CacheHours: c.CacheHours, MinGapSeconds: c.MinGapSeconds}
}

// List returns every collector the last Reload found.
func (c *Catalog) List() []CollectorView {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return slices.Clone(c.views)
}

// Enabled returns the collectors that may run (built-ins first, then by id).
func (c *Catalog) Enabled() []*Collector {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*Collector, 0, len(c.enabled))
	for _, col := range c.enabled {
		out = append(out, col)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Builtin != out[j].Builtin {
			return out[i].Builtin
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Get returns an enabled collector.
func (c *Catalog) Get(id string) (*Collector, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	col, ok := c.enabled[id]
	return col, ok
}

// hostFloor is the strictest minGapSeconds (max) and dailyLimit (min)
// among every enabled collector whose Host is host, built-in or not: the
// shared budget a run against that real site must respect regardless of
// which collector made the request. A built-in collector using the host
// is included like any other, so it always sets at least as strict a
// floor as its own manifest already asked for; a user collector sharing
// the host can only make the shared budget stricter, never looser.
func (c *Catalog) hostFloor(host string) (minGapSeconds, dailyLimit int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, col := range c.enabled {
		if col.Host() != host {
			continue
		}
		if col.MinGapSeconds > minGapSeconds {
			minGapSeconds = col.MinGapSeconds
		}
		if dailyLimit == 0 || col.DailyLimit < dailyLimit {
			dailyLimit = col.DailyLimit
		}
	}
	return
}

// collectorsForHost returns the ids of enabled collectors whose Host is
// host, for summing a shared daily count across all of them.
func (c *Catalog) collectorsForHost(host string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var ids []string
	for _, col := range c.enabled {
		if col.Host() == host {
			ids = append(ids, col.ID)
		}
	}
	return ids
}

// Approve records the user's permission for a user collector to read its
// hosts (this version only) and reloads.
func (c *Catalog) Approve(ctx context.Context, id string) error {
	c.mu.RLock()
	col, ok := c.pending[id]
	c.mu.RUnlock()
	if !ok {
		return fmt.Errorf("collect: %s is not waiting for approval", id)
	}
	if err := c.Store.GrantCollector(ctx, col.ID, col.Version, col.Hosts, c.now().Unix()); err != nil {
		return err
	}
	return c.Reload(ctx)
}
