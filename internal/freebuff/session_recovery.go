package freebuff

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ── Session Recovery ─────────────────────────────────────────────────────────
//
// Ported from codebuff-proxy (TypeScript) patterns:
//
//	shouldRetryFreshFreebuffSession() — triggers on 409, 410, 426, 428, 429
//	retryWithFreshFreebuffSession()   — POST → poll → inject → retry chat
//	acquireFreebuffSessionInstanceId() — session lifecycle management
//
// These status codes indicate the freebuff session needs recovery:
//   409: session conflict (e.g., another instance took over)
//   410: session expired
//   426: upgrade required (waiting-room is active)
//   428: precondition required (session not ready)
//   429: rate limited (too many requests)

// SessionRecoveryStatuses lists the upstream status codes that trigger a
// freebuff session auto-recovery flow.
var SessionRecoveryStatuses = []int{409, 410, 426, 428, 429}

// SessionRecoveryError wraps an upstream chat error that should trigger session recovery.
type SessionRecoveryError struct {
	StatusCode int
	Message    string
}

func (e *SessionRecoveryError) Error() string {
	return fmt.Sprintf("freebuff session recovery required (HTTP %d): %s", e.StatusCode, e.Message)
}

// ShouldRecoverSession checks whether the given status code should trigger
// a freebuff session auto-recovery flow.
func ShouldRecoverSession(statusCode int, freeMode bool) bool {
	if !freeMode {
		return false
	}
	for _, code := range SessionRecoveryStatuses {
		if statusCode == code {
			return true
		}
	}
	return false
}

// SessionRecoveryConfig configures how the proxy recovers from session errors.
type SessionRecoveryConfig struct {
	// Enabled controls whether auto-recovery is active. Default: true.
	Enabled bool

	// WaitTimeout is the maximum time to wait for a session to become active. Default: 2 min.
	WaitTimeout time.Duration

	// PollInterval is how often to poll the session endpoint. Default: 5s.
	PollInterval time.Duration

	// MaxRetries is the maximum number of recovery attempts. Default: 1.
	MaxRetries int
}

// DefaultSessionRecoveryConfig returns sensible defaults.
func DefaultSessionRecoveryConfig() SessionRecoveryConfig {
	return SessionRecoveryConfig{
		Enabled:      true,
		WaitTimeout:  2 * time.Minute,
		PollInterval: 5 * time.Second,
		MaxRetries:   1,
	}
}

// RecoverSessionConfig holds configuration for session recovery.
type RecoverSessionConfig struct {
	InstanceID   string
	WaitTimeout  time.Duration
	PollInterval time.Duration
}

// DefaultRecoverSessionConfig returns sensible defaults for session recovery.
func DefaultRecoverSessionConfig() RecoverSessionConfig {
	return RecoverSessionConfig{
		InstanceID:   "freebuff-proxy-recovery",
		WaitTimeout:  2 * time.Minute,
		PollInterval: 5 * time.Second,
	}
}

// RecoverSession attempts to recover a freebuff session by:
//  1. Calling POST /api/v1/freebuff/session to request/refresh a session
//  2. Polling GET /api/v1/freebuff/session until active (or timeout)
//  3. Returning the new session on success
//
// Ported from codebuff-proxy's acquireFreebuffSessionInstanceId().
func (c *Client) RecoverSession(ctx context.Context, token string, model string, cfg RecoverSessionConfig) (Session, error) {
	instanceID := cfg.InstanceID
	if instanceID == "" {
		instanceID = "freebuff-proxy-recovery"
	}

	// Step 1: POST to request a new session.
	session, err := c.StartSession(ctx, token, instanceID, model)
	if err != nil {
		// If POST fails, try GET to reuse existing session.
		existing, getErr := c.GetSession(ctx, token, instanceID)
		if getErr == nil && existing.InstanceID != "" {
			return existing, nil
		}
		return Session{}, fmt.Errorf("start recovery session: %w", err)
	}

	// Step 2: If already active, return immediately.
	if session.Status == SessionActive {
		return session, nil
	}

	// Step 3: Poll until active.
	if session.Status == SessionQueued {
		return c.pollSessionUntilActive(ctx, token, session, cfg)
	}

	return session, nil
}

