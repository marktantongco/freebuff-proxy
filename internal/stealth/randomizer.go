package stealth

import (
	"math/rand"
	"sync"
	"time"
)

// Randomizer adds configurable timing jitter between requests to prevent
// pattern-based detection. It spreads request timing across a realistic
// human-like distribution rather than the machine-gun pattern of automated
// clients.
type Randomizer struct {
	mu          sync.Mutex
	lastTime    time.Time
	minDelay    time.Duration
	maxDelay    time.Duration
	rng         *rand.Rand
}

// RandomizerOption configures a Randomizer.
type RandomizerOption func(*Randomizer)

// WithMinDelay sets the minimum delay between requests (default: 50ms).
func WithMinDelay(d time.Duration) RandomizerOption {
	return func(r *Randomizer) {
		r.minDelay = d
	}
}

// WithMaxDelay sets the maximum delay between requests (default: 300ms).
func WithMaxDelay(d time.Duration) RandomizerOption {
	return func(r *Randomizer) {
		r.maxDelay = d
	}
}

// NewRandomizer creates a new Randomizer with the given options.
//
// Defaults:
//   - MinDelay: 50ms
//   - MaxDelay: 300ms
//
// These values simulate human reaction time between page loads.
func NewRandomizer(opts ...RandomizerOption) *Randomizer {
	r := &Randomizer{
		lastTime: time.Now(),
		minDelay: 50 * time.Millisecond,
		maxDelay: 300 * time.Millisecond,
		rng:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Wait blocks for a random duration between MinDelay and MaxDelay,
// measured from the last call to Wait or Reset. This ensures the delay
// is relative to the previous request, maintaining realistic timing.
//
// If the time since last request already exceeds the computed delay,
// Wait returns immediately (no negative delay).
func (r *Randomizer) Wait() {
	r.mu.Lock()
	defer r.mu.Unlock()

	elapsed := time.Since(r.lastTime)
	delay := r.randomDelay()

	if elapsed < delay {
		remaining := delay - elapsed
		time.Sleep(remaining)
	}

	r.lastTime = time.Now()
}

// Reset clears the last request time so the next Wait() starts fresh.
func (r *Randomizer) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastTime = time.Now()
}

// randomDelay returns a random duration in [MinDelay, MaxDelay].
func (r *Randomizer) randomDelay() time.Duration {
	if r.minDelay >= r.maxDelay {
		return r.minDelay
	}
	delta := int64(r.maxDelay - r.minDelay)
	return r.minDelay + time.Duration(r.rng.Int63n(delta))
}

// JitterDuration returns a duration with +/- 20% jitter applied to the base.
// Useful for randomizing timeouts, keepalive intervals, and retry delays
// to prevent thundering herd patterns.
func JitterDuration(base time.Duration) time.Duration {
	if base <= 0 {
		return base
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	jitter := time.Duration(float64(base) * 0.2)
	offset := time.Duration(rng.Int63n(2*int64(jitter)+1)) - jitter
	return base + offset
}

// HumanDelay returns a random duration that simulates human reading time
// based on content length. Roughly 200-400 words per minute reading speed.
// For zero content, returns a random delay between 500ms and 2s.
func HumanDelay(contentLength int) time.Duration {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	if contentLength <= 0 {
		// Random pause: 500ms - 2s
		return 500*time.Millisecond + time.Duration(rng.Int63n(1501))*time.Millisecond
	}
	// Rough estimate: 250 words/min reading, ~5 chars/word
	words := contentLength / 5
	readingTimeMs := (words * 60 * 1000) / 250
	// Add variability: +/- 30%
	jitter := float64(readingTimeMs) * 0.3
	actual := float64(readingTimeMs) - jitter + float64(rng.Int63n(int64(2*jitter)+1))
	if actual < 100 {
		actual = 100
	}
	return time.Duration(actual) * time.Millisecond
}
