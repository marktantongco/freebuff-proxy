// Package lock provides a singleton process lock using flock(2).
//
// On Linux, flock locks are automatically released when the holding
// process exits — even on crash/kill — so there are no stale lock files.
//
// ## Kullanım örneği
//
// ```go
// lk, err := lock.New("/tmp/freebuff-proxy.lock")
// if err != nil {
//     return fmt.Errorf("another instance is already running: %w", err)
// }
// defer lk.Release()
// ```
package lock

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Lock represents an exclusive file lock held by this process.
// The lock is automatically released when the process exits.
type Lock struct {
	f *os.File
}

// New creates or opens the lock file at path and acquires an exclusive
// advisory lock (flock LOCK_EX + LOCK_NB). If another process holds the
// lock, it returns ErrAlreadyRunning.
func New(path string) (*Lock, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}

	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if err == unix.EWOULDBLOCK {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("flock: %w", err)
	}

	// Write our PID to the lock file for introspection.
	_, _ = f.Seek(0, 0)
	_ = f.Truncate(0)
	_, _ = f.WriteString(fmt.Sprintf("%d\n", os.Getpid()))
	_ = f.Sync()
	return &Lock{f: f}, nil
}

// Release closes the lock file, which releases the flock.
func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = l.f.Close()
	l.f = nil
}

// ErrAlreadyRunning is returned when another instance holds the lock.
var ErrAlreadyRunning = fmt.Errorf("another freebuff-proxy instance is already running on this host")