// pollSessionUntilActive polls the session endpoint until the session becomes
// active or the context is cancelled / timeout is reached.
//
// Ported from codebuff-proxy's polling loop in acquireFreebuffSessionInstanceId().
func (c *Client) pollSessionUntilActive(ctx context.Context, token string, session Session, cfg RecoverSessionConfig) (Session, error) {
	instanceID := session.InstanceID
	if instanceID == "" {
		instanceID = cfg.InstanceID
	}

	timeout := cfg.WaitTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	interval := cfg.PollInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}

	deadline := time.Now().Add(timeout)
	current := session

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return current, ctx.Err()
		case <-time.After(interval):
		}

		next, err := c.GetSession(ctx, token, instanceID)
		if err != nil {
			return current, fmt.Errorf("poll session: %w", err)
		}

		current = next

		switch current.Status {
		case SessionActive:
			return current, nil
		case SessionQueued:
			// Continue polling.
			continue
		case SessionDisabled, SessionCountryBlocked, SessionBanned, SessionRateLimited:
			return current, fmt.Errorf("session unrecoverable: %s", current.Status)
		}
	}

	return current, fmt.Errorf("session did not become active within %v", timeout)
}

// RetryWithFreshSession attempts to recover a freebuff session and retry the
// chat request. Returns the new session and a boolean indicating success.
//
// Ported from codebuff-proxy's retryWithFreshFreebuffSession() pattern.
func (c *Client) RetryWithFreshSession(ctx context.Context, token string, model string, cfg RecoverSessionConfig) (Session, bool) {
	session, err := c.RecoverSession(ctx, token, model, cfg)
	if err != nil {
		return Session{}, false
	}
	if session.Status != SessionActive {
		return Session{}, false
	}
	return session, true
}

// sessionRecoveryCodes are the upstream error codes that indicate a session
// needs recovery. Only these (not all 409/429 responses) trigger recovery.
var sessionRecoveryCodes = map[string]bool{
	"freebuff_update_required": true,
	"session_expired":          true,
	"session_model_mismatch":   true,
	"session_superseded":       true,
	"waiting_room_queued":      true,
	"waiting_room_required":    true,
}

// ShouldRecoverSessionFromResponse checks whether an upstream chat response
// should trigger session recovery. It requires BOTH:
//   - The HTTP status code is a known recovery trigger (409, 410, 426, 428, 429)
//   - The response body contains a session-specific error code
//     (e.g., "session_expired", "waiting_room_queued")
//
// The body parameter should be the raw response bytes. If empty, no recovery.
func ShouldRecoverSessionFromResponse(statusCode int, body []byte) bool {
	if !ShouldRecoverSession(statusCode, true) {
		return false
	}
	if len(body) == 0 {
		return false
	}

	var payload struct {
		Code  string `json:"code"`
		Error any   `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}

	// Check top-level "code" field.
	if sessionRecoveryCodes[payload.Code] {
		return true
	}

	// Check "error" as a string.
	if errorStr, ok := payload.Error.(string); ok && sessionRecoveryCodes[errorStr] {
		return true
	}

	// Check "error.code" as nested object.
	if errorObj, ok := payload.Error.(map[string]any); ok {
		if code, ok := errorObj["code"].(string); ok && sessionRecoveryCodes[code] {
			return true
		}
	}

	return false
}

// IsSessionRecoveryError checks if an error is a session recovery error
// by looking for known recovery status codes in the error message.
func IsSessionRecoveryError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, code := range SessionRecoveryStatuses {
		if strings.Contains(msg, fmt.Sprintf("HTTP %d", code)) {
			return true
		}
	}
	return false
}
