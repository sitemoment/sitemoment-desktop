// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !darwin && !windows

package netproxy

// readSystem: there is no single system proxy setting to read on Linux
// (GNOME, KDE and others each keep their own); the environment is used.
func readSystem() (System, error) { return System{}, nil }
