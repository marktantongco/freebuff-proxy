package credentials

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ferdiunal/freebuff-proxy/internal/lock"
)

// testCredential is a reusable test credential fixture.
var testCred = Credential{
	ID:              "user_123",
	Name:            "Ada Lovelace",
	Email:           "ada@example.com",
	AuthToken:       "42d7350000000000000000000000a223",
	FingerprintID:   "fp_123",
	FingerprintHash: "hash_456",
}

// testCredB is a different credential for concurrent write tests.
var testCredB = Credential{
	ID:              "user_456",
	Name:            "Charles Babbage",
	Email:           "charles@example.com",
	AuthToken:       "99d7350000000000000000000000b789",
	FingerprintID:   "fp_456",
	FingerprintHash: "hash_789",
}

// ── Concurrent Save ────────────────────────────────────────────────────

// TestConcurrentSave verifies that two concurrent Save calls are serialized
// by the exclusive write lock. After both complete, the file content must
// exactly match one of the two credentials (no corruption).
func TestConcurrentSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	store := FileStore{Path: path}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		if err := store.Save(context.Background(), testCred); err != nil {
			t.Errorf("Save(testCred) = %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := store.Save(context.Background(), testCredB); err != nil {
			t.Errorf("Save(testCredB) = %v", err)
		}
	}()

	wg.Wait()

	// Verify the file exists and contains valid JSON with a token.
	loaded, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load after concurrent Save = %v", err)
	}
	if loaded.AuthToken == "" {
		t.Fatal("loaded credential has empty AuthToken")
	}
	// Must exactly match one of the two, not a mix.
	if loaded.AuthToken != testCred.AuthToken && loaded.AuthToken != testCredB.AuthToken {
		t.Errorf("loaded AuthToken = %q, expected either %q or %q",
			loaded.AuthToken, testCred.AuthToken, testCredB.AuthToken)
	}
}

// TestConcurrentSaveStress runs 20 concurrent saves to stress-test the lock.
func TestConcurrentSaveStress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stress.json")
	store := FileStore{Path: path}

	var wg sync.WaitGroup
	const count = 20

	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cred := testCred
			cred.AuthToken = testCred.AuthToken[:len(testCred.AuthToken)-1] + string(rune('0'+i%10))
			if err := store.Save(context.Background(), cred); err != nil {
				t.Errorf("Save #%d = %v", i, err)
			}
		}(i)
	}

	wg.Wait()

	// Final read must succeed without corruption.
	loaded, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load after %d concurrent Save = %v", count, err)
	}
	if loaded.AuthToken == "" {
		t.Fatal("loaded credential has empty AuthToken")
	}
}

// ── Save blocks Clear ──────────────────────────────────────────────────

// TestSaveBlocksClear verifies that holding an exclusive write lock (via Save)
// prevents Clear from proceeding until the lock is released.
func TestSaveBlocksClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "block_clear.json")
	store := FileStore{Path: path}

	// First, save something so Clear has something to remove.
	if err := store.Save(context.Background(), testCred); err != nil {
		t.Fatalf("initial Save = %v", err)
	}

	// Acquire the exclusive lock directly, which will block Clear.
	// Note: no defer — we release manually below to unblock the Clear goroutine.
	lockFile, err := AcquireWriteLock(path)
	if err != nil {
		t.Fatalf("AcquireWriteLock = %v", err)
	}

	// Try Clear in a goroutine — it should block because we hold the lock.
	clearDone := make(chan error, 1)
	go func() {
		clearDone <- store.Clear(context.Background())
	}()

	// Wait a short time — Clear should be blocked (not yet completed).
	select {
	case err := <-clearDone:
		t.Fatalf("Clear completed while lock held: %v", err)
	case <-time.After(100 * time.Millisecond):
		// Expected: Clear is blocked on the lock.
	}

	// Release the lock — Clear should now proceed.
	ReleaseLock(lockFile)

	select {
	case err := <-clearDone:
		if err != nil {
			t.Fatalf("Clear after lock released = %v", err)
		}
		// Clear succeeded — file should be gone.
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatal("Clear did not remove the file")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Clear did not complete within 5s after lock release")
	}
}

// ── Shared Load doesn't block other Load ───────────────────────────────

