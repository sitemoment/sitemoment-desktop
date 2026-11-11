// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"reflect"
	"testing"
)

func TestPageTerms(t *testing.T) {
	cases := []struct {
		title string
		h1    []string
		want  []string
	}{
		{"Sourdough Bread at Home: A Beginner's Guide | Bakes", []string{"Sourdough bread for beginners"},
			[]string{"sourdough", "bread", "beginner", "guide", "bakes", "beginners"}},
		{"Welcome to the Home Page", nil, []string{}},
		{"Top 10 AI Tools in 2026", []string{"AI tools"}, []string{"ai", "tools"}},
		{"", nil, []string{}},
	}
	for _, c := range cases {
		got := PageTerms(&Report{Page: PageInfo{Title: c.title, H1: c.h1}})
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("PageTerms(%q, %v) = %v, want %v", c.title, c.h1, got, c.want)
		}
	}
	if got := PageTerms(nil); got == nil || len(got) != 0 {
		t.Errorf("nil report: %v", got)
	}
}
