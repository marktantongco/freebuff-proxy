package credentials

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// BenchmarkSaveContention measures Save throughput under increasing concurrent
// writer counts to characterize the overhead of the exclusive flock (LOCK_EX).
//
// Each sub-benchmark runs with exactly N goroutines calling Save in a tight
// loop. The flock serializes the writers, so as N increases, total throughput
// should remain roughly flat (lock overhead dominates) while latency per Save
// increases linearly with N.
//
// Run with:
//
//	go test -bench=BenchmarkSave -benchtime=2s ./internal/credentials/
func BenchmarkSaveContention(b *testing.B) {
	workerCounts := []int{1, 5, 10, 50}

	for _, n := range workerCounts {
		b.Run(fmt.Sprintf("writers=%d", n), func(b *testing.B) {
			b.ReportAllocs()

			path := filepath.Join(b.TempDir(), "credentials.json")
			store := FileStore{Path: path}

			// Pre-create the lock file so AcquireWriteLock doesn't contend on
			// os.OpenFile(…, O_CREATE) itself (which is serialized by the kernel
			// for O_CREATE). This keeps the benchmark focused on flock contention.
			lk, err := AcquireWriteLock(path)
			if err != nil {
				b.Fatalf("pre-create lock file: %v", err)
			}
			ReleaseLock(lk)

			// Warm-up Save to ensure state exists before timing.
			if err := store.Save(context.Background(), testCred); err != nil {
				b.Fatalf("warm-up Save: %v", err)
			}

			b.ResetTimer()

			var wg sync.WaitGroup
			wg.Add(n)

			savesPerWorker := (b.N + n - 1) / n
			totalSaves := 0
			var mu sync.Mutex

			for i := 0; i < n; i++ {
				go func() {
					defer wg.Done()
					for j := 0; j < savesPerWorker; j++ {
						_ = store.Save(context.Background(), testCred)
					}
					mu.Lock()
					totalSaves += savesPerWorker
					mu.Unlock()
				}()
			}
			wg.Wait()

			elapsed := b.Elapsed().Seconds()
			b.ReportMetric(float64(totalSaves)/elapsed, "saves/sec")
		})
	}
}

// BenchmarkLoadContention measures Load throughput under shared (LOCK_SH)
// lock contention. Multiple readers should NOT block each other, so throughput
// should scale with reader count.
func BenchmarkLoadContention(b *testing.B) {
	workerCounts := []int{1, 5, 10, 50}

	for _, n := range workerCounts {
		b.Run(fmt.Sprintf("readers=%d", n), func(b *testing.B) {
			b.ReportAllocs()

			path := filepath.Join(b.TempDir(), "credentials.json")
			store := FileStore{Path: path}

			// Save initial data so Load has something to read.
			if err := store.Save(context.Background(), testCred); err != nil {
				b.Fatalf("setup Save: %v", err)
			}

			b.ResetTimer()

			var wg sync.WaitGroup
			wg.Add(n)
			loadsPerWorker := (b.N + n - 1) / n
			totalLoads := 0
			var mu sync.Mutex

			for i := 0; i < n; i++ {
				go func() {
					defer wg.Done()
					for j := 0; j < loadsPerWorker; j++ {
						_, _ = store.Load(context.Background())
					}
					mu.Lock()
					totalLoads += loadsPerWorker
					mu.Unlock()
				}()
			}
			wg.Wait()

			elapsed := b.Elapsed().Seconds()
			b.ReportMetric(float64(totalLoads)/elapsed, "loads/sec")
		})
	}
}

// BenchmarkNoLockSave measures Save throughput WITHOUT the advisory flock,
// isolating the raw write path cost (json.MarshalIndent + writeFileAtomic).
// Compare to BenchmarkSaveContention/writers=1 to quantify lock overhead.
func BenchmarkNoLockSave(b *testing.B) {
	b.ReportAllocs()

	path := filepath.Join(b.TempDir(), "nolock.json")
	cred := testCred

	// Warm-up.
	if err := writeFileAtomic(path, []byte(`{"default":{"id":"user_123","name":"Ada Lovelace","email":"ada@example.com","authToken":"42d7350000000000000000000000a223","fingerprintId":"fp_123","fingerprintHash":"hash_456"}}`)); err != nil {
		b.Fatalf("warm-up writeFileAtomic: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, err := json.MarshalIndent(filePayload{Default: cred}, "", "  ")
		if err != nil {
			b.Fatalf("json.Marshal: %v", err)
		}
		if err := writeFileAtomic(path, data); err != nil {
			b.Fatalf("writeFileAtomic: %v", err)
		}
	}

	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "saves/sec")
}


