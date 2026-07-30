package session

import (
	"context"
	"sync"
	"testing"
	"time"
)

// ── NewPool Tests ──────────────────────────────────────────────────────────

func TestNewPoolDefaults(t *testing.T) {
	t.Parallel()
	p := NewPool(nil, 0, 0)
	if p == nil {
		t.Fatal("NewPool() returned nil")
	}
	if p.maxSize != 10 {
		t.Errorf("maxSize = %d, want 10", p.maxSize)
	}
	if p.ttl != 55*time.Minute {
		t.Errorf("ttl = %v, want 55m", p.ttl)
	}
	if len(p.tokens) != 0 {
		t.Errorf("tokens = %v, want empty", p.tokens)
	}
}

func TestNewPoolCustom(t *testing.T) {
	t.Parallel()
	p := NewPool([]string{"a", "b"}, 5, 10*time.Minute)
	if p.maxSize != 5 {
		t.Errorf("maxSize = %d, want 5", p.maxSize)
	}
	if p.ttl != 10*time.Minute {
		t.Errorf("ttl = %v, want 10m", p.ttl)
	}
	if len(p.tokens) != 2 {
		t.Errorf("tokens = %v, want 2", len(p.tokens))
	}
}

// ── Acquire Tests ──────────────────────────────────────────────────────────

func TestAcquireFirstToken(t *testing.T) {
	p := NewPool([]string{"token-1", "token-2"}, 10, 55*time.Minute)

	s, err := p.Acquire(context.Background(), "model-a")
	if err != nil {
		t.Fatalf("Acquire() failed: %v", err)
	}
	if s == nil {
		t.Fatal("Acquire() returned nil session")
	}
	if s.Token != "token-1" {
		t.Errorf("session.Token = %q, want %q", s.Token, "token-1")
	}
	if s.Model != "model-a" {
		t.Errorf("session.Model = %q, want %q", s.Model, "model-a")
	}
	if !s.InUse {
		t.Error("session.InUse = false, want true")
	}
	if p.Len() != 1 {
		t.Errorf("pool.Len() = %d, want 1", p.Len())
	}
}

func TestAcquireRoundRobin(t *testing.T) {
	p := NewPool([]string{"token-1", "token-2", "token-3"}, 10, 55*time.Minute)

	s1, _ := p.Acquire(context.Background(), "model-a")
	if s1.Token != "token-1" {
		t.Errorf("first Acquire token = %q, want %q", s1.Token, "token-1")
	}

	s2, _ := p.Acquire(context.Background(), "model-b")
	if s2.Token != "token-2" {
		t.Errorf("second Acquire token = %q, want %q", s2.Token, "token-2")
	}

	s3, _ := p.Acquire(context.Background(), "model-c")
	if s3.Token != "token-3" {
		t.Errorf("third Acquire token = %q, want %q", s3.Token, "token-3")
	}

	// After all tokens used, next Acquire should wrap around.
	p.Release(s1)
	p.Release(s2)
	p.Release(s3)

	s4, _ := p.Acquire(context.Background(), "model-a")
	if s4.Token != "token-1" {
		t.Errorf("wrap-around Acquire token = %q, want %q", s4.Token, "token-1")
	}
}

func TestAcquireReusesFreshSession(t *testing.T) {
	p := NewPool([]string{"token-1", "token-2"}, 10, 55*time.Minute)

	// Acquire and release for the same model.
	s1, _ := p.Acquire(context.Background(), "model-a")
	p.Release(s1)

	// Next Acquire for same model should reuse the fresh session.
	s2, err := p.Acquire(context.Background(), "model-a")
	if err != nil {
		t.Fatalf("Acquire() failed: %v", err)
	}
	if s2.Token != s1.Token {
		t.Errorf("reuse: got token %q, want %q (same)", s2.Token, s1.Token)
	}
	if !s2.InUse {
		t.Error("reused session not marked InUse")
	}
	if p.Len() != 1 {
		t.Errorf("Len = %d, want 1 (session reused, not new)", p.Len())
	}
}

