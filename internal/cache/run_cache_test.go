package cache

import (
	"testing"
	"time"
)

func TestNewRunCache_Defaults(t *testing.T) {
	c := NewRunCache(DefaultConfig())
	if c == nil {
		t.Fatal("NewRunCache returned nil")
	}
	if c.max != 512 {
		t.Errorf("max = %d, want 512", c.max)
	}
	if c.ttl != 30*time.Minute {
		t.Errorf("ttl = %v, want 30m", c.ttl)
	}
}

func TestSetGet(t *testing.T) {
	c := NewRunCache(Config{MaxEntries: 10, TTL: 5 * time.Minute})
	c.Set("key1", "value1")

	val, ok := c.Get("key1")
	if !ok {
		t.Error("Get returned false for existing key")
	}
	if val != "value1" {
		t.Errorf("Get = %q, want %q", val, "value1")
	}
}

func TestGet_Missing(t *testing.T) {
	c := NewRunCache(Config{MaxEntries: 10, TTL: 5 * time.Minute})
	_, ok := c.Get("nonexistent")
	if ok {
		t.Error("Get returned true for missing key")
	}
}

func TestGet_Expired(t *testing.T) {
	c := NewRunCache(Config{MaxEntries: 10, TTL: 1 * time.Millisecond})
	c.Set("key1", "value1")

	// Wait for expiry.
	time.Sleep(5 * time.Millisecond)

	_, ok := c.Get("key1")
	if ok {
		t.Error("Get returned true for expired key")
	}
}

func TestSet_Update(t *testing.T) {
	c := NewRunCache(Config{MaxEntries: 10, TTL: 5 * time.Minute})
	c.Set("key1", "value1")
	c.Set("key1", "value2")

	val, ok := c.Get("key1")
	if !ok {
		t.Error("Get returned false for updated key")
	}
	if val != "value2" {
		t.Errorf("Get = %q, want %q", val, "value2")
	}
}

func TestEviction(t *testing.T) {
	c := NewRunCache(Config{MaxEntries: 3, TTL: 5 * time.Minute})
	c.Set("a", "1")
	c.Set("b", "2")
	c.Set("c", "3")
	c.Set("d", "4") // Should evict "a"

	_, ok := c.Get("a")
	if ok {
		t.Error("Oldest entry 'a' not evicted")
	}

	// b, c, d should still exist.
	for _, k := range []string{"b", "c", "d"} {
		_, ok := c.Get(k)
		if !ok {
			t.Errorf("Entry %q was evicted unexpectedly", k)
		}
	}
}

func TestDelete(t *testing.T) {
	c := NewRunCache(Config{MaxEntries: 10, TTL: 5 * time.Minute})
	c.Set("key1", "value1")
	c.Delete("key1")

	_, ok := c.Get("key1")
	if ok {
		t.Error("Get returned true after Delete")
	}
}

func TestClear(t *testing.T) {
	c := NewRunCache(Config{MaxEntries: 10, TTL: 5 * time.Minute})
	c.Set("a", "1")
	c.Set("b", "2")
	c.Clear()

	if c.Len() != 0 {
		t.Errorf("Len = %d after Clear, want 0", c.Len())
	}
}

func TestLen(t *testing.T) {
	c := NewRunCache(Config{MaxEntries: 10, TTL: 5 * time.Minute})
	if c.Len() != 0 {
		t.Errorf("Len = %d for empty cache, want 0", c.Len())
	}
	c.Set("a", "1")
	if c.Len() != 1 {
		t.Errorf("Len = %d, want 1", c.Len())
	}
	c.Set("b", "2")
	if c.Len() != 2 {
		t.Errorf("Len = %d, want 2", c.Len())
	}
}

func TestHashFNV1a(t *testing.T) {
	c := NewRunCache(DefaultConfig())
	h1 := c.HashFNV1a("test-value")
	h2 := c.HashFNV1a("test-value")
	if h1 != h2 {
		t.Errorf("HashFNV1a not deterministic: %q != %q", h1, h2)
	}
	if h1 == "" {
		t.Error("HashFNV1a returned empty string")
	}

	h3 := c.HashFNV1a("different-value")
	if h1 == h3 {
		t.Error("HashFNV1a returned same hash for different inputs")
	}
}

func TestConcurrency(t *testing.T) {
	c := NewRunCache(Config{MaxEntries: 100, TTL: 5 * time.Minute})
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			c.Set("key", "value")
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < 100; i++ {
			c.Get("key")
		}
		done <- struct{}{}
	}()
	<-done
	<-done
}

func TestBuildKeys(t *testing.T) {
	key1 := BuildAgentRunCacheKey("hash123", "agent1", "clientA")
	key2 := BuildAgentRunCacheKey("hash123", "agent1", "clientA")
	if key1 != key2 {
		t.Errorf("BuildAgentRunCacheKey not deterministic: %q != %q", key1, key2)
	}

	fbKey := BuildFreebuffSessionCacheKey("hash123")
	if fbKey == "" {
		t.Error("BuildFreebuffSessionCacheKey returned empty")
	}
	if len(fbKey) <= len("freebuff_session\x00") {
		t.Error("BuildFreebuffSessionCacheKey too short")
	}
}
