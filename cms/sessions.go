package cms

import (
	"context"
	"errors"
	"time"
)

var ErrSessionNotFound = errors.New("session not found")

type BrowserSession struct {
	Authenticated bool
	Expires       time.Time
}

// Only digests of opaque cookie values cross this boundary. Implementations
// atomically replace pre-login sessions and enforce a bounded scope capacity.
type BrowserSessionStore interface {
	CheckBrowserSessions(context.Context) error
	LoadBrowserSession(context.Context, string, string, time.Time) (BrowserSession, error)
	SaveBrowserSession(context.Context, string, string, string, BrowserSession, time.Time) error
	DeleteBrowserSession(context.Context, string, string) error
}
