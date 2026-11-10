// SPDX-License-Identifier: AGPL-3.0-or-later
package collect

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed sdk.js
var sdkJS string

// Script returns the JavaScript to inject into a page: the SDK bound to run,
// wrapping the collector body, which sees the SDK as `sm`.
func Script(run Run, collector string) string {
	id, _ := json.Marshal(run.ID)
	nonce, _ := json.Marshal(run.Nonce)
	return strings.NewReplacer(
		"__RUN__", string(id),
		"__NONCE__", string(nonce),
		"__COLLECTOR__", collector,
	).Replace(sdkJS)
}