// TestLoadWithSharedLockDoesNotBlockOtherLoad verifies that multiple
// concurrent Load calls with shared (LOCK_SH) locks do not block each other.
func TestLoadWithSharedLockDoesNotBlockOtherLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared_load.json")
	store := FileStore{Path: path}

	// Save initial data.
	if err := store.Save(context.Background(), testCred); err != nil {
		t.Fatalf("initial Save = %v", err)
	}

	// Launch 5 concurrent Loads — all should complete quickly.
	var wg sync.WaitGroup
	const readers = 5
	start := make(chan struct{}) // release all at once

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // wait for signal
			_, err := store.Load(context.Background())
			if err != nil {
				t.Errorf("Load #%d = %v", i, err)
			}
		}(i)
	}

	// Signal all to start and measure completion time.
	close(start)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All Loads completed — shared lock didn't block.
	case <-time.After(5 * time.Second):
		t.Fatalf("%d concurrent Loads did not complete within 5s (shared lock blocking?)", readers)
	}
}

// TestAcquireReadLockDoesNotBlockOtherReadLock verifies at the lock level
// that multiple LOCK_SH acquisitions on the same file succeed concurrently.
func TestAcquireReadLockDoesNotBlockOtherReadLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "readlock_test.json")

	// First read lock should succeed.
	lk1, err := AcquireReadLock(path)
	if err != nil {
		t.Fatalf("first AcquireReadLock = %v", err)
	}
	defer ReleaseLock(lk1)

	// Second read lock should also succeed (LOCK_SH is shared).
	lk2, err := AcquireReadLock(path)
	if err != nil {
		t.Fatalf("second AcquireReadLock blocked by first = %v", err)
	}
	defer ReleaseLock(lk2)

	// Both held simultaneously — shared lock works.
}

// TestWriteLockBlocksReadLock verifies that an exclusive write lock
// prevents acquiring a shared read lock.
func TestWriteLockBlocksReadLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "write_block_read.json")

	// Acquire write lock first.
	// Note: no defer — we release manually below to unblock the read lock goroutine.
	writeLock, err := AcquireWriteLock(path)
	if err != nil {
		t.Fatalf("AcquireWriteLock = %v", err)
	}

	// Try to acquire a read lock — should block.
	readDone := make(chan error, 1)
	go func() {
		_, err := AcquireReadLock(path)
		readDone <- err
	}()

	select {
	case <-readDone:
		t.Fatal("AcquireReadLock completed while write lock held — lock not blocking!")
	case <-time.After(100 * time.Millisecond):
		// Expected: read lock is blocked by write lock.
	}

	// Release write lock — read lock should proceed.
	ReleaseLock(writeLock)

	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("AcquireReadLock after write lock released = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AcquireReadLock did not complete within 5s after write lock release")
	}
}

// ── Lock file persistence ──────────────────────────────────────────────

// TestLockFileExistsAfterSave verifies that after Save completes, the
// .lock file persists on disk (it is never deleted).
func TestLockFileExistsAfterSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persist.json")
	lockPath := CredentialLockPath(path)
	store := FileStore{Path: path}

	// Before Save: lock file should not exist.
	if _, err := os.Stat(lockPath); err == nil {
		t.Fatal("lock file exists before any Save")
	}

	// Save creates the lock file.
	if err := store.Save(context.Background(), testCred); err != nil {
		t.Fatalf("Save = %v", err)
	}

	// After Save: lock file must exist (persistent).
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file missing after Save: %v", err)
	}

	// Clear does NOT remove the lock file (by design — flock is fd-based).
	if err := store.Clear(context.Background()); err != nil {
		t.Fatalf("Clear = %v", err)
	}

	// After Clear: lock file should still exist.
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file removed after Clear (should persist): %v", err)
	}
}

// TestLockFileCreatedByAcquireWriteLock verifies that AcquireWriteLock
// creates the lock file on first use.
func TestLockFileCreatedByAcquireWriteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "create_test.json")
	lockPath := CredentialLockPath(path)

	lk, err := AcquireWriteLock(path)
	if err != nil {
		t.Fatalf("AcquireWriteLock = %v", err)
	}

	// Lock file should exist.
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file not created by AcquireWriteLock: %v", err)
	}

	ReleaseLock(lk)

	// Lock file still exists after release.
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file removed after release (should persist): %v", err)
	}
}

// TestLockFileCreatedByAcquireReadLock verifies that AcquireReadLock
// also creates the lock file on first use.
func TestLockFileCreatedByAcquireReadLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "read_create_test.json")
	lockPath := CredentialLockPath(path)

	lk, err := AcquireReadLock(path)
	if err != nil {
		t.Fatalf("AcquireReadLock = %v", err)
	}

	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file not created by AcquireReadLock: %v", err)
	}

	ReleaseLock(lk)
}

// ── ABBA Deadlock Prevention ───────────────────────────────────────────

