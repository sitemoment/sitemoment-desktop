// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/bing"
	"github.com/sitemoment/sitemoment-desktop/internal/cloud"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// Keyword data settings for M2: the US market in English.
const (
	keywordCountry    = "us"
	keywordLanguage   = "en-US"
	cloudKeywordTool  = "lookup_keywords"
	cloudBatchSize    = 100 // lookup_keywords takes at most 100 keywords
	bingWeeks         = 4   // Bing volume = exact-match searches of the last 4 weeks
	foundLimit        = 200 // "found" keywords: top Search Console queries by impressions
	keywordWindowDays = 28
	maxChosen         = 500
)

// ErrNoBingKey means there is no saved Bing key for the free keyword stats.
var ErrNoBingKey = errors.New("还没有添加 Bing API key（设置 → Bing Webmaster）")

// CloudData is SiteMoment's paid data API (cloud.Client).
type CloudData interface {
	Quote(ctx context.Context, tool string, params map[string]any) (cloud.Quote, error)
	Data(ctx context.Context, tool string, params map[string]any) (cloud.DataResult, error)
}

// BingKeywordStats is Bing's free keyword history (bing.Client).
type BingKeywordStats interface {
	KeywordStats(ctx context.Context, key, q, country, language string) ([]bing.KeywordWeek, error)
}

// KeywordService serves the keyword workspace.
type KeywordService struct {
	Store    *store.Store
	Cloud    CloudData
	Bing     BingKeywordStats
	BingKeys interface {
		BingKey(account string) (string, error)
	}
	Now     func() time.Time // default time.Now
	BingGap time.Duration    // pause between Bing keyword calls; 0: 300ms
}

// KeywordList is the keywords page: Search Console numbers cover [From, To],
// the 28 days ending at the newest synced day (both empty before a sync).
type KeywordList struct {
	From     string          `json:"from"`
	To       string          `json:"to"`
	Keywords []store.Keyword `json:"keywords"`
}

// FetchResult says what a fetch did.
type FetchResult struct {
	Updated int     `json:"updated"`
	Skipped int     `json:"skipped"` // no data, or a better source already set
	Credits float64 `json:"credits"`
	Cached  bool    `json:"cached"` // every SiteMoment batch came from the cache
	Source  string  `json:"source"` // "bing" or "sitemoment"
}

// CloudQuote is the most a SiteMoment fetch for the chosen keywords costs.
type CloudQuote struct {
	Tool       string  `json:"tool"`
	Label      string  `json:"label"`
	Keywords   int     `json:"keywords"`
	MaxCredits float64 `json:"maxCredits"`
	Cached     bool    `json:"cached"`
}

func (k *KeywordService) now() time.Time {
	if k.Now != nil {
		return k.Now()
	}
	return time.Now()
}

// window is the 28 days ending at the newest synced Search Console query day.
func (k *KeywordService) window(ctx context.Context, siteID int64) (string, string, error) {
	latest, err := k.Store.LatestDay(ctx, siteID, "gsc", "query")
	if err != nil || latest == "" {
		return "", "", err
	}
	to, err := time.Parse(store.DayLayout, latest)
	if err != nil {
		return "", "", err
	}
	return to.AddDate(0, 0, -(keywordWindowDays - 1)).Format(store.DayLayout), latest, nil
}

// List adds the newest "found" keywords and returns the site's keywords of
// kind ("" = all) with their Search Console numbers.
func (k *KeywordService) List(ctx context.Context, siteID int64, kind string) (KeywordList, error) {
	from, to, err := k.window(ctx, siteID)
	if err != nil {
		return KeywordList{}, err
	}
	if to != "" {
		if _, err := k.Store.AddFoundKeywords(ctx, siteID, from, to, foundLimit, k.now().Unix()); err != nil {
			return KeywordList{}, err
		}
	}
	list, err := k.Store.Keywords(ctx, siteID, kind, from, to)
	return KeywordList{From: from, To: to, Keywords: list}, err
}

// Add adds keywords (one per line, or separated by commas) as kind and
// returns how many it added.
func (k *KeywordService) Add(ctx context.Context, siteID int64, text, kind string) (int, error) {
	n := 0
	for _, part := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == ',' || r == '，' }) {
		if strings.TrimSpace(part) == "" {
			continue
		}
		if _, err := k.Store.AddKeyword(ctx, siteID, part, kind, k.now().Unix()); err != nil {
			return n, err
		}
		n++
	}
	if n == 0 {
		return 0, store.ErrBadKeyword
	}
	return n, nil
}

// Update sets a keyword's kind and notes.
func (k *KeywordService) Update(ctx context.Context, id int64, kind, notes string) error {
	return k.Store.UpdateKeyword(ctx, id, kind, notes)
}

// Remove deletes a keyword.
func (k *KeywordService) Remove(ctx context.Context, id int64) error {
	return k.Store.DeleteKeyword(ctx, id)
}

