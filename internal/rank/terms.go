// SPDX-License-Identifier: AGPL-3.0-or-later

package rank

import (
	"strings"
	"unicode"
)

// stopWords are dropped from page terms: English function words plus words
// that appear on almost every page title.
var stopWords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`a about above after again against all also am an and any are as at be
		because been before being below between both but by can could did do does doing down during each
		few for from further get got had has have having he her here hers herself him himself his how i if
		in into is it its itself just let me more most my myself no nor not now of off on once only or other
		our ours ourselves out over own same she should so some such than that the their theirs them
		themselves then there these they this those through to too under until up us very was we were what
		when where which while who whom why will with would you your yours yourself yourselves
		home page welcome official site website online free best top new get vs via`) {
		m[w] = true
	}
	return m
}()

// PageTerms returns the significant lower-cased words of the page title and
// H1 headings, without stop words, numbers or one-letter words,
// deduplicated in order of first appearance.
func PageTerms(r *Report) []string {
	if r == nil {
		return []string{}
	}
	text := r.Page.Title + " " + strings.Join(r.Page.H1, " ")
	out := []string{}
	seen := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(c rune) bool {
		return !unicode.IsLetter(c) && !unicode.IsDigit(c)
	}) {
		if len([]rune(w)) < 2 || stopWords[w] || seen[w] || isDigits(w) {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

func isDigits(s string) bool {
	for _, c := range s {
		if !unicode.IsDigit(c) {
			return false
		}
	}
	return true
}
