// SPDX-License-Identifier: AGPL-3.0-or-later
package updates

import (
	"context"
	"crypto/sha256"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// memSettings is an in-memory settings table.
type memSettings struct {
	mu sync.Mutex
	m  map[string]string
}

func (m *memSettings) Setting(_ context.Context, k string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.m[k], nil
}

func (m *memSettings) SetSetting(_ context.Context, k, v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.m == nil {
		m.m = map[string]string{}
	}
	if v == "" {
		delete(m.m, k)
	} else {
		m.m[k] = v
	}
	return nil
}

// newTestService returns the service on the fake GitHub and a function that
// returns the "update:status" events sent so far.
func newTestService(t *testing.T, f *fakeGitHub, version, key string, set *memSettings) (*Service, func() []Status) {
	t.Helper()
	var mu sync.Mutex
	var events []Status
	s := New(ctx, Options{Version: version, PublicKey: key, Settings: set,
		Emit: func(name string, data any) {
			if name == "update:status" {
				mu.Lock()
				events = append(events, data.(Status))
				mu.Unlock()
			}
		},
		Provider: ProviderConfig{APIBase: f.srv.URL, DownloadBase: f.srv.URL}})
	return s, func() []Status {
		mu.Lock()
		defer mu.Unlock()
		return append([]Status(nil), events...)
	}
}

func waitState(t *testing.T, s *Service, states ...string) Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st := s.Status()
		for _, want := range states {
			if st.State == want {
				return st
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("state = %+v, want one of %v", s.Status(), states)
	return Status{}
}

func TestDevelopmentBuildsDoNotUpdate(t *testing.T) {
	s := New(ctx, Options{Version: "dev"})
	if st := s.Status(); st.State != StateOff || !strings.Contains(st.Reason, "开发版本") {
		t.Fatalf("status = %+v", st)
	}
	if _, err := s.Check(); err == nil {
		t.Fatal("Check on a dev build must fail")
	}
	if s.Install() == nil || s.Restart() == nil {
		t.Fatal("Install/Restart on a dev build must fail")
	}
	if st := New(ctx, Options{Version: "1.0.0", PublicKey: "not-a-key"}).Status(); st.State != StateOff {
		t.Fatalf("bad key = %+v", st)
	}
}

func TestCheckInstallsAVerifiedRelease(t *testing.T) {
	f := newFakeGitHub(t, "v1.1.0")
	set := &memSettings{}
	s, events := newTestService(t, f, "1.0.0", "", set)
	if st := s.Status(); st.State != StateIdle || !st.Auto || st.Current != "1.0.0" {
		t.Fatalf("initial = %+v", st)
	}
	st, err := s.Check()
	if err != nil || st.State != StateAvailable || st.Latest == nil || st.Latest.Version != "1.1.0" || st.Latest.Notes != "- 新功能" ||
		!strings.HasSuffix(st.Latest.URL, "/releases/tag/v1.1.0") || st.Latest.PublishedAt == 0 || st.CheckedAt == 0 {
		t.Fatalf("check = %+v, %v", st, err)
	}
	if v, _ := set.Setting(ctx, keyCheckedAt); v == "" {
		t.Fatal("last check not saved")
	}
	if err := s.Install(); err != nil {
		t.Fatal(err)
	}
	st = waitState(t, s, StateReady, StateError)
	if st.State != StateReady || st.Written != int64(len(f.payload)) {
		t.Fatalf("install = %+v", st)
	}
	seen := map[string]bool{}
	for _, e := range events() {
		seen[e.State] = true
	}
	for _, want := range []string{StateChecking, StateAvailable, StateDownloading, StateVerifying, StateReady} {
		if !seen[want] {
			t.Errorf("no %s event in %v", want, seen)
		}
	}
	if _, err := s.Check(); err != nil || s.Status().State != StateReady {
		t.Fatal("a staged update must not be checked over")
	}
}

func TestTamperedDownloadFailsVerification(t *testing.T) {
	f := newFakeGitHub(t, "v1.1.0")
	f.sums = strings.Repeat("ab", 32) + "  " + UpdateAsset(runtime.GOOS, runtime.GOARCH) + "\n"
	s, _ := newTestService(t, f, "1.0.0", "", &memSettings{})
	if _, err := s.Check(); err != nil {
		t.Fatal(err)
	}
	s.Install()
	if st := waitState(t, s, StateReady, StateError); st.State != StateError || st.Stage != "verify" || !strings.Contains(st.Error, "digest mismatch") {
		t.Fatalf("status = %+v", st)
	}
}

func TestReleaseWithoutChecksumIsRefused(t *testing.T) {
	f := newFakeGitHub(t, "v1.1.0")
	f.sums = strings.Repeat("0", 64) + "  other.zip\n"
	s, _ := newTestService(t, f, "1.0.0", "", &memSettings{})
	if _, err := s.Check(); err == nil || !strings.Contains(err.Error(), "no SHA-256") {
		t.Fatalf("err = %v", err)
	}
	if st := s.Status(); st.State != StateError || st.Stage != "check" {
		t.Fatalf("status = %+v", st)
	}
}

func TestUpToDate(t *testing.T) {
	f := newFakeGitHub(t, "v1.0.0")
	s, _ := newTestService(t, f, "1.0.0", "", &memSettings{})
	if st, err := s.Check(); err != nil || st.State != StateUpToDate || st.Latest != nil {
		t.Fatalf("check = %+v, %v", st, err)
	}
}

func TestSignedReleases(t *testing.T) {
	pub, seed, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	f := newFakeGitHub(t, "v1.1.0")
	name := UpdateAsset(runtime.GOOS, runtime.GOARCH)
	// No .sig: refused at check time, before anything is downloaded.
	s, _ := newTestService(t, f, "1.0.0", pub, &memSettings{})
	if _, err := s.Check(); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("unsigned release: %v", err)
	}
	if st := s.Status(); st.State != StateError || st.Stage != "check" {
		t.Fatalf("unsigned release status = %+v", st)
	}
	// A good signature, bound to this exact tag and asset name, installs.
	sum := sha256.Sum256(f.payload)
	if f.sig, err = SignRelease(seed, f.tag, name, sum[:]); err != nil {
		t.Fatal(err)
	}
	s, _ = newTestService(t, f, "1.0.0", pub, &memSettings{})
	if _, err := s.Check(); err != nil {
		t.Fatal(err)
	}
	s.Install()
	if st := waitState(t, s, StateReady, StateError); st.State != StateReady {
		t.Fatalf("signed = %+v", st)
	}
	// A signature by another key is refused at check time too (our provider
	// verifies it, not Wails' own download/verify step).
	_, other, _ := GenerateKey()
	f.sig, _ = SignRelease(other, f.tag, name, sum[:])
	s, _ = newTestService(t, f, "1.0.0", pub, &memSettings{})
	if _, err := s.Check(); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("wrong key: %v", err)
	}
	if st := s.Status(); st.State != StateError || st.Stage != "check" {
		t.Fatalf("wrong key status = %+v", st)
	}
}

