package stealth

import (
	"context"
	"net"
	"net/http"
	"time"
)

// ClientConfig configures a stealth http.Client.
type ClientConfig struct {
	// Profile is the browser fingerprint to impersonate. If nil, DefaultProfile is used.
	Profile *Profile

	// Timeout is the total client timeout. Default: 180s.
	Timeout time.Duration

	// MaxIdleConns is the max number of idle connections to keep alive. Default: 100.
	MaxIdleConns int

	// IdleConnTimeout is how long idle connections are kept. Default: 90s.
	IdleConnTimeout time.Duration

	// EnableJitter enables timing jitter between requests. Default: false.
	EnableJitter bool

	// JitterMin is the minimum delay when jitter is enabled. Default: 50ms.
	JitterMin time.Duration

	// JitterMax is the maximum delay when jitter is enabled. Default: 300ms.
	JitterMax time.Duration

	// SanitizeHeaders enables automatic header sanitization. Default: true.
	SanitizeHeaders bool

	// CustomUserAgent overrides the profile's User-Agent if set.
	CustomUserAgent string

	// ExtraHeaders are additional headers to inject on every request.
	ExtraHeaders map[string]string

	// PreserveHeaders lists headers that should NOT be stripped during sanitization.
	PreserveHeaders []string

	// RoundTripper is an optional base RoundTripper. If nil, the utls-based
	// transport is used. When set, only the headers/cleanup wrappers are applied.
	RoundTripper http.RoundTripper

	// Resolver is an optional custom TCP dial function for the underlying connection.
	Resolver func(ctx context.Context, network, addr string) (net.Conn, error)

	// ProxyPool is an optional pool of SOCKS5 proxies for rotating egress IPs.
	// When set, each outbound connection picks the next proxy from the pool
	// in round-robin order, providing IP rotation and geo-evasion.
	// The SOCKS5 connection is established first, then wrapped with utls for
	// JA3 TLS fingerprint impersonation.
	ProxyPool *ProxyPool
}

// DefaultClientConfig returns a ClientConfig with sensible defaults.
func DefaultClientConfig() *ClientConfig {
	return &ClientConfig{
		Profile:         DefaultProfile,
		Timeout:         180 * time.Second,
		MaxIdleConns:    100,
		IdleConnTimeout: 90 * time.Second,
		EnableJitter:    false,
		SanitizeHeaders: true,
	}
}

// NewClient creates a drop-in replacement for http.Client that:
//   - Impersonates a browser TLS fingerprint (JA3 evasion)
//   - Sanitizes outgoing headers (removes proxy identifiers, injects browser headers)
//   - Optionally adds timing jitter between requests
//
// Basic usage:
//
//	client := stealth.NewClient(stealth.ClientConfig{
//	    Profile: stealth.ProfileChrome120,
//	})
//	resp, err := client.Get("https://example.com")
func NewClient(cfg ClientConfig) *http.Client {
	if cfg.Profile == nil {
		cfg.Profile = DefaultProfile
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 180 * time.Second
	}
	if cfg.MaxIdleConns == 0 {
		cfg.MaxIdleConns = 100
	}
	if cfg.IdleConnTimeout == 0 {
		cfg.IdleConnTimeout = 90 * time.Second
	}

	// Build the transport chain from the inside out.
	var transport http.RoundTripper

	if cfg.RoundTripper != nil {
		transport = cfg.RoundTripper
	} else {
		transport = &http.Transport{
			DialTLSContext:  Dialer(cfg.Profile, cfg.Resolver, WithProxyPool(cfg.ProxyPool)),
			MaxIdleConns:    cfg.MaxIdleConns,
			IdleConnTimeout: cfg.IdleConnTimeout,
			ForceAttemptHTTP2: true,
		}
	}

	// Wrap with header sanitizer (middle layer).
	if cfg.SanitizeHeaders {
		sanitizerOpts := []HeaderSanitizerOption{}
		if cfg.CustomUserAgent != "" {
			sanitizerOpts = append(sanitizerOpts, WithCustomUserAgent(cfg.CustomUserAgent))
		}
		for k, v := range cfg.ExtraHeaders {
			sanitizerOpts = append(sanitizerOpts, WithExtraHeader(k, v))
		}
		for _, h := range cfg.PreserveHeaders {
			sanitizerOpts = append(sanitizerOpts, PreserveHeader(h))
		}
		sanitizer := NewHeaderSanitizer(cfg.Profile, sanitizerOpts...)
		transport = &headerRoundTripper{
			next:      transport,
			sanitizer: sanitizer,
		}
	}

	// Wrap with timing jitter (outermost layer).
	if cfg.EnableJitter {
		jitterMin := cfg.JitterMin
		if jitterMin == 0 {
			jitterMin = 50 * time.Millisecond
		}
		jitterMax := cfg.JitterMax
		if jitterMax == 0 {
			jitterMax = 300 * time.Millisecond
		}
		transport = &jitterRoundTripper{
			next:       transport,
			randomizer: NewRandomizer(WithMinDelay(jitterMin), WithMaxDelay(jitterMax)),
		}
	}

	return &http.Client{
		Transport: transport,
		Timeout:   cfg.Timeout,
	}
}

// NewDefaultClient returns a stealth http.Client with Chrome 120 fingerprint,
// header sanitization enabled, no jitter, and a 180s timeout.
//
// This is the simplest drop-in replacement for http.DefaultClient:
//
//	client := stealth.NewDefaultClient()
//	resp, err := client.Post("https://api.example.com/...", "application/json", body)
func NewDefaultClient() *http.Client {
	return NewClient(*DefaultClientConfig())
}

// ---- Internal RoundTripper Wrappers ----

// headerRoundTripper wraps an http.RoundTripper and applies header sanitization
// to every request before passing it downstream.
type headerRoundTripper struct {
	next      http.RoundTripper
	sanitizer *HeaderSanitizer
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	if req2.Header == nil {
		req2.Header = make(http.Header)
	}
	_ = h.sanitizer.Sanitize(req2.Header)
	return h.next.RoundTrip(req2)
}

// jitterRoundTripper wraps an http.RoundTripper and adds timing jitter
// before each RoundTrip call.
type jitterRoundTripper struct {
	next       http.RoundTripper
	randomizer *Randomizer
}

func (j *jitterRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	j.randomizer.Wait()
	return j.next.RoundTrip(req)
}


