//go:build !testhooks

package testhooks

import "net"

// Enabled says whether this build has the test hooks.
const Enabled = false

// control has nothing in it: a release build never makes one.
type control struct {
	ln interface{ Addr() net.Addr }
}

func (c *control) close() error { return nil }

// Start does nothing in a release build: the environment is not even read.
func Start(getenv func(string) string) (*Control, error) { return nil, nil }

// MetadataURL is "" in a release build: the real provider is always used.
func MetadataURL() string { return "" }

// FailMigration is nil in a release build: no migration is made to fail.
func FailMigration(version int) error { return nil }
