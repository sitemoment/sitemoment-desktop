// SPDX-License-Identifier: AGPL-3.0-or-later
package collect

import (
	"strings"
	"testing"
)

func TestScriptEmbedsRunAndCollector(t *testing.T) {
	run := Run{ID: "r1", Nonce: `n"1`, Hosts: []string{"www.google.com"}}
	js := Script(run, "sm.post('result', {ok: true});")
	for _, want := range []string{`"r1"`, `"n\"1"`, "sm.post('result', {ok: true});", "messageHandlers", "chrome.webview"} {
		if !strings.Contains(js, want) {
			t.Errorf("script lacks %q", want)
		}
	}
}