// TestABBADeadlockPrevention verifies that the lock ordering (singleton →
// credential) prevents ABBA deadlocks. The singleton lock uses LOCK_EX|LOCK_NB
// (non-blocking), so if a credential holder tries to acquire the singleton
// lock, it immediately fails with ErrAlreadyRunning rather than deadlocking.
//
// Scenario:
//   Goroutine A: acquire singleton lock → hold → try credential lock (blocks)
//   Goroutine B: acquire credential lock → hold → try singleton lock (LOCK_NB → fails!)
//
// Goroutine B's failure breaks the deadlock — this is the detection mechanism.
func TestABBADeadlockPrevention(t *testing.T) {
	singletonPath := filepath.Join(t.TempDir(), "singleton.lock")
	credPath := filepath.Join(t.TempDir(), "credentials.json")

	var (
		aStep   sync.WaitGroup
		bStep   sync.WaitGroup
		allDone sync.WaitGroup
	)
	aStep.Add(1) // A waits for B to acquire credential lock first
	bStep.Add(1) // B waits for A to acquire singleton lock first

	type result struct {
		err  error
		info string
	}
	results := make(chan result, 2)

	allDone.Add(1)
	go func() {
		defer allDone.Done()

		// Goroutine A: singleton → credential (correct order)
		lk, err := lock.New(singletonPath)
		if err != nil {
			results <- result{err: err, info: "A: singleton lock"}
			aStep.Done()
			return
		}
		defer lk.Release()

		// Signal B that singleton is held.
		aStep.Done()

		// Wait for B to acquire credential lock.
		bStep.Wait()

		// Try to acquire credential lock — will block because B holds it.
		// Use a timeout to detect deadlock.
		credDone := make(chan error, 1)
		go func() {
			_, err := AcquireWriteLock(credPath)
			credDone <- err
		}()

		select {
		case err := <-credDone:
			if err != nil {
				results <- result{err: err, info: "A: credential lock after singleton"}
			} else {
				results <- result{info: "A: credential lock acquired successfully"}
			}
		case <-time.After(3 * time.Second):
			results <- result{err: nil, info: "A: credential lock blocked for 3s — possible deadlock"}
		}
	}()

	allDone.Add(1)
	go func() {
		defer allDone.Done()

		// Goroutine B: credential → singleton (WRONG order — should detect deadlock)
		aStep.Wait()

		credLock, err := AcquireWriteLock(credPath)
		if err != nil {
			results <- result{err: err, info: "B: credential lock"}
			bStep.Done()
			return
		}
		defer ReleaseLock(credLock)

		bStep.Done()

		lk, err := lock.New(singletonPath)
		if err == lock.ErrAlreadyRunning {
			results <- result{info: "B: singleton lock correctly rejected with ErrAlreadyRunning"}
			return
		}
		if err != nil {
			results <- result{err: err, info: "B: unexpected error acquiring singleton lock"}
			return
		}
		lk.Release()
		results <- result{info: "B: SINGLETON LOCK ACQUIRED — deadlock detection MISSED!"}
	}()

	// Collect all results from both goroutines.
	var errors []error
	deadlockDetected := false
	timedOut := false

	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			if r.err != nil {
				errors = append(errors, fmt.Errorf("%s: %w", r.info, r.err))
			} else if r.info == "B: singleton lock correctly rejected with ErrAlreadyRunning" {
				deadlockDetected = true
				t.Log(r.info)
			} else if r.info == "B: SINGLETON LOCK ACQUIRED — deadlock detection MISSED!" {
				errors = append(errors, fmt.Errorf("%s", r.info))
			} else {
				t.Log(r.info)
			}
		case <-time.After(5 * time.Second):
			timedOut = true
			t.Error("Test timed out waiting for results")
		}
	}

	// Wait for goroutines to finish before cleanup.
	allDone.Wait()

	if timedOut {
		t.Fatal("Test timed out — potential deadlock or lock mismatch")
	}
	for _, err := range errors {
		t.Error(err)
	}
	if !deadlockDetected {
		t.Fatal("ABBA deadlock was NOT detected — lock ordering may be broken")
	}
	if len(errors) > 0 {
		t.Fatal("ABBA deadlock detection test had errors")
	}
}

// TestLockFilePathDerivation verifies the lock path is derived correctly.
func TestLockFilePathDerivation(t *testing.T) {
	cases := []struct {
		credsPath string
		want      string
	}{
		{"/home/user/.config/manicode/credentials.json", "/home/user/.config/manicode/credentials.json.lock"},
		{"/tmp/creds.json", "/tmp/creds.json.lock"},
		{"creds.json", "creds.json.lock"},
	}

	for _, tc := range cases {
		got := CredentialLockPath(tc.credsPath)
		if got != tc.want {
			t.Errorf("CredentialLockPath(%q) = %q, want %q", tc.credsPath, got, tc.want)
		}
	}
}
