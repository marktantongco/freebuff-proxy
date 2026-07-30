// Package cache provides in-memory caching for agent run IDs and freebuff session IDs.
//
// Ported from codebuff-proxy (TypeScript) patterns:
//   - FNV-1a hashing for API key fingerprinting (no plaintext in cache keys)
//   - TTL-based expiry with lazy cleanup
//   - LRU-like eviction via max-entries cap
package cache

import (
	"fmt"
	"hash/fnv"
	"sync"
	"time"
)

// entry holds a cached value with its expiry time.
type entry struct {
	value     string
	expiresAt int64 // unix millis
}

// RunCache is an in-memory, TTL-aware cache for agent run IDs and freebuff session IDs.
//
// Safe for concurrent use. Entries are evicted when:
//   - TTL expires (checked on Get)
//   - Max entries is exceeded (oldest by insertion order, not access time)
//
// Ported from codebuff-proxy patterns:
//   - Agent run cache: key = <hashed-api-key>\0<agent-id>\0<client-identity>
//   - Freebuff session cache: key = "freebuff_session\0<hashed-api-key>"
type RunCache struct {
	mu      sync.RWMutex
	entries map[string]*entry
	order   []string // insertion order for eviction
	max     int
	ttl     time.Duration
	salt    string
}

// Config configures the RunCache.
type Config struct {
	MaxEntries int
	TTL        time.Duration
	Salt       string
}

// DefaultConfig returns sensible defaults matching codebuff-proxy:
//   - MaxEntries: 512
//   - TTL: 30 minutes
//   - Salt: "cmux-use-platform-key"
func DefaultConfig() Config {
	return Config{
		MaxEntries: 512,
		TTL:        30 * time.Minute,
		Salt:       "cmux-use-platform-key",
	}
}

// NewRunCache creates a new RunCache with the given config.
func NewRunCache(cfg Config) *RunCache {
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = 512
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 30 * time.Minute
	}
	return &RunCache{
		entries: make(map[string]*entry),
		max:     cfg.MaxEntries,
		ttl:     cfg.TTL,
		salt:    cfg.Salt,
	}
}

// Get retrieves a cached value. Returns the value and true if found and not expired.
// Expired entries are lazily removed.
func (c *RunCache) Get(key string) (string, bool) {
	c.mu.RLock()
	e, ok := c.entries[key]
	if !ok {
		c.mu.RUnlock()
		return "", false
	}

	now := time.Now().UnixMilli()
	if e.expiresAt > 0 && now > e.expiresAt {
		c.mu.RUnlock()
		// Lazily delete expired entry (need write lock).
		c.mu.Lock()
		delete(c.entries, key)
		c.removeOrderLocked(key)
		c.mu.Unlock()
		return "", false
	}
	c.mu.RUnlock()
	return e.value, true
}

// Set stores a value with TTL. If the cache is at max capacity, the oldest
// entry (by insertion order) is evicted first.
func (c *RunCache) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// If already exists, just update.
	if _, ok := c.entries[key]; ok {
		c.entries[key] = &entry{
			value:     value,
			expiresAt: time.Now().Add(c.ttl).UnixMilli(),
		}
		return
	}

	// Evict oldest if at capacity.
	if len(c.entries) >= c.max {
		for _, oldKey := range c.order {
			if _, ok := c.entries[oldKey]; ok {
				delete(c.entries, oldKey)
				c.removeOrderLocked(oldKey)
				break
			}
		}
	}

	c.entries[key] = &entry{
		value:     value,
		expiresAt: time.Now().Add(c.ttl).UnixMilli(),
	}
	c.order = append(c.order, key)
}

// Delete removes a key from the cache.
func (c *RunCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
	c.removeOrderLocked(key)
}

// Clear removes all entries from the cache.
func (c *RunCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*entry)
	c.order = nil
}

// Len returns the current number of cache entries.
func (c *RunCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// HashFNV1a returns a FNV-1a 32-bit hash of the input, salted with the cache's salt.
// This matches codebuff-proxy's hashForCacheKey() pattern.
func (c *RunCache) HashFNV1a(value string) string {
	input := c.salt + ":" + value
	h := fnv.New32a()
	h.Write([]byte(input))
	return fmt.Sprintf("%x", h.Sum32())
}

// BuildAgentRunCacheKey builds the cache key for an agent run ID.
// Pattern: <hashed-api-key>\0<agent-id>\0<client-identity>
func BuildAgentRunCacheKey(hashedKey, agentID, clientIdentity string) string {
	return hashedKey + "\x00" + agentID + "\x00" + clientIdentity
}

// BuildFreebuffSessionCacheKey builds the cache key for a freebuff session.
// Pattern: "freebuff_session\0<hashed-api-key>"
func BuildFreebuffSessionCacheKey(hashedKey string) string {
	return "freebuff_session\x00" + hashedKey
}

// removeOrderLocked removes a key from the insertion order slice.
// Caller must hold c.mu write lock.
func (c *RunCache) removeOrderLocked(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}
