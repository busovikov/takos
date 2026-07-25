package fsstore

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Lock is an advisory exclusive file lock (flock). It guarantees a single
// daemon per workspace: the lock dies with the process, so a crash never
// leaves the workspace locked.
type Lock struct {
	f *os.File
}

// TryLock acquires path exclusively without blocking. A second caller gets an
// error immediately — that is the "daemon already running" signal.
func TryLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock %s is held by another process: %w", path, err)
	}
	return &Lock{f: f}, nil
}

// Unlock releases the lock. The lock file itself is left in place.
func (l *Lock) Unlock() error {
	if err := unix.Flock(int(l.f.Fd()), unix.LOCK_UN); err != nil {
		l.f.Close()
		return err
	}
	return l.f.Close()
}
