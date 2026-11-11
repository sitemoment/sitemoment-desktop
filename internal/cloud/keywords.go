// SPDX-License-Identifier: AGPL-3.0-or-later
package cloud

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// KeywordMetrics are one keyword's numbers from a SiteMoment data answer.
type KeywordMetrics struct {
	Volume, KD, CPC *float64
}

var (
	keywordKeys = []string{"keyword", "kw", "query", "term"}
	volumeKeys  = []string{"volume", "search_volume", "searchVolume", "avg_monthly_searches", "monthly_searches", "sv"}
	kdKeys      = []string{"kd", "gf_kd", "keyword_difficulty", "difficulty"}
	cpcKeys     = []string{"cpc", "cpc_usd"}
)

// ParseKeywordMetrics reads per-keyword volume, KD and CPC from a
// lookup_keywords (or keyword_volume) answer. web.cafe does not publish
// these shapes, so this is deliberately tolerant: it walks the JSON for
// objects that name a keyword and takes the first number under each
// metric's known names (a JSON number, a numeric string like "12,100", or
// an object with "value"/"score"). Rows marked not found (see
// isFoundFalse) are skipped. Keys are lower case with single spaces.
func ParseKeywordMetrics(data []byte) map[string]KeywordMetrics {
	out := map[string]KeywordMetrics{}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return out
	}
	walk(v, out)
	return out
}

func walk(v any, out map[string]KeywordMetrics) {
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			walk(e, out)
		}
	case map[string]any:
		kw := ""
		for _, k := range keywordKeys {
			if s, ok := x[k].(string); ok && strings.TrimSpace(s) != "" {
				kw = strings.ToLower(strings.Join(strings.Fields(s), " "))
				break
			}
		}
		if kw == "" {
			for _, e := range x {
				walk(e, out)
			}
			return
		}
		if fv, ok := x["found"]; ok && isFoundFalse(fv) {
			return
		}
		m := KeywordMetrics{Volume: firstNumber(x, volumeKeys), KD: firstNumber(x, kdKeys), CPC: firstNumber(x, cpcKeys)}
		if m.Volume == nil && m.KD == nil && m.CPC == nil {
			return
		}
		old := out[kw]
		if old.Volume == nil {
			old.Volume = m.Volume
		}
		if old.KD == nil {
			old.KD = m.KD
		}
		if old.CPC == nil {
			old.CPC = m.CPC
		}
		out[kw] = old
	}
}

// isFoundFalse reports whether a "found" field marks the row as not found:
// the boolean false, the string "false" (any case, surrounding space
// ignored), or the number 0.
func isFoundFalse(v any) bool {
	switch x := v.(type) {
	case bool:
		return !x
	case string:
		return strings.EqualFold(strings.TrimSpace(x), "false")
	case json.Number:
		f, err := x.Float64()
		return err == nil && f == 0
	}
	return false
}

func firstNumber(obj map[string]any, keys []string) *float64 {
	for _, k := range keys {
		if f, ok := number(obj[k]); ok {
			return &f
		}
	}
	return nil
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		s := strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(x), ",", ""), "$")
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil
	case map[string]any:
		for _, k := range []string{"value", "score"} {
			if f, ok := number(x[k]); ok {
				return f, true
			}
		}
	}
	return 0, false
}
