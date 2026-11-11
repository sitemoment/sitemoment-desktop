// SPDX-License-Identifier: AGPL-3.0-or-later
package updates

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/release"
	"github.com/wailsapp/wails/v3/pkg/updater"
)

// Update states, as the frontend sees them.
const (
	StateOff         = "off"         // development build, or no self-update on this platform
	StateIdle        = "idle"        // not checked yet in this run
	StateChecking    = "checking"    // asking GitHub
	StateUpToDate    = "uptodate"    // the latest release is what runs
	StateAvailable   = "available"   // a newer release, not downloaded
	StateDownloading = "downloading" // Written of Total bytes
	StateVerifying   = "verifying"   // SHA-256 (and signature) check
	StateInstalling  = "installing"  // unpacking next to the app
	StateReady       = "ready"       // verified and staged; Restart swaps it in
	StateError       = "error"       // Error says what failed (Stage: check, download, verify, install)
)

// Settings keys (the settings table, no secrets).
const (
	keyAuto      = "update.auto"      // "off" turns the daily check off
	keyCheckedAt = "update.checkedAt" // unix seconds of the last finished check
)

// AutoInterval is how often the app checks by itself.
const AutoInterval = 24 * time.Hour

// Status is what the update bar and Settings show.
type Status struct {
	Current   string   `json:"current"`
	State     string   `json:"state"`
	Reason    string   `json:"reason,omitempty"` // why updates are off
	Latest    *Version `json:"latest,omitempty"`
	Written   int64    `json:"written"`
	Total     int64    `json:"total"`
	Stage     string   `json:"stage,omitempty"`
	Error     string   `json:"error,omitempty"`
	CheckedAt int64    `json:"checkedAt"`
	Auto      bool     `json:"auto"`
}

// Version is a release found on GitHub.
type Version struct {
	Version     string `json:"version"`
	Notes       string `json:"notes"`
	URL         string `json:"url"`
	PublishedAt int64  `json:"publishedAt"`
	Size        int64  `json:"size"`
}

// Settings stores the auto-check switch and the last check (store.Store).
type Settings interface {
	Setting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
}

// engine is the part of *updater.Updater the service drives.
type engine interface {
	Check(ctx context.Context) (*updater.Release, error)
	DownloadAndInstall(ctx context.Context) error
	Restart(ctx context.Context) error
}

// Options configures New.
type Options struct {
	Version   string // the running version (main.version)
	PublicKey string // base64 Ed25519 key; "" accepts SHA-256-checked releases without a signature (integrity-only, see RELEASING.md)
	Settings  Settings
	Emit      func(name string, data any) // "update:status" to the main window
	OnFound   func(version string)        // the daily check found a new release (notification)
	Quit      func()                      // quits the app so the updater's helper can swap it in
	Provider  ProviderConfig              // PublicKey is set from the parsed Options.PublicKey
}

// Service is the app's update service (bound to the frontend as
// UpdateService): Status, Check, Install, Restart, SetAuto. Run does the
// daily check.
type Service struct {
	u        engine
	settings Settings
	emit     func(name string, data any)
	onFound  func(version string)
	now      func() time.Time
	ctx      context.Context

	mu sync.Mutex
	st Status
}

// New builds the service. Development builds, platforms without a
// self-update asset and a bad public key leave it off (State "off" with a
// Reason) rather than failing the app.
func New(ctx context.Context, o Options) *Service {
	s := &Service{settings: o.Settings, emit: o.Emit, onFound: o.OnFound, now: time.Now, ctx: ctx}
	s.st = Status{Current: o.Version, State: StateIdle}
	off := func(reason string) *Service {
		s.st.State, s.st.Reason = StateOff, reason
		return s
	}
	if !release.IsRelease(o.Version) {
		return off("开发版本（" + o.Version + "）不自动更新")
	}
	if UpdateAsset(runtime.GOOS, runtime.GOARCH) == "" {
		return off("这个平台没有自动更新，请到 GitHub Releases 下载新版本")
	}
	var pub ed25519.PublicKey
	if o.PublicKey != "" {
		pk, err := ParsePublicKey(o.PublicKey)
		if err != nil {
			return off(err.Error())
		}
		pub = pk
	}
	pc := o.Provider
	pc.PublicKey = pub
	if pc.HTTP == nil {
		pc.HTTP = &http.Client{Timeout: 10 * time.Minute}
	}
	p, err := NewProvider(pc)
	if err != nil {
		return off(err.Error())
	}
	u := updater.New(&host{s: s, quit: o.Quit})
	// Verification, including any signature, is this package's job (see the
	// checked provider in provider.go): the Release it returns never carries
	// a Signature, only a digest-only Verification, so Wails' own
	// Config.PublicKey stays empty here — it would otherwise require every
	// release to carry a Signature it never sees and reject them all.
	if err := u.Init(updater.Config{CurrentVersion: o.Version, Providers: []updater.Provider{p}, Window: updater.WindowNone}); err != nil {
		return off(err.Error())
	}
	s.u = u
	s.loadSettings()
	return s
}

func (s *Service) loadSettings() {
	if s.settings == nil {
		s.st.Auto = true
		return
	}
	auto, _ := s.settings.Setting(s.ctx, keyAuto)
	at, _ := s.settings.Setting(s.ctx, keyCheckedAt)
	s.st.Auto = auto != "off"
	s.st.CheckedAt, _ = strconv.ParseInt(at, 10, 64)
}

// Status returns the current update status.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot()
}

func (s *Service) snapshot() Status {
	st := s.st
	if st.Latest != nil {
		v := *st.Latest
		st.Latest = &v
	}
	return st
}