// chosen is the site's keywords among ids (others are dropped).
func (k *KeywordService) chosen(ctx context.Context, siteID int64, ids []int64) ([]store.Keyword, error) {
	if len(ids) == 0 {
		return nil, errors.New("先选中要取数的关键词")
	}
	if len(ids) > maxChosen {
		return nil, fmt.Errorf("一次最多取 %d 个关键词", maxChosen)
	}
	seen := map[int64]bool{}
	out := []store.Keyword{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		kw, err := k.Store.Keyword(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if kw.SiteID == siteID {
			out = append(out, kw)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("选中的关键词不属于这个站点")
	}
	return out, nil
}

// bingKey is the site's bound Bing key, else the first saved one.
func (k *KeywordService) bingKey(ctx context.Context, siteID int64) (string, error) {
	site, err := k.Store.Site(ctx, siteID)
	if err != nil {
		return "", err
	}
	account := site.BingAccount
	if account == "" {
		conns, err := k.Store.Connections(ctx, "bing")
		if err != nil {
			return "", err
		}
		if len(conns) == 0 {
			return "", ErrNoBingKey
		}
		account = conns[0].Account
	}
	return k.BingKeys.BingKey(account)
}

// FetchBing sets volume from Bing's free keyword stats (exact-match searches
// of the last 4 weeks, US English). It never replaces SiteMoment numbers.
func (k *KeywordService) FetchBing(ctx context.Context, siteID int64, ids []int64) (FetchResult, error) {
	res := FetchResult{Source: "bing"}
	kws, err := k.chosen(ctx, siteID, ids)
	if err != nil {
		return res, err
	}
	key, err := k.bingKey(ctx, siteID)
	if err != nil {
		return res, err
	}
	gap := k.BingGap
	if gap <= 0 {
		gap = 300 * time.Millisecond
	}
	for i, kw := range kws {
		if i > 0 {
			select {
			case <-ctx.Done():
				return res, ctx.Err()
			case <-time.After(gap):
			}
		}
		weeks, err := k.Bing.KeywordStats(ctx, key, kw.Keyword, keywordCountry, keywordLanguage)
		if err != nil {
			return res, err
		}
		if len(weeks) == 0 {
			res.Skipped++
			continue
		}
		if len(weeks) > bingWeeks {
			weeks = weeks[len(weeks)-bingWeeks:]
		}
		sum := 0.0
		for _, w := range weeks {
			sum += w.Impressions
		}
		wrote, err := k.Store.SetKeywordMetric(ctx, kw.ID, "volume", sum, "bing", k.now().Unix())
		if err != nil {
			return res, err
		}
		if wrote {
			res.Updated++
		} else {
			res.Skipped++
		}
	}
	return res, nil
}

func cloudBatches(kws []store.Keyword) [][]store.Keyword {
	var out [][]store.Keyword
	for len(kws) > 0 {
		n := min(cloudBatchSize, len(kws))
		out = append(out, kws[:n])
		kws = kws[n:]
	}
	return out
}

func cloudParams(batch []store.Keyword) map[string]any {
	words := make([]string, len(batch))
	for i, kw := range batch {
		words[i] = kw.Keyword
	}
	return map[string]any{"keywords": words, "gl": keywordCountry}
}

func fmtCredits(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// cloudMessage turns cloud errors into what the keywords page shows.
func cloudMessage(err error) error {
	var ce *cloud.CreditsError
	switch {
	case errors.As(err, &ce):
		return fmt.Errorf("本月 SiteMoment 额度不够：这次最多要 %s 积分，已用 %s / %s", fmtCredits(ce.Need), fmtCredits(ce.Used), fmtCredits(ce.Limit))
	case errors.Is(err, cloud.ErrSignedOut):
		return errors.New("请先登录 SiteMoment（左下角）")
	case errors.Is(err, cloud.ErrDataUnavailable):
		return errors.New("SiteMoment 数据暂时不可用，请稍后再试")
	}
	return err
}

// QuoteCloud prices a SiteMoment fetch of volume, KD and CPC for the chosen
// keywords, without spending anything.
func (k *KeywordService) QuoteCloud(ctx context.Context, siteID int64, ids []int64) (CloudQuote, error) {
	kws, err := k.chosen(ctx, siteID, ids)
	if err != nil {
		return CloudQuote{}, err
	}
	out := CloudQuote{Tool: cloudKeywordTool, Keywords: len(kws), Cached: true}
	for _, b := range cloudBatches(kws) {
		q, err := k.Cloud.Quote(ctx, cloudKeywordTool, cloudParams(b))
		if err != nil {
			return CloudQuote{}, cloudMessage(err)
		}
		out.Label = q.Label
		out.MaxCredits += q.MaxCredits
		out.Cached = out.Cached && q.Cached
	}
	return out, nil
}

// FetchCloud buys volume, KD and CPC for the chosen keywords from SiteMoment
// (cached answers are free). Keywords the answer does not cover are skipped.
func (k *KeywordService) FetchCloud(ctx context.Context, siteID int64, ids []int64) (FetchResult, error) {
	res := FetchResult{Source: "sitemoment", Cached: true}
	kws, err := k.chosen(ctx, siteID, ids)
	if err != nil {
		return res, err
	}
	for _, b := range cloudBatches(kws) {
		r, err := k.Cloud.Data(ctx, cloudKeywordTool, cloudParams(b))
		if err != nil {
			return res, cloudMessage(err)
		}
		res.Credits += r.Credits
		res.Cached = res.Cached && r.Cached
		at := r.FetchedAt
		if at == 0 {
			at = k.now().Unix()
		}
		metrics := cloud.ParseKeywordMetrics(r.Data)
		for _, kw := range b {
			m, ok := metrics[kw.Keyword]
			if !ok {
				res.Skipped++
				continue
			}
			for _, f := range []struct {
				name string
				v    *float64
			}{{"volume", m.Volume}, {"kd", m.KD}, {"cpc", m.CPC}} {
				if f.v == nil {
					continue
				}
				if _, err := k.Store.SetKeywordMetric(ctx, kw.ID, f.name, *f.v, "sitemoment", at); err != nil {
					return res, err
				}
			}
			res.Updated++
		}
	}
	return res, nil
}
