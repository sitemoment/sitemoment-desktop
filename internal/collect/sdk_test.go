// SPDX-License-Identifier: AGPL-3.0-or-later
package collect

import (
	"strings"
	"testing"
)

func TestScriptEmbedsRunInputAndCollector(t *testing.T) {
	run := Run{ID: "r1", Nonce: `n"1`, Hosts: []string{"www.google.com"}}
	js := Script(run, "sm.done({ok: true});", map[string]any{"q": "</script>"})
	for _, want := range []string{`"r1"`, `"n\"1"`, `{"q":"\u003c/script\u003e"}`, "sm.done({ok: true});", "messageHandlers", "chrome.webview", "async function (sm)"} {
		if !strings.Contains(js, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if strings.Contains(js, "__INPUT__") || strings.Contains(js, "__COLLECTOR__") {
		t.Error("placeholders left in the script")
	}
}