func TestDailyCheck(t *testing.T) {
	f := newFakeGitHub(t, "v1.1.0")
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		auto, checkedAt string
		want            bool
	}{
		{"", "", true},
		{"", fmt.Sprint(now.Add(-time.Hour).Unix()), false},
		{"", fmt.Sprint(now.Add(-25 * time.Hour).Unix()), true},
		{"off", "", false},
	} {
		set := &memSettings{m: map[string]string{keyAuto: c.auto, keyCheckedAt: c.checkedAt}}
		s, _ := newTestService(t, f, "1.0.0", "", set)
		s.now = func() time.Time { return now }
		var found string
		s.onFound = func(v string) { found = v }
		before := f.apiCalls.Load()
		s.autoCheck(ctx)
		if checked := f.apiCalls.Load() > before; checked != c.want || (c.want && found != "1.1.0") {
			t.Errorf("auto=%q checkedAt=%q: checked %v found %q, want %v", c.auto, c.checkedAt, checked, found, c.want)
		}
	}
	set := &memSettings{}
	s, _ := newTestService(t, f, "1.0.0", "", set)
	if st, err := s.SetAuto(false); err != nil || st.Auto || set.m[keyAuto] != "off" {
		t.Fatalf("SetAuto(false) = %+v %v %v", st, err, set.m)
	}
	if st, _ := s.SetAuto(true); !st.Auto || set.m[keyAuto] != "" {
		t.Fatalf("SetAuto(true) = %+v %v", st, set.m)
	}
}
