package session

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// TokenSession holds state for a single Freebuff auth token session.
type TokenSession struct {
	Token      string
	InstanceID string
	Model      string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	InUse      bool
	Locked     bool // true if session is model-locked
}

// Pool manages multiple token sessions with round-robin rotation.
// Ported from the freebuff-unified TokenPool pattern — provides
// multi-account rotation across auth tokens with mutex locking.
//
// Features:
//   - Round-robin across auth tokens
//   - Per-model session tracking
//   - Locked model detection (for /healthz visibility)
//   - Thread-safe operations
type Pool struct {
	mu       sync.RWMutex
	tokens   []string
	sessions map[string]*TokenSession // key = token
	maxSize  int
	ttl      time.Duration
	next     int // round-robin index
}

// NewPool creates a new TokenPool with the given auth tokens.
// maxSize limits concurrent sessions (default: 10).
// ttl controls session expiry (default: 55 min).
func NewPool(tokens []string, maxSize int, ttl time.Duration) *Pool {
	if maxSize <= 0 {
		maxSize = 10
	}
	if ttl <= 0 {
		ttl = 55 * time.Minute
	}
	return &Pool{
		tokens:   tokens,
		sessions: make(map[string]*TokenSession),
		maxSize:  maxSize,
		ttl:      ttl,
	}
}

// Acquire gets an available session for the given model, creating a new
// one if needed. Returns an error if all tokens are exhausted.
func (p *Pool) Acquire(ctx context.Context, model string) (*TokenSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 1. Try to reuse an existing fresh session for this model.
	for _, s := range p.sessions {
		if s.Model == model && time.Since(s.CreatedAt) < p.ttl && !s.InUse {
			s.InUse = true
			return s, nil
		}
	}

	// 2. Find a token without an active session (round-robin).
	start := p.next
	for i := 0; i < len(p.tokens); i++ {
		idx := (start + i) % len(p.tokens)
		tok := p.tokens[idx]
		if _, exists := p.sessions[tok]; !exists {
			p.next = (idx + 1) % len(p.tokens)
			s := &TokenSession{
				Token:     tok,
				Model:     model,
				CreatedAt: time.Now(),
				InUse:     true,
			}
			p.sessions[tok] = s
			return s, nil
		}
	}

	// 3. All tokens have active sessions — try to evict the oldest unused.
	var oldestKey string
	var oldestTime time.Time
	for tok, s := range p.sessions {
		if !s.InUse && (oldestKey == "" || s.CreatedAt.Before(oldestTime)) {
			oldestKey = tok
			oldestTime = s.CreatedAt
		}
	}
	if oldestKey != "" {
		delete(p.sessions, oldestKey)
		s := &TokenSession{
			Token:     oldestKey,
			Model:     model,
			CreatedAt: time.Now(),
			InUse:     true,
		}
		p.sessions[oldestKey] = s
		return s, nil
	}

	return nil, fmt.Errorf("token pool: no available tokens for model %s", model)
}

// Release marks a session as no longer in use.
func (p *Pool) Release(s *TokenSession) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s != nil {
		s.InUse = false
	}
}

// MarkLocked marks a session as model-locked (stuck on a model).
func (p *Pool) MarkLocked(s *TokenSession) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s != nil {
		s.Locked = true
	}
}

// MarkTokenLocked looks up a session by auth token string and marks it
// as model-locked. This is the preferred method when the caller only
// has the token string (e.g., from the session manager callback).
func (p *Pool) MarkTokenLocked(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.sessions[token]; ok {
		s.Locked = true
	}
}

// Remove deletes a session (e.g., on expiry or error).
func (p *Pool) Remove(token string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.sessions, token)
}

// Len returns the current number of active sessions.
func (p *Pool) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.sessions)
}

// LenLocked returns a map of model → locked session count.
// Operators use this in /healthz to see when tokens are stuck on
// unexpected models, indicating a session recovery issue.
func (p *Pool) LenLocked() map[string]int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make(map[string]int)
	for _, s := range p.sessions {
		if s.Locked {
			result[s.Model]++
		}
	}
	return result
}

// Stats returns pool statistics for monitoring.
func (p *Pool) Stats() any {
	if p == nil {
		return map[string]any{"configured": false}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	totalTokens := len(p.tokens)
	activeSessions := len(p.sessions)
	inUse := 0
	locked := 0
	byModel := make(map[string]int)
	for _, s := range p.sessions {
		if s.InUse {
			inUse++
		}
		if s.Locked {
			locked++
		}
		byModel[s.Model]++
	}
	return map[string]any{
		"total_tokens":    totalTokens,
		"active_sessions": activeSessions,
		"in_use":          inUse,
		"locked":          locked,
		"by_model":        byModel,
	}
}
