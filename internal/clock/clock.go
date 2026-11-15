// Package clock is the one source of "now". Everything in the server that
// needs the time asks here, so a test build can move it (see testhooks).
// Production uses the system clock.
package clock

import (
	"sync/atomic"
	"time"
)

var source atomic.Pointer[func() time.Time]

// Now is the current time from the installed source, the system clock by default.
func Now() time.Time {
	if f := source.Load(); f != nil {
		return (*f)()
	}
	return time.Now()
}

// SetSource installs the function Now answers from, and returns a function
// that puts back the source that was there. A nil f means the system clock.
func SetSource(f func() time.Time) (restore func()) {
	var next *func() time.Time
	if f != nil {
		next = &f
	}
	prev := source.Swap(next)
	return func() { source.Store(prev) }
}
