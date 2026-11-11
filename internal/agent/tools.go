// SPDX-License-Identifier: AGPL-3.0-or-later
package agent

import "context"

// toolset is the tools offered in one turn and whether paid tools are among
// them: local data needs a site, paid data needs a sign-in and a mode that
// may spend.
func (a *Agent) toolset(ctx context.Context, env *Env, mode string) ([]*Tool, bool) {
	var out []*Tool
	if env.Site.ID != 0 {
		out = append(out, localTools()...)
	}
	d := env.Deps
	out = append(out, webTools(d)...)
	if d.Collectors != nil {
		out = append(out, collectorTools(d.Collectors.Tools())...)
	}
	paid := false
	if mode != ModeFreeOnly && d.Cloud != nil && d.Cloud.SignedIn() {
		if ct := cloudTools(ctx, d); len(ct) > 0 {
			out = append(out, ct...)
			paid = true
		}
	}
	return out, paid
}
