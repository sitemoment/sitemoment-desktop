// SPDX-License-Identifier: AGPL-3.0-or-later
package collect

import (
	"strings"
	"testing"
)

func TestScriptEmbedsRunInputAndCollector(t *testing.T) {
	run := Run{ID: "r1", Nonce: `n"1`, Hosts: []string{"www.google.com"}}
	js := Script(run, "sm.done({ok: true});", map[string]any{"q": "</script>"})
	for _, want := range []string{`"r1"`, `"n\"1"`, `{"q":"\u003c/script\u003e"}`, `["www.google.com"]`, "sm.done({ok: true});", "messageHandlers", "chrome.webview", "async function (sm)"} {
		if !strings.Contains(js, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if strings.Contains(js, "__INPUT__") || strings.Contains(js, "__COLLECTOR__") || strings.Contains(js, "__HOSTS__") {
		t.Error("placeholders left in the script")
	}
	// The host allow-list check must run before the nonce is assigned to a
	// variable a malicious page's own scripts could read.
	if i, j := strings.Index(js, "location.hostname"), strings.Index(js, "var run ="); i < 0 || j < 0 || i > j {
		t.Fatalf("host check is not before the nonce is touched: host at %d, nonce line at %d", i, j)
	}
}

func TestScriptNoHostsAllowsNone(t *testing.T) {
	js := Script(Run{ID: "r1", Nonce: "n1"}, "sm.done({});", nil)
	if !strings.Contains(js, "var allowedHosts = []") {
		t.Fatalf("empty host list should still be a valid (empty) array: %s", js)
	}
}