func TestAcquireDoesNotReuseExpiredSession(t *testing.T) {
	// Create pool manually with negative TTL to bypass NewPool's `if ttl <= 0` guard.
	p := &Pool{
		tokens:   []string{"token-1", "token-2"},
		sessions: make(map[string]*TokenSession),
		maxSize:  10,
		ttl:      -1 * time.Nanosecond, // always expired
	}

	s1, _ := p.Acquire(context.Background(), "model-a")
	p.Release(s1)

	s2, err := p.Acquire(context.Background(), "model-a")
	if err != nil {
		t.Fatalf("Acquire() failed: %v", err)
	}
	// Should get token-2 because token-1's session is expired (negative TTL).
	if s2.Token != "token-2" {
		t.Errorf("after expiration: got token %q, want %q (new)", s2.Token, "token-2")
	}
}

func TestAcquireDoesNotReuseDifferentModel(t *testing.T) {
	p := NewPool([]string{"token-1", "token-2"}, 10, 55*time.Minute)

	s1, _ := p.Acquire(context.Background(), "model-a")
	p.Release(s1)

	// Different model should not reuse token-1's session.
	s2, err := p.Acquire(context.Background(), "model-b")
	if err != nil {
		t.Fatalf("Acquire() failed: %v", err)
	}
	if s2.Token != "token-2" {
		t.Errorf("different model: got token %q, want %q", s2.Token, "token-2")
	}
}

func TestAcquireDoesNotReuseBusySession(t *testing.T) {
	p := NewPool([]string{"token-1", "token-2"}, 10, 55*time.Minute)

	// Acquire without releasing — session stays InUse.
	s1, _ := p.Acquire(context.Background(), "model-a")

	// Second Acquire should not reuse busy session, should get token-2.
	s2, err := p.Acquire(context.Background(), "model-a")
	if err != nil {
		t.Fatalf("Acquire() failed: %v", err)
	}
	if s2.Token != "token-2" {
		t.Errorf("busy session: got token %q, want %q", s2.Token, "token-2")
	}

	_ = s1
}

func TestAcquireEvictsOldestUnused(t *testing.T) {
	p := NewPool([]string{"token-1", "token-2"}, 10, 55*time.Minute)

	// Use both tokens, then keep token-2's session active.
	s1, _ := p.Acquire(context.Background(), "model-a")
	s2, _ := p.Acquire(context.Background(), "model-b")
	p.Release(s1)
	// s2 is still InUse.

	time.Sleep(time.Millisecond)

	// Acquire again — should evict token-1's session (oldest unused) and reuse token-1.
	s3, err := p.Acquire(context.Background(), "model-c")
	if err != nil {
		t.Fatalf("Acquire() failed: %v", err)
	}
	if s3.Token != "token-1" {
		t.Errorf("eviction: got token %q, want %q", s3.Token, "token-1")
	}
	if s3.Model != "model-c" {
		t.Errorf("eviction model: got %q, want %q", s3.Model, "model-c")
	}

	_ = s2
}

func TestAcquireAllBusyReturnsError(t *testing.T) {
	p := NewPool([]string{"token-1"}, 10, 55*time.Minute)

	s1, _ := p.Acquire(context.Background(), "model-a")

	// All tokens are InUse, none can be evicted.
	_, err := p.Acquire(context.Background(), "model-b")
	if err == nil {
		t.Fatal("Acquire() should return error when all tokens busy")
	}
	_ = s1
}

func TestAcquireEmptyPoolReturnsError(t *testing.T) {
	p := NewPool(nil, 10, 55*time.Minute)

	_, err := p.Acquire(context.Background(), "model-a")
	if err == nil {
		t.Fatal("Acquire() on empty pool should return error")
	}
}

// ── Release Tests ──────────────────────────────────────────────────────────

func TestReleaseMarksNotInUse(t *testing.T) {
	p := NewPool([]string{"token-1"}, 10, 55*time.Minute)

	s, _ := p.Acquire(context.Background(), "model-a")
	if !s.InUse {
		t.Fatal("session should be InUse after Acquire")
	}

	p.Release(s)
	if s.InUse {
		t.Error("session.InUse should be false after Release")
	}
}

func TestReleaseNilDoesNotPanic(t *testing.T) {
	p := NewPool([]string{"token-1"}, 10, 55*time.Minute)

	// Should not panic.
	p.Release(nil)
}

// ── MarkLocked Tests ───────────────────────────────────────────────────────

