// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import "os"

// Info is shown in the status bar and settings.
type Info struct {
	Version string `json:"version"`
	DataDir string `json:"dataDir"`
	DBBytes int64  `json:"dbBytes"`
}

// InfoService reports the app version and local data size.
type InfoService struct {
	Version string
	DataDir string
	DBPath  string
}

func (i *InfoService) Get() Info {
	var size int64
	for _, p := range []string{i.DBPath, i.DBPath + "-wal", i.DBPath + "-shm"} {
		if fi, err := os.Stat(p); err == nil {
			size += fi.Size()
		}
	}
	return Info{Version: i.Version, DataDir: i.DataDir, DBBytes: size}
}
