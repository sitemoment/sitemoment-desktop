// SPDX-License-Identifier: AGPL-3.0-or-later
package collect

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed sdk.js
var sdkJS string

// Script returns the JavaScript to inject into a page: the SDK bound to run
// and input, wrapping the collector body, which sees the SDK as `sm` and may
// use await.
func Script(run Run, collector string, input map[string]any) string {
	id, _ := json.Marshal(run.ID)
	nonce, _ := json.Marshal(run.Nonce)
	if input == nil {
		input = map[string]any{}
	}
	in, err := json.Marshal(input)
	if err != nil {
		in = []byte("{}")
	}
	return strings.NewReplacer(
		"__RUN__", string(id),
		"__NONCE__", string(nonce),
		"__INPUT__", string(in),
		"__COLLECTOR__", collector,
	).Replace(sdkJS)
}