func TestMarkLocked(t *testing.T) {
	p := NewPool([]string{"token-1"}, 10, 55*time.Minute)

	s, _ := p.Acquire(context.Background(), "model-a")

	p.MarkLocked(s)
	if !s.Locked {
		t.Error("session should be Locked after MarkLocked")
	}

	locked := p.LenLocked()
	if locked["model-a"] != 1 {
		t.Errorf("LenLocked() = %v, want {model-a: 1}", locked)
	}
}

func TestMarkLockedNilDoesNotPanic(t *testing.T) {
	p := NewPool([]string{"token-1"}, 10, 55*time.Minute)
	p.MarkLocked(nil) // Should not panic.
}

func TestMarkTokenLocked(t *testing.T) {
	p := NewPool([]string{"token-1", "token-2"}, 10, 55*time.Minute)

	s1, _ := p.Acquire(context.Background(), "model-a")
	s2, _ := p.Acquire(context.Background(), "model-b")

	p.MarkTokenLocked("token-1")

	if !s1.Locked {
		t.Error("token-1 session should be locked after MarkTokenLocked")
	}
	if s2.Locked {
		t.Error("token-2 session should not be locked")
	}

	locked := p.LenLocked()
	if locked["model-a"] != 1 {
		t.Errorf("LenLocked() = %v, want {model-a: 1}", locked)
	}
}

func TestMarkTokenLockedNonExistent(t *testing.T) {
	p := NewPool([]string{"token-1"}, 10, 55*time.Minute)

	// Should not panic.
	p.MarkTokenLocked("nonexistent-token")
}

func TestMarkTokenLockedEmptyToken(t *testing.T) {
	p := NewPool([]string{"token-1"}, 10, 55*time.Minute)

	// Should not panic.
	p.MarkTokenLocked("")
}

// ── LenLocked Tests ────────────────────────────────────────────────────────

func TestLenLockedEmpty(t *testing.T) {
	p := NewPool(nil, 10, 55*time.Minute)

	locked := p.LenLocked()
	if len(locked) != 0 {
		t.Errorf("LenLocked() = %v, want empty", locked)
	}
}

func TestLenLockedMultipleModels(t *testing.T) {
	p := NewPool([]string{"token-1", "token-2", "token-3"}, 10, 55*time.Minute)

	s1, _ := p.Acquire(context.Background(), "model-a")
	s2, _ := p.Acquire(context.Background(), "model-b")
	s3, _ := p.Acquire(context.Background(), "model-a")

	p.MarkLocked(s1) // model-a locked
	p.MarkLocked(s3) // model-a locked again

	locked := p.LenLocked()
	if locked["model-a"] != 2 {
		t.Errorf("LenLocked()[model-a] = %d, want 2", locked["model-a"])
	}
	if locked["model-b"] != 0 {
		t.Errorf("LenLocked()[model-b] = %d, want 0", locked["model-b"])
	}

	_ = s2
}

// ── Remove Tests ───────────────────────────────────────────────────────────

func TestRemove(t *testing.T) {
	p := NewPool([]string{"token-1", "token-2"}, 10, 55*time.Minute)

	p.Acquire(context.Background(), "model-a")
	if p.Len() != 1 {
		t.Errorf("Len = %d, want 1", p.Len())
	}

	p.Remove("token-1")
	if p.Len() != 0 {
		t.Errorf("Len = %d, want 0 after Remove", p.Len())
	}
}

func TestRemoveNonExistent(t *testing.T) {
	p := NewPool([]string{"token-1"}, 10, 55*time.Minute)
	p.Remove("nonexistent") // Should not panic.
}

// ── Len Tests ──────────────────────────────────────────────────────────────

func TestLenEmpty(t *testing.T) {
	p := NewPool(nil, 10, 55*time.Minute)
	if p.Len() != 0 {
		t.Errorf("Len() = %d, want 0", p.Len())
	}
}

func TestLenWithSessions(t *testing.T) {
	p := NewPool([]string{"token-1", "token-2"}, 10, 55*time.Minute)

	p.Acquire(context.Background(), "model-a")
	if p.Len() != 1 {
		t.Errorf("Len() = %d, want 1", p.Len())
	}

	p.Acquire(context.Background(), "model-b")
	if p.Len() != 2 {
		t.Errorf("Len() = %d, want 2", p.Len())
	}
}

// ── Stats Tests ────────────────────────────────────────────────────────────