// busy reports whether a check must not start now: one is running, or a
// download is underway or staged.
func busy(state string) bool {
	switch state {
	case StateChecking, StateDownloading, StateVerifying, StateInstalling, StateReady:
		return true
	}
	return false
}

// Check asks GitHub for a newer release now (Settings → 检查更新).
func (s *Service) Check() (Status, error) {
	if s.u == nil {
		return s.Status(), errors.New(s.Status().Reason)
	}
	if st := s.Status(); busy(st.State) {
		return st, nil
	}
	_, err := s.check(s.ctx)
	return s.Status(), err
}

func (s *Service) check(ctx context.Context) (*updater.Release, error) {
	rel, err := s.u.Check(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now().Unix()
	s.mu.Lock()
	s.st.CheckedAt = now
	st := s.snapshot()
	s.mu.Unlock()
	if s.settings != nil {
		_ = s.settings.SetSetting(ctx, keyCheckedAt, strconv.FormatInt(now, 10))
	}
	s.send(st)
	return rel, nil
}

// Install downloads, verifies and stages the release Check found. It
// returns at once; progress and the result arrive as "update:status".
func (s *Service) Install() error {
	if s.u == nil {
		return errors.New(s.Status().Reason)
	}
	if st := s.Status().State; st != StateAvailable && st != StateError {
		return fmt.Errorf("updates: nothing to install (state %s)", st)
	}
	go func() { _ = s.u.DownloadAndInstall(s.ctx) }()
	return nil
}

// Restart quits the app and lets the updater's helper swap in the staged
// release and start it.
func (s *Service) Restart() error {
	if s.u == nil {
		return errors.New(s.Status().Reason)
	}
	if st := s.Status().State; st != StateReady {
		return fmt.Errorf("updates: no update is ready (state %s)", st)
	}
	return s.u.Restart(s.ctx)
}

// SetAuto turns the daily check on or off.
func (s *Service) SetAuto(on bool) (Status, error) {
	v := ""
	if !on {
		v = "off"
	}
	if s.settings != nil {
		if err := s.settings.SetSetting(s.ctx, keyAuto, v); err != nil {
			return s.Status(), err
		}
	}
	s.mu.Lock()
	s.st.Auto = on
	st := s.snapshot()
	s.mu.Unlock()
	s.send(st)
	return st, nil
}

func (s *Service) send(st Status) {
	if s.emit != nil {
		s.emit("update:status", st)
	}
}

// lifecycle turns the updater's events into the status.
func (s *Service) lifecycle(name string, data []any) {
	var arg any
	if len(data) > 0 {
		arg = data[0]
	}
	s.mu.Lock()
	switch name {
	case updater.EventCheckStarted:
		s.st.State, s.st.Error, s.st.Stage = StateChecking, "", ""
	case updater.EventNoUpdate:
		s.st.State, s.st.Latest = StateUpToDate, nil
	case updater.EventUpdateAvailable:
		s.st.State = StateAvailable
		if r, ok := arg.(*updater.Release); ok && r != nil {
			url, _ := r.Metadata["github.release.htmlURL"].(string)
			v := &Version{Version: r.Version, Notes: r.Notes, URL: url, Size: r.Artifact.Size}
			if !r.PublishedAt.IsZero() {
				v.PublishedAt = r.PublishedAt.Unix()
			}
			s.st.Latest = v
		}
	case updater.EventDownloadStarted:
		s.st.State, s.st.Written, s.st.Total, s.st.Error, s.st.Stage = StateDownloading, 0, 0, "", ""
	case updater.EventDownloadProgress:
		if p, ok := arg.(updater.Progress); ok {
			s.st.Written, s.st.Total = p.Written, p.Total
		}
	case updater.EventDownloadComplete:
		// verifying follows at once
	case updater.EventVerifying:
		s.st.State = StateVerifying
	case updater.EventInstalling:
		s.st.State = StateInstalling
	case updater.EventUpdateReady:
		s.st.State = StateReady
	case updater.EventError:
		s.st.State = StateError
		if e, ok := arg.(updater.ErrorInfo); ok {
			s.st.Stage, s.st.Error = string(e.Stage), e.Message
		}
	default:
		s.mu.Unlock()
		return
	}
	st := s.snapshot()
	s.mu.Unlock()
	s.send(st)
}

// Run checks once shortly after start and then whenever a day has passed
// since the last finished check (also across restarts), while the daily
// check is on. A release found this way is passed to OnFound.
func Run(ctx context.Context, s *Service, first, tick time.Duration) {
	if s.u == nil {
		return
	}
	t := time.NewTimer(first)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.autoCheck(ctx)
		t.Reset(tick)
	}
}

func (s *Service) autoCheck(ctx context.Context) {
	st := s.Status()
	if !st.Auto || busy(st.State) || s.now().Sub(time.Unix(st.CheckedAt, 0)) < AutoInterval {
		return
	}
	rel, err := s.check(ctx)
	if err == nil && rel != nil && s.onFound != nil {
		s.onFound(rel.Version)
	}
}

// host is the updater.Host behind the service's updater: its events feed
// the status (sent to the main window only, unlike app.Updater, whose
// events reach every window), and it never opens a window.
type host struct {
	s    *Service
	quit func()
}

func (h *host) Emit(name string, data ...any) bool { h.s.lifecycle(name, data); return false }

func (h *host) OnEvent(string, func(any)) func() { return func() {} }

func (h *host) OpenWindow(updater.WindowOptions) updater.WindowHandle { return noWindow{} }

func (h *host) Quit() {
	if h.quit != nil {
		h.quit()
	}
}

type noWindow struct{}

func (noWindow) EmitEvent(string, ...any) bool { return false }
func (noWindow) Show()                         {}
func (noWindow) Close()                        {}
