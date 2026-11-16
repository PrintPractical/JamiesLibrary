// Package auth says who is making a request. The sessions code fills the
// caller in; until a request has one, it has no caller and every route that
// needs a permission answers 401.
package auth

import (
	"context"

	"jamieslibrary/internal/perm"
)

// CallerInfo is the signed-in user as loaded from the database for one request.
type CallerInfo struct {
	UserID             int64
	Permissions        perm.Set
	MustChangePassword bool
	// SessionID names the session the request came with (the hash of its
	// token, never the token), so "all but this one" can be said.
	SessionID string
}

// Has reports whether the caller holds p.
func (c *CallerInfo) Has(p perm.Permission) bool { return c != nil && c.Permissions.Has(p) }

type callerKey struct{}

// WithCaller returns a context that carries c.
func WithCaller(ctx context.Context, c *CallerInfo) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// Caller returns the signed-in user of the request, or nil when there is none.
func Caller(ctx context.Context) *CallerInfo {
	c, _ := ctx.Value(callerKey{}).(*CallerInfo)
	return c
}
