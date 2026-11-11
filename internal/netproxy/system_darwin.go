// SPDX-License-Identifier: AGPL-3.0-or-later
package netproxy

import (
	"context"
	"os/exec"
	"time"
)

// readSystem asks configd for the current proxy settings (what System
// Settings → Network → Proxies and ClashX/V2RayU's "set system proxy" write).
func readSystem() (System, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--proxy").Output()
	if err != nil {
		return System{}, err
	}
	return parseScutil(string(out)), nil
}
