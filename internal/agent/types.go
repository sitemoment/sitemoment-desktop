// SPDX-License-Identifier: AGPL-3.0-or-later
package agent

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/sitemoment/sitemoment-desktop/internal/cloud"
)

// Spend modes: whether and how the agent may spend SiteMoment credits.
const (
	// ModeFreeOnly hides the paid tools.
	ModeFreeOnly = "free_only"
	// ModeConfirm pauses the turn before an uncached paid call until the user approves.
	ModeConfirm = "confirm"
	// ModeAuto spends without asking, up to autoTurnCap credits per turn.
	ModeAuto = "auto"
)

// autoTurnCap is what auto mode may spend in one turn without asking.
const autoTurnCap = 20

// ValidMode reports whether m is a spend mode.
func ValidMode(m string) bool { return m == ModeFreeOnly || m == ModeConfirm || m == ModeAuto }

// Tool kinds, for the UI.
const (
	KindLocal     = "local"     // local Search Console / GA4 / Bing / keyword data
	KindWeb       = "web"       // free web requests (crawl, PageSpeed, Wayback, autocomplete)
	KindCollector = "collector" // collectors in a hidden browser window
	KindCloud     = "cloud"     // SiteMoment paid data
)

// SessionView is a session for the UI.
type SessionView struct {
	ID        int64   `json:"id"`
	SiteID    int64   `json:"siteId"`
	Title     string  `json:"title"`
	SpendMode string  `json:"spendMode"`
	Status    string  `json:"status"` // idle | running | awaiting_confirm
	Credits   float64 `json:"credits"`
	UpdatedAt int64   `json:"updatedAt"`
}

// Card is what the UI renders for a tool call.
type Card struct {
	Type    string  `json:"type"` // table | lines
	Title   string  `json:"title,omitempty"`
	Columns []any   `json:"columns,omitempty"`
	Rows    [][]any `json:"rows,omitempty"`
	Items   []Line  `json:"items,omitempty"`
	Note    string  `json:"note,omitempty"`
}

// Line is one ✓/⚠/✗ item; S is ok, warn or fail.
type Line struct {
	S string `json:"s"`
	T string `json:"t"`
}

// ToolView is one tool call for the UI.
type ToolView struct {
	CallID  string  `json:"callId"`
	Tool    string  `json:"tool"`
	Label   string  `json:"label"`
	Kind    string  `json:"kind"`
	Status  string  `json:"status"` // running | ok | error | declined
	Cached  bool    `json:"cached"`
	Credits float64 `json:"credits"`
	Card    *Card   `json:"card,omitempty"`
	Error   string  `json:"error,omitempty"`
	Ms      int64   `json:"ms"`
}

// Item is one transcript entry for the UI.
type Item struct {
	Kind string    `json:"kind"` // user | answer | note | tool | error | stopped
	Turn int       `json:"turn"`
	Text string    `json:"text,omitempty"`
	Tool *ToolView `json:"tool,omitempty"`
	At   int64     `json:"at"`
}

// PendingCall is a paid call waiting for the user's approval.
type PendingCall struct {
	CallID     string  `json:"callId"`
	Tool       string  `json:"tool"`
	Label      string  `json:"label"`
	MaxCredits float64 `json:"maxCredits"`
}

// Pending is what the confirm bar shows.
type Pending struct {
	Calls    []PendingCall `json:"calls"`
	TotalMax float64       `json:"totalMax"`
}

// Transcript is a session as the UI shows it.
type Transcript struct {
	Session SessionView `json:"session"`
	Items   []Item      `json:"items"`
	Pending *Pending    `json:"pending"`
}

// CollectorTool is one collector offered to the model as a tool.
type CollectorTool struct {
	ID, Tool, Name, Description string
	Params                      json.RawMessage
}

// CollectorResult is a collector's answer.
type CollectorResult struct {
	Data      json.RawMessage
	Cached    bool
	FetchedAt int64
}

// Collectors runs collectors (internal/collect, adapted in main.go).
type Collectors interface {
	Tools() []CollectorTool
	Run(ctx context.Context, id string, input map[string]any) (CollectorResult, error)
}

// Collector errors the Collectors adapter wraps (with %w) so the agent can
// explain them.
var (
	// ErrCollectorLimit: the collector's daily limit is used up.
	ErrCollectorLimit = errors.New("agent: collector daily limit reached")
	// ErrCollectorHuman: the page asked for a human check that nobody solved
	// (or the user closed the window).
	ErrCollectorHuman = errors.New("agent: collector needs a human check")
)

// Cloud is SiteMoment's paid data API (cloud.Client).
type Cloud interface {
	SignedIn() bool
	Catalog(ctx context.Context) ([]cloud.Tool, error)
	Quote(ctx context.Context, tool string, params map[string]any) (cloud.Quote, error)
	Data(ctx context.Context, tool string, params map[string]any) (cloud.DataResult, error)
}
