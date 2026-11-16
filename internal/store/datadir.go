package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const (
	dirMode  = 0o700
	fileMode = 0o600
)

// ErrLocked means another process already holds the data directory.
var ErrLocked = errors.New("data directory is in use by another instance")

// prepareDir creates dir if it is missing, tightens it to 0700 and proves it
// can be written to. Every error names the path.
func prepareDir(dir string) error {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("cannot use the data directory %q: %w", dir, err)
	}
	if err := os.Chmod(dir, dirMode); err != nil {
		return fmt.Errorf("cannot restrict the data directory %q to its owner (0700): %w", dir, err)
	}
	f, err := os.CreateTemp(dir, ".writable-*")
	if err != nil {
		return fmt.Errorf("the data directory %q is not writable: %w", dir, err)
	}
	name := f.Name()
	_ = f.Close()
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("the data directory %q is not writable: %w", dir, err)
	}
	return nil
}

// mkdirPrivate makes a sub-directory of the data directory, 0700 even if the
// umask or an earlier version made it looser.
func mkdirPrivate(path string) error {
	if err := os.MkdirAll(path, dirMode); err != nil {
		return fmt.Errorf("cannot create %q: %w", path, err)
	}
	if err := os.Chmod(path, dirMode); err != nil {
		return fmt.Errorf("cannot restrict %q to its owner (0700): %w", path, err)
	}
	return nil
}

// lockDir takes an exclusive, non-blocking lock on dir/lock. The lock lives as
// long as the returned file stays open, and the operating system drops it if
// the process dies.
func lockDir(dir string) (*os.File, error) {
	path := filepath.Join(dir, "lock")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, fileMode)
	if err != nil {
		return nil, fmt.Errorf("cannot open the lock file %q: %w", path, err)
	}
	if err := f.Chmod(fileMode); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("cannot restrict %q to its owner (0600): %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another Jamie's Library is already using the data directory %q: stop it first, or give this one its own directory: %w", dir, ErrLocked)
		}
		return nil, fmt.Errorf("cannot lock %q: %w", path, err)
	}
	return f, nil
}