func TestStatsEmpty(t *testing.T) {
	p := NewPool(nil, 10, 55*time.Minute)
	s := p.Stats().(map[string]any)

	if s["total_tokens"] != 0 {
		t.Errorf("Stats().total_tokens = %v, want 0", s["total_tokens"])
	}
	if s["active_sessions"] != 0 {
		t.Errorf("Stats().active_sessions = %v, want 0", s["active_sessions"])
	}
	if s["in_use"] != 0 {
		t.Errorf("Stats().in_use = %v, want 0", s["in_use"])
	}
	if s["locked"] != 0 {
		t.Errorf("Stats().locked = %v, want 0", s["locked"])
	}
}

func TestStatsWithSessions(t *testing.T) {
	p := NewPool([]string{"token-1", "token-2", "token-3"}, 10, 55*time.Minute)

	s1, _ := p.Acquire(context.Background(), "model-a")
	s2, _ := p.Acquire(context.Background(), "model-b")
	p.Release(s2)
	p.MarkLocked(s1)

	stats := p.Stats().(map[string]any)

	if stats["total_tokens"] != 3 {
		t.Errorf("total_tokens = %v, want 3", stats["total_tokens"])
	}
	if stats["active_sessions"] != 2 {
		t.Errorf("active_sessions = %v, want 2", stats["active_sessions"])
	}
	if stats["in_use"] != 1 {
		t.Errorf("in_use = %v, want 1", stats["in_use"])
	}
	if stats["locked"] != 1 {
		t.Errorf("locked = %v, want 1", stats["locked"])
	}

	byModel := stats["by_model"].(map[string]int)
	if byModel["model-a"] != 1 {
		t.Errorf("by_model[model-a] = %v, want 1", byModel["model-a"])
	}
	if byModel["model-b"] != 1 {
		t.Errorf("by_model[model-b] = %v, want 1", byModel["model-b"])
	}
}

// ── Concurrent Safety Tests ────────────────────────────────────────────────

func TestConcurrentAcquire(t *testing.T) {
	p := NewPool([]string{"token-1", "token-2", "token-3", "token-4", "token-5"}, 10, 55*time.Minute)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := p.Acquire(context.Background(), "model-x")
			if err != nil {
				t.Errorf("Acquire() failed: %v", err)
				return
			}
			p.Release(s)
		}()
	}
	wg.Wait()

	// This test primarily verifies that concurrent Acquire/Release doesn't
	// deadlock. The exact Len depends on goroutine scheduling and may vary.
	// At minimum, no sessions should be left InUse.
	for _, s := range p.sessions {
		if s.InUse {
			t.Errorf("session %s is still InUse after all goroutines completed", s.Token)
		}
	}
}

func TestConcurrentAcquireRelease(t *testing.T) {
	p := NewPool([]string{"token-1", "token-2", "token-3"}, 10, 55*time.Minute)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := p.Acquire(context.Background(), "model-x")
			if err != nil {
				return
			}
			p.Release(s)
		}()
	}
	wg.Wait()
}

func TestConcurrentMarkAndStats(t *testing.T) {
	p := NewPool([]string{"token-1", "token-2", "token-3"}, 10, 55*time.Minute)

	s1, _ := p.Acquire(context.Background(), "model-a")
	s2, _ := p.Acquire(context.Background(), "model-b")
	s3, _ := p.Acquire(context.Background(), "model-c")

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.Len()
			p.LenLocked()
			p.Stats()
		}()
	}

	p.MarkLocked(s1)
	p.MarkLocked(s2)
	p.MarkLocked(s3)

	wg.Wait()

	locked := p.LenLocked()
	if len(locked) != 3 {
		t.Errorf("LenLocked() = %v, want 3 models locked", locked)
	}
}

func TestConcurrentMixedOperations(t *testing.T) {
	p := NewPool([]string{"token-1", "token-2", "token-3", "token-4", "token-5"}, 10, 55*time.Minute)

	var sessions []*TokenSession
	for i := 0; i < 5; i++ {
		s, _ := p.Acquire(context.Background(), "model-x")
		sessions = append(sessions, s)
	}

	var wg sync.WaitGroup
	// Reader goroutines.
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				p.Len()
				p.LenLocked()
				p.Stats()
			}
		}()
	}
	// Writer goroutines.
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				p.MarkLocked(sessions[j%len(sessions)])
				p.Remove("nonexistent")
			}
		}()
	}
	wg.Wait()

	// Release all to clean up.
	for _, s := range sessions {
		p.Release(s)
	}
}
