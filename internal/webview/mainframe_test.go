// SPDX-License-Identifier: AGPL-3.0-or-later
package webview

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestMainFrameSameOrigin(t *testing.T) {
	cases := []struct {
		name string
		o    *application.OriginInfo
		want bool
	}{
		{"nil", nil, false},
		{"both empty (Linux's normal top-level report)", &application.OriginInfo{}, true},
		{"origin equals top origin (Windows top-level)", &application.OriginInfo{Origin: "https://www.google.com", TopOrigin: "https://www.google.com"}, true},
		{"origin differs from top origin (an iframe)", &application.OriginInfo{Origin: "https://ads.example/", TopOrigin: "https://www.google.com"}, false},
		{"origin set, top origin empty", &application.OriginInfo{Origin: "https://www.google.com"}, false},
		{"origin empty, top origin set", &application.OriginInfo{TopOrigin: "https://www.google.com"}, false},
	}
	for _, c := range cases {
		if got := mainFrameSameOrigin(c.o); got != c.want {
			t.Errorf("%s: mainFrameSameOrigin(%+v) = %v, want %v", c.name, c.o, got, c.want)
		}
	}
}
