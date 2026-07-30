// Command cred-lock-test verifies the credential flock lock end-to-end.
//
// Usage:
//
//	go run ./cmd/cred-lock-test
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ferdiunal/freebuff-proxy/internal/credentials"
)

func main() {
	exitCode := 0
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		exitCode = 1
	} else {
		fmt.Println("\nPASS: all credential lock assertions passed")
	}
	os.Exit(exitCode)
}

func run() error {
	dir, err := os.MkdirTemp("", "cred-lock-e2e-*")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	credPath := filepath.Join(dir, "credentials.json")
	lockPath := credPath + ".lock"
	store := credentials.FileStore{Path: credPath}
	ctx := context.Background()

	// ── Step 1: Save creates both files ────────────────────────────────
	fmt.Println("=== Step 1: Save creates credentials.json + credentials.json.lock ===")
	if err := store.Save(ctx, credentials.Credential{
		ID: "e2e_001", Name: "Alice", Email: "alice@test.com",
		AuthToken: "tok_alice_001", FingerprintID: "fp_001", FingerprintHash: "hash_001",
	}); err != nil {
		return fmt.Errorf("initial Save: %w", err)
	}
	if _, err := os.Stat(credPath); err != nil {
		return fmt.Errorf("credentials.json not found: %w", err)
	}
	fmt.Printf("  OK: credentials.json exists (%d bytes)\n", fileSize(credPath))
	if _, err := os.Stat(lockPath); err != nil {
		return fmt.Errorf("credentials.json.lock not found: %w", err)
	}
	fmt.Printf("  OK: credentials.json.lock exists (%d bytes)\n", fileSize(lockPath))

	// ── Step 2: Load round-trip ───────────────────────────────────────
	fmt.Println("\n=== Step 2: Load round-trip ===")
	cred, err := store.Load(ctx)
	if err != nil {
		return fmt.Errorf("Load: %w", err)
	}
	if cred.AuthToken != "tok_alice_001" {
		return fmt.Errorf("Load returned wrong token: %q", cred.AuthToken)
	}
	fmt.Printf("  OK: loaded credential: %s (%s)\n", cred.Name, cred.Email)

	// ── Step 3: Concurrent Save serialization ─────────────────────────
	fmt.Println("\n=== Step 3: Concurrent Save serialization ===")
	var wg sync.WaitGroup
	errs := make(chan error, 2)

	wg.Add(1)
	go func() {
		defer wg.Done()
		errs <- store.Save(ctx, credentials.Credential{
			ID: "e2e_002", Name: "Bob", Email: "bob@test.com",
			AuthToken: "tok_bob_002", FingerprintID: "fp_002", FingerprintHash: "hash_002",
		})
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		errs <- store.Save(ctx, credentials.Credential{
			ID: "e2e_003", Name: "Charlie", Email: "charlie@test.com",
			AuthToken: "tok_charlie_003", FingerprintID: "fp_003", FingerprintHash: "hash_003",
		})
	}()
	wg.Wait()
	close(errs)

	for e := range errs {
		if e != nil {
			return fmt.Errorf("concurrent Save: %w", e)
		}
	}

	// Verify final content matches one of the two (not corrupted).
	cred, err = store.Load(ctx)
	if err != nil {
		return fmt.Errorf("Load after concurrent Save: %w", err)
	}
	if cred.AuthToken != "tok_bob_002" && cred.AuthToken != "tok_charlie_003" {
		return fmt.Errorf("concurrent Save produced corrupted content: token=%q", cred.AuthToken)
	}
	fmt.Printf("  OK: concurrent Saves serialized, final token=%q\n", cred.AuthToken)

	// ── Step 4: Lock blocks concurrent Save ───────────────────────────
	fmt.Println("\n=== Step 4: Exclusive lock blocks concurrent Save ===")

	// Acquire the write lock directly.
	lockFile, err := credentials.AcquireWriteLock(credPath)
	if err != nil {
		return fmt.Errorf("AcquireWriteLock: %w", err)
	}

	// Try Save in goroutine — should block.
	saveDone := make(chan error, 1)
	go func() {
		saveDone <- store.Save(ctx, credentials.Credential{
			ID: "e2e_004", Name: "Delta", Email: "delta@test.com",
			AuthToken: "tok_delta_004", FingerprintID: "fp_004", FingerprintHash: "hash_004",
		})
	}()

	select {
	case err := <-saveDone:
		credentials.ReleaseLock(lockFile)
		return fmt.Errorf("Save completed while lock held: %v (lock didn't block!)", err)
	case <-time.After(2 * time.Second):
		fmt.Println("  OK: Save correctly blocked (lock is holding)")
	}

	// Release the lock.
	fmt.Println("  Releasing lock...")
	credentials.ReleaseLock(lockFile)

	select {
	case err := <-saveDone:
		if err != nil {
			return fmt.Errorf("Save after lock release: %w", err)
		}
		fmt.Println("  OK: Save completed after lock release")
	case <-time.After(10 * time.Second):
		return fmt.Errorf("Save did not complete within 10s after lock release")
	}

	// ── Step 5: Final content verification ────────────────────────────
	fmt.Println("\n=== Step 5: Final content verification ===")
	cred, err = store.Load(ctx)
	if err != nil {
		return fmt.Errorf("final Load: %w", err)
	}
	if cred.AuthToken != "tok_delta_004" {
		return fmt.Errorf("final content mismatch: got %q, want tok_delta_004", cred.AuthToken)
	}
	fmt.Printf("  OK: final credential: %s (%s) — token=%q\n", cred.Name, cred.Email, cred.AuthToken)

	// ── Step 6: Lock file persists after Clear ─────────────────────────
	fmt.Println("\n=== Step 6: Lock file persists after Clear ===")
	if err := store.Clear(ctx); err != nil {
		return fmt.Errorf("Clear: %w", err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		return fmt.Errorf("lock file removed after Clear (should persist): %w", err)
	}
	fmt.Printf("  OK: lock file still exists after Clear (%d bytes)\n", fileSize(lockPath))

	return nil
}

func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return fi.Size()
}
