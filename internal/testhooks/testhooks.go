// Package testhooks holds the seams tests need: a control endpoint that moves
// the clock, a replacement base URL for the book-lookup provider, and a
// migration that can be made to fail.
//
// It is only live in a build with -tags testhooks. In any other build every
// function here does nothing, so production code calls the same functions and
// a release ignores every JL_TEST_* variable.
package testhooks

// Environment variables a testhooks build reads.
const (
	EnvControlAddr   = "JL_TEST_CONTROL_ADDR"
	EnvMetadataURL   = "JL_TEST_METADATA_URL"
	EnvFailMigration = "JL_TEST_FAIL_MIGRATION"
	// EnvNow is the time the clock stands at from start-up, as RFC 3339; it
	// needs the control address, which moves the clock.
	EnvNow = "JL_TEST_NOW"
)

// Control is the running control listener. A nil *Control (what a release
// build returns) is safe to use.
type Control struct {
	impl *control
}

// Addr is the address the control listener is bound to, "" for a nil Control.
func (c *Control) Addr() string {
	if c == nil || c.impl == nil {
		return ""
	}
	return c.impl.ln.Addr().String()
}

// Close stops the control listener, puts the system clock back and clears
// every setting Start made. It waits for the listener to finish.
func (c *Control) Close() error {
	if c == nil || c.impl == nil {
		return nil
	}
	return c.impl.close()
}
