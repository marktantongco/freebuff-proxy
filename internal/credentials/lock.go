package credentials

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// CredentialLockPath returns the path to the flock lock file that guards
// the credentials file at credsPath. The lock file sits alongside the
// credentials file with a ".lock" suffix.
//
// Example:
//
//	credsPath = "/home/user/.config/manicode/credentials.json"
//	→ lockPath = "/home/user/.config/manicode/credentials.json.lock"
func CredentialLockPath(credsPath string) string {
	return credsPath + ".lock"
}

// AcquireWriteLock opens (or creates) the lock file for the credentials
// file at credsPath and acquires an exclusive advisory flock (LOCK_EX).
// Returns the open file, which the caller must close to release the lock.
//
// If another process already holds the lock, this blocks until the lock
// is released.
func AcquireWriteLock(credsPath string) (*os.File, error) {
	lockPath := CredentialLockPath(credsPath)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open credential lock file: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("flock credential lock (EX): %w", err)
	}
	return f, nil
}

// AcquireReadLock opens the lock file and acquires a shared advisory flock
// (LOCK_SH). Multiple readers can hold the lock simultaneously, but no
// writer can write while any reader holds the lock.
func AcquireReadLock(credsPath string) (*os.File, error) {
	lockPath := CredentialLockPath(credsPath)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open credential lock file: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_SH); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("flock credential lock (SH): %w", err)
	}
	return f, nil
}

// ReleaseLock closes the lock file, which releases the flock (both
// exclusive and shared locks are released on close). Safe to call with
// a nil file.
func ReleaseLock(f *os.File) {
	if f == nil {
		return
	}
	_ = f.Close()
}
