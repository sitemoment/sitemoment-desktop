// SPDX-License-Identifier: AGPL-3.0-or-later
package agent

import (
	"github.com/sitemoment/sitemoment-desktop/internal/rank"
	"github.com/sitemoment/sitemoment-desktop/internal/webdata"
)

// ReservedToolNames are the names of the agent's own tools (local data, free
// web, SiteMoment data, people_also_ask and M4's create_task). A user
// collector may not register a tool under one of them.
func ReservedToolNames() map[string]bool {
	out := map[string]bool{"people_also_ask": true, "create_task": true}
	all := &Deps{Fetcher: &rank.Fetcher{}, PSI: &webdata.PSI{}, Wayback: &webdata.Wayback{}, Suggest: &webdata.Suggest{}}
	for _, t := range append(localTools(), webTools(all)...) {
		out[t.Name] = true
	}
	for _, sp := range paidSpecs {
		out[sp.name] = true
	}
	return out
}
