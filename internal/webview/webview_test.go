// SPDX-License-Identifier: AGPL-3.0-or-later
package webview

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddleware(t *testing.T) {
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("app")) }))
	do := func(path, window string) (int, string) {
		req := httptest.NewRequest("GET", path, nil)
		if window != "" {
			req.Header.Set(windowHeader, window)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	if code, body := do(BootPath, "collector-1"); code != 200 || !strings.Contains(body, "wails:runtime:ready") {
		t.Fatalf("boot page = %d %q", code, body)
	}
	for _, p := range []string{"/", "/wails/runtime", "/assets/index.js"} {
		if code, _ := do(p, "collector-2"); code != http.StatusForbidden {
			t.Errorf("collector window got %s: %d", p, code)
		}
	}
	if code, body := do("/wails/runtime", ""); code != 200 || body != "app" {
		t.Fatalf("main window = %d %q", code, body)
	}
}
