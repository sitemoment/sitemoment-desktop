// SPDX-License-Identifier: AGPL-3.0-or-later
package netproxy

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

// readSystem reads the current user's WinINet proxy settings (what Windows
// Settings → Proxy and Clash for Windows' "system proxy" write).
func readSystem() (System, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return System{}, err
	}
	defer k.Close()
	str := func(name string) (string, error) {
		v, _, err := k.GetStringValue(name)
		if errors.Is(err, registry.ErrNotExist) {
			return "", nil
		}
		return v, err
	}
	enable, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return System{}, err
	}
	server, err := str("ProxyServer")
	if err != nil {
		return System{}, err
	}
	override, err := str("ProxyOverride")
	if err != nil {
		return System{}, err
	}
	pac, err := str("AutoConfigURL")
	if err != nil {
		return System{}, err
	}
	return parseWindows(enable, server, override, pac), nil
}
