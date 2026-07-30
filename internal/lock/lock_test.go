package lock

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestLockAcquireAndRelease verifies basic acquire and release lifecycle.
func TestLockAcquireAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.lock")

	lk, err := New(path)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	if lk == nil {
		t.Fatal("New() returned nil lock")
	}

	// Release should not error.
	lk.Release()
}

// TestLockDoubleAcquireRejectsSecondInstance verifies that a second
// New() on the same path returns ErrAlreadyRunning while the first
// lock is held.
func TestLockDoubleAcquireRejectsSecondInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "double.lock")

	lk1, err := New(path)
	if err != nil {
		t.Fatalf("first New() = %v", err)
	}
	defer lk1.Release()

	_, err = New(path)
	if err != ErrAlreadyRunning {
		t.Fatalf("second New() = %v, want ErrAlreadyRunning", err)
	}
}

// TestLockAcquireAfterRelease verifies that acquiring the lock again
// after releasing works.
func TestLockAcquireAfterRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reacquire.lock")

	lk1, err := New(path)
	if err != nil {
		t.Fatalf("first New() = %v", err)
	}
	lk1.Release()

	lk2, err := New(path)
	if err != nil {
		t.Fatalf("second New() after release = %v", err)
	}
	lk2.Release()
}

// TestLockNilReleaseDoesNotPanic verifies that Release on a nil Lock
// or a Lock with a nil file is safe.
func TestLockNilReleaseDoesNotPanic(t *testing.T) {
	var nilLock *Lock
	nilLock.Release() // should not panic

	emptyLock := &Lock{f: nil}
	emptyLock.Release() // should not panic
}

// TestLockPIDWritten verifies that the lock file contains the current
// process PID after acquisition.
func TestLockPIDWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pid.lock")

	lk, err := New(path)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	defer lk.Release()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() = %v", err)
	}

	expected := os.Getpid()
	var got int
	if _, err := fmt.Sscanf(string(data), "%d", &got); err != nil {
		t.Fatalf("parse PID from %q: %v", string(data), err)
	}
	if got != expected {
		t.Errorf("PID = %d, want %d", got, expected)
	}
}

// TestLockFileCleanedOnRelease verifies the lock file is properly
// handle-cleaned (flock released) when the file is closed.
func TestLockFileCleanedOnRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cleanup.lock")

	lk, err := New(path)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	lk.Release()

	// After release, a new lock should succeed.
	second, err := New(path)
	if err != nil {
		t.Fatalf("New() after release = %v", err)
	}
	second.Release()
}
