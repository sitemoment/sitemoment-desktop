// SPDX-License-Identifier: AGPL-3.0-or-later

// Package webdata holds the free web data sources the agent uses: PageSpeed
// Insights, the Wayback Machine and Google autocomplete. It is copied from
// SiteMoment's backend (internal/providers, free parts only).
package webdata

import "fmt"

// ErrorKind classifies a provider failure.
type ErrorKind int

// Error kinds.
const (
	// KindRateLimited means back off for RetryAfterSecs.
	KindRateLimited ErrorKind = iota
	// KindOutOfCredits means the account has no balance left.
	KindOutOfCredits
	// KindHTTP is a transport or non-success HTTP status (transient).
	KindHTTP
	// KindDecode means the response could not be understood (permanent).
	KindDecode
)

// Error is a provider failure.
type Error struct {
	Kind           ErrorKind
	RetryAfterSecs uint64
	Msg            string
}

func (e *Error) Error() string {
	switch e.Kind {
	case KindRateLimited:
		return fmt.Sprintf("rate limited, retry after %ds", e.RetryAfterSecs)
	case KindOutOfCredits:
		return "out of credits"
	case KindHTTP:
		return "http error: " + e.Msg
	default:
		return "decode error: " + e.Msg
	}
}

// RateLimited builds a KindRateLimited error.
func RateLimited(secs uint64) *Error { return &Error{Kind: KindRateLimited, RetryAfterSecs: secs} }

// HTTPError builds a KindHTTP error.
func HTTPError(msg string) *Error { return &Error{Kind: KindHTTP, Msg: msg} }

// DecodeError builds a KindDecode error.
func DecodeError(msg string) *Error { return &Error{Kind: KindDecode, Msg: msg} }

const (
	// retryAfterCapSecs caps a server's Retry-After.
	retryAfterCapSecs = 3600
	// errorBodyMaxChars caps an upstream error body quoted in an error.
	errorBodyMaxChars = 500
)
