// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// FactQuery asks for one dimension of a site's local data over [From, To]
// (days, YYYY-MM-DD; Bing's query and page rows are weekly and match on the
// week's first day). Contains and Exclude filter the dimension's value
// (case-insensitive); OrderBy names a metric of the source (default: the
// day ascending for "date", else the first metric descending).
type FactQuery struct {
	SiteID   int64
	Dim      string
	From, To string
	Contains string
	Exclude  string
	OrderBy  string
	Asc      bool
	Limit    int // 1–500, default 50
}

// ErrBadQuery means a FactQuery names an unknown dimension or metric.
var ErrBadQuery = errors.New("store: unknown dimension or metric")

// GSCStat is one Search Console row: Position is impression-weighted.
type GSCStat struct {
	Key         string  `json:"key"`
	Clicks      float64 `json:"clicks"`
	Impressions float64 `json:"impressions"`
	CTR         float64 `json:"ctr"`
	Position    float64 `json:"position"`
}

// GAStat is one GA4 row (users are added up per day).
type GAStat struct {
	Key             string  `json:"key"`
	Sessions        float64 `json:"sessions"`
	Users           float64 `json:"users"`
	EngagedSessions float64 `json:"engagedSessions"`
	KeyEvents       float64 `json:"keyEvents"`
}

// BingStatRow is one Bing row; Position is 0 for daily traffic (Bing does
// not report it per day).
type BingStatRow struct {
	Key         string  `json:"key"`
	Clicks      float64 `json:"clicks"`
	Impressions float64 `json:"impressions"`
	Position    float64 `json:"position"`
}

// factSQL builds the shared tail of a fact query: filters on keyExpr,
// grouping, order and limit. orderCols maps metric names to SQL expressions
// over the result columns; defaultOrder is used for non-date dimensions.
func factSQL(q FactQuery, keyExpr string, orderCols map[string]string, defaultOrder string) (string, []any, error) {
	var sb strings.Builder
	var args []any
	if q.Contains != "" {
		sb.WriteString(" AND instr(lower(" + keyExpr + "), lower(?)) > 0")
		args = append(args, q.Contains)
	}
	if q.Exclude != "" {
		sb.WriteString(" AND instr(lower(" + keyExpr + "), lower(?)) = 0")
		args = append(args, q.Exclude)
	}
	sb.WriteString(" GROUP BY " + keyExpr)
	order, dir := "k", "ASC"
	if q.OrderBy != "" || q.Dim != "date" {
		name := q.OrderBy
		if name == "" {
			name = defaultOrder
		}
		col, ok := orderCols[name]
		if !ok {
			return "", nil, fmt.Errorf("%w: %s", ErrBadQuery, name)
		}
		order = col
		if !q.Asc {
			dir = "DESC"
		}
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 500)
	sb.WriteString(fmt.Sprintf(" ORDER BY %s %s, k LIMIT %d", order, dir, limit))
	return sb.String(), args, nil
}

var gscKeys = map[string]string{"date": "day", "query": "query", "page": "page"}

// GSCQuery sums the Search Console slice q.Dim (date, query or page).
func (s *Store) GSCQuery(ctx context.Context, q FactQuery) ([]GSCStat, error) {
	key, ok := gscKeys[q.Dim]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrBadQuery, q.Dim)
	}
	tail, extra, err := factSQL(q, key, map[string]string{
		"clicks": "c", "impressions": "i", "ctr": "c / NULLIF(i, 0)", "position": "p",
	}, "clicks")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+key+" AS k, SUM(clicks) AS c, SUM(impressions) AS i, "+
		"COALESCE(SUM(position * impressions) / NULLIF(SUM(impressions), 0), 0) AS p "+
		"FROM gsc_facts WHERE site_id = ? AND slice = ? AND day BETWEEN ? AND ?"+tail,
		append([]any{q.SiteID, q.Dim, q.From, q.To}, extra...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GSCStat{}
	for rows.Next() {
		var r GSCStat
		if err := rows.Scan(&r.Key, &r.Clicks, &r.Impressions, &r.Position); err != nil {
			return nil, err
		}
		if r.Impressions > 0 {
			r.CTR = r.Clicks / r.Impressions
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

var gaKeys = map[string]string{"date": "day", "landing": "dim", "channel": "dim"}

// GAQuery sums the GA4 slice q.Dim (date, landing or channel).
func (s *Store) GAQuery(ctx context.Context, q FactQuery) ([]GAStat, error) {
	key, ok := gaKeys[q.Dim]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrBadQuery, q.Dim)
	}
	tail, extra, err := factSQL(q, key, map[string]string{
		"sessions": "se", "users": "u", "engagedSessions": "e", "keyEvents": "ke",
	}, "sessions")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+key+" AS k, SUM(sessions) AS se, SUM(users) AS u, SUM(engaged_sessions) AS e, SUM(key_events) AS ke "+
		"FROM ga_facts WHERE site_id = ? AND slice = ? AND day BETWEEN ? AND ?"+tail,
		append([]any{q.SiteID, q.Dim, q.From, q.To}, extra...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GAStat{}
	for rows.Next() {
		var r GAStat
		if err := rows.Scan(&r.Key, &r.Sessions, &r.Users, &r.EngagedSessions, &r.KeyEvents); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// BingQuery sums Bing's daily traffic (Dim "date") or weekly query or page
// rows over [From, To].
func (s *Store) BingQuery(ctx context.Context, q FactQuery) ([]BingStatRow, error) {
	var from, key, period string
	switch q.Dim {
	case "date":
		from, key, period = "bing_daily", "day", "day"
	case "query":
		from, key, period = "bing_queries", "query", "week"
	case "page":
		from, key, period = "bing_pages", "page", "week"
	default:
		return nil, fmt.Errorf("%w: %s", ErrBadQuery, q.Dim)
	}
	pos := "0"
	if q.Dim != "date" {
		pos = "COALESCE(SUM(position * impressions) / NULLIF(SUM(impressions), 0), 0)"
	}
	tail, extra, err := factSQL(q, key, map[string]string{"clicks": "c", "impressions": "i", "position": "p"}, "clicks")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+key+" AS k, SUM(clicks) AS c, SUM(impressions) AS i, "+pos+" AS p FROM "+from+
		" WHERE site_id = ? AND "+period+" BETWEEN ? AND ?"+tail,
		append([]any{q.SiteID, q.From, q.To}, extra...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BingStatRow{}
	for rows.Next() {
		var r BingStatRow
		if err := rows.Scan(&r.Key, &r.Clicks, &r.Impressions, &r.Position); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
