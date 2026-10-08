package searxng

import (
	"fmt"
	"log/slog"
)

// Kind classifies upstream failures.
type Kind int

const (
	KindUnreachable Kind = iota + 1
	KindRejected
	KindNotFound
	KindRateLimited
	KindUnavailable
	KindBadResponse
	KindBadRequest
	KindRedirect
	KindCanceled
)

// Error is returned by Client.Search for every failure.
type Error struct {
	Kind   Kind
	Status int // HTTP status, 0 if no response was received
	Err    error
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("searxng: %s (HTTP %d): %v", e.Kind, e.Status, e.Err)
	}
	return fmt.Sprintf("searxng: %s: %v", e.Kind, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Message is a user-facing explanation suitable for an LLM. It contains no
// configuration details or secrets.
func (e *Error) Message() string {
	switch e.Kind {
	case KindRejected:
		return "The search service rejected the request (API key invalid or missing, or JSON output disabled). Searching is not possible right now."
	case KindNotFound:
		return "The search endpoint was not found (check the SearXNG URL and that format=json is allowed by the proxy). Searching is not possible right now."
	case KindRateLimited:
		return "The search service is busy (rate limit reached). Please try again later."
	case KindUnavailable:
		return "The search service is temporarily unavailable. Please try again later."
	case KindBadResponse:
		return "The search service returned an unexpected response."
	case KindBadRequest:
		return "The search service rejected the query as invalid. Try simpler search terms or other parameters."
	case KindRedirect:
		return "The search service answered with a redirect (check the SearXNG URL, e.g. http vs. https). Searching is not possible right now."
	case KindCanceled:
		return "The search was canceled."
	default:
		return "The search service is unreachable. Please try again later."
	}
}

// Level is the log level an operator should see this failure at:
// configuration problems are errors, transient ones are warnings.
func (e *Error) Level() slog.Level {
	switch e.Kind {
	case KindRejected, KindNotFound, KindBadResponse, KindRedirect:
		return slog.LevelError
	case KindCanceled:
		return slog.LevelInfo
	default:
		return slog.LevelWarn
	}
}

func (k Kind) String() string {
	switch k {
	case KindUnreachable:
		return "unreachable"
	case KindRejected:
		return "rejected"
	case KindNotFound:
		return "not found"
	case KindRateLimited:
		return "rate limited"
	case KindUnavailable:
		return "unavailable"
	case KindBadResponse:
		return "invalid response"
	case KindBadRequest:
		return "bad request"
	case KindRedirect:
		return "redirect"
	case KindCanceled:
		return "canceled"
	default:
		return "unknown"
	}
}
