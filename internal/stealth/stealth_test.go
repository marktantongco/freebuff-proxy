package stealth

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// ── Profile Tests ──────────────────────────────────────────────────────────

func TestProfileFromID(t *testing.T) {
	tests := []struct {
		id      ProfileID
		wantNil bool
	}{
		{ProfileIDChrome120, false},
		{ProfileIDSafari17, false},
		{ProfileIDFirefox120, false},
		{"nonexistent", true},
		{"", true},
	}

	for _, tt := range tests {
		t.Run(string(tt.id), func(t *testing.T) {
			got := ProfileFromID(tt.id)
			if tt.wantNil && got != nil {
				t.Errorf("ProfileFromID(%q) = %v, want nil", tt.id, got)
			}
			if !tt.wantNil && got == nil {
				t.Errorf("ProfileFromID(%q) = nil, want non-nil", tt.id)
			}
		})
	}
}

func TestAllProfiles(t *testing.T) {
	all := AllProfiles()
	if len(all) < 3 {
		t.Errorf("AllProfiles() returned %d profiles, want at least 3", len(all))
	}

	seen := make(map[ProfileID]bool)
	for _, p := range all {
		if p == nil {
			t.Error("AllProfiles() contains nil profile")
			continue
		}
		if seen[p.ID] {
			t.Errorf("Duplicate profile ID: %s", p.ID)
		}
		seen[p.ID] = true

		if p.UserAgent == "" {
			t.Errorf("Profile %s has empty UserAgent", p.ID)
		}
		if p.AcceptLanguage == "" {
			t.Errorf("Profile %s has empty AcceptLanguage", p.ID)
		}
	}
}

func TestRandomPick(t *testing.T) {
	picked := make(map[ProfileID]bool)
	for i := 0; i < 30; i++ {
		p := RandomPick()
		if p == nil {
			t.Fatal("RandomPick() returned nil")
		}
		picked[p.ID] = true
	}
	// After 30 picks we should see at least 2 different profiles.
	if len(picked) < 2 {
		t.Errorf("RandomPick() only returned %d unique profiles after 30 tries", len(picked))
	}
}

func TestProfileDefaults(t *testing.T) {
	if DefaultProfile == nil {
		t.Fatal("DefaultProfile is nil")
	}
	if DefaultProfile.ClientHelloID.Str() == "" {
		t.Error("DefaultProfile has empty ClientHelloID")
	}
	if DefaultProfile.UserAgent == "" {
		t.Error("DefaultProfile has empty UserAgent")
	}
}

// ── HeaderSanitizer Tests ──────────────────────────────────────────────────

func TestHeaderSanitizer_StripProxyHeaders(t *testing.T) {
	hs := NewHeaderSanitizer(ProfileChrome120)

	h := make(http.Header)
	h.Set("X-Forwarded-For", "10.0.0.1")
	h.Set("Via", "1.1 proxy.example.com")
	h.Set("CF-Connecting-IP", "203.0.113.1")
	h.Set("User-Agent", "Go-http-client/2.0")
	h.Set("Accept", "application/json")

	removed := hs.Clean(h)

	if len(removed) < 3 {
		t.Errorf("Expected at least 3 headers removed, got %d: %v", len(removed), removed)
	}

	if v := h.Get("X-Forwarded-For"); v != "" {
		t.Errorf("X-Forwarded-For not stripped: %s", v)
	}
	if v := h.Get("Via"); v != "" {
		t.Errorf("Via not stripped: %s", v)
	}
	if v := h.Get("CF-Connecting-IP"); v != "" {
		t.Errorf("CF-Connecting-IP not stripped: %s", v)
	}
}

func TestHeaderSanitizer_InjectBrowserHeaders(t *testing.T) {
	hs := NewHeaderSanitizer(ProfileChrome120)

	h := make(http.Header)
	hs.Inject(h, true)

	if ua := h.Get("User-Agent"); ua == "" {
		t.Error("User-Agent not injected")
	} else if !strings.Contains(ua, "Chrome/120") {
		t.Errorf("User-Agent doesn't look like Chrome 120: %s", ua)
	}

	if accept := h.Get("Accept"); accept == "" {
		t.Error("Accept header not injected")
	}
	if lang := h.Get("Accept-Language"); lang == "" {
		t.Error("Accept-Language not injected")
	}
	if sec := h.Get("Sec-CH-UA"); sec == "" {
		t.Error("Sec-CH-UA not injected for Chrome profile")
	}
}

func TestHeaderSanitizer_SafariNoSecChUA(t *testing.T) {
	hs := NewHeaderSanitizer(ProfileSafari17)

	h := make(http.Header)
	hs.Inject(h, true)

	if sec := h.Get("Sec-CH-UA"); sec != "" {
		t.Errorf("Safari profile injected Sec-CH-UA: %s", sec)
	}
	if ua := h.Get("User-Agent"); ua == "" {
		t.Error("User-Agent not injected for Safari")
	} else if !strings.Contains(ua, "Safari/605") {
		t.Errorf("User-Agent doesn't look like Safari: %s", ua)
	}
}

func TestHeaderSanitizer_PreserveHeader(t *testing.T) {
	hs := NewHeaderSanitizer(ProfileChrome120, PreserveHeader("X-Forwarded-For"))

	h := make(http.Header)
	h.Set("X-Forwarded-For", "10.0.0.1")

	removed := hs.Clean(h)
	for _, r := range removed {
		if strings.HasPrefix(r, "X-Forwarded-For") {
			t.Errorf("X-Forwarded-For was removed despite PreserveHeader: %s", r)
		}
	}

	if v := h.Get("X-Forwarded-For"); v != "10.0.0.1" {
		t.Errorf("X-Forwarded-For was changed: got %q, want %q", v, "10.0.0.1")
	}
}

func TestHeaderSanitizer_CustomUserAgent(t *testing.T) {
	hs := NewHeaderSanitizer(ProfileFirefox120, WithCustomUserAgent("MyCustomBot/1.0"))

	h := make(http.Header)
	hs.Inject(h, true)

	if ua := h.Get("User-Agent"); ua != "MyCustomBot/1.0" {
		t.Errorf("Custom User-Agent not applied: got %q, want %q", ua, "MyCustomBot/1.0")
	}

	// Without force, should not overwrite existing.
	h2 := make(http.Header)
	h2.Set("User-Agent", "ExistingUA")
	hs.Inject(h2, false)
	if ua := h2.Get("User-Agent"); ua != "ExistingUA" {
		t.Errorf("Inject overwrote existing header without force: got %q, want %q", ua, "ExistingUA")
	}
}

func TestHeaderSanitizer_Sanitize(t *testing.T) {
	hs := NewHeaderSanitizer(ProfileChrome120)

	h := make(http.Header)
	h.Set("X-Forwarded-For", "10.0.0.1")
	// Do NOT pre-set User-Agent — Sanitize should inject a browser UA.

	removed := hs.Sanitize(h)

	if len(removed) == 0 {
		t.Error("Sanitize() returned no removed headers")
	}

	// X-Forwarded-For should be stripped.
	if v := h.Get("X-Forwarded-For"); v != "" {
		t.Errorf("X-Forwarded-For not stripped after Sanitize: %s", v)
	}

	// User-Agent should be set to a browser value.
	if ua := h.Get("User-Agent"); ua == "" {
		t.Error("User-Agent not set after Sanitize")
	} else if strings.Contains(ua, "Go-http-client") {
		t.Errorf("User-Agent still shows Go: %s", ua)
	}
}

func TestCheckIdentifiers(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		wantFind bool
	}{
		{
			name:     "Go client",
			headers:  map[string]string{"User-Agent": "Go-http-client/2.0"},
			wantFind: true,
		},
		{
			name:     "Python requests",
			headers:  map[string]string{"User-Agent": "python-requests/2.31.0"},
			wantFind: true,
		},
		{
			name:     "Curl",
			headers:  map[string]string{"User-Agent": "curl/8.0.1"},
			wantFind: true,
		},
		{
			name:     "Browser (clean)",
			headers:  map[string]string{"User-Agent": "Mozilla/5.0 Chrome/120"},
			wantFind: false,
		},
		{
			name:     "Proxy headers present",
			headers:  map[string]string{"X-Forwarded-For": "10.0.0.1"},
			wantFind: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := make(http.Header)
			for k, v := range tt.headers {
				h.Set(k, v)
			}
			findings := CheckIdentifiers(h)
			if tt.wantFind && len(findings) == 0 {
				t.Errorf("CheckIdentifiers() = nil, expected findings for %s", tt.name)
			}
			if !tt.wantFind && len(findings) > 0 {
				t.Errorf("CheckIdentifiers() = %v, expected no findings for %s", findings, tt.name)
			}
		})
	}
}

// ── Randomizer Tests ───────────────────────────────────────────────────────

func TestNewRandomizer_Defaults(t *testing.T) {
	r := NewRandomizer()
	if r == nil {
		t.Fatal("NewRandomizer() returned nil")
	}

	start := time.Now()
	r.Wait()
	elapsed := time.Since(start)

	if elapsed < 0 {
		t.Error("Wait() returned negative elapsed time")
	}
}

func TestNewRandomizer_CustomDelays(t *testing.T) {
	r := NewRandomizer(
		WithMinDelay(10*time.Millisecond),
		WithMaxDelay(50*time.Millisecond),
	)

	start := time.Now()
	r.Wait()
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Errorf("Wait() took %v, expected at most 100ms", elapsed)
	}
}

func TestRandomizer_Reset(t *testing.T) {
	r := NewRandomizer()
	r.Reset() // Should not panic.

	start := time.Now()
	r.Wait()
	elapsed := time.Since(start)

	if elapsed < 0 {
		t.Error("After Reset(), Wait() returned negative elapsed time")
	}
}

func TestJitterDuration(t *testing.T) {
	base := 100 * time.Millisecond

	for i := 0; i < 20; i++ {
		got := JitterDuration(base)
		min := base - time.Duration(float64(base)*0.2)
		max := base + time.Duration(float64(base)*0.2)

		if got < min || got > max {
			t.Errorf("JitterDuration(%v) = %v, want in [%v, %v]", base, got, min, max)
		}
	}
}

func TestJitterDuration_Zero(t *testing.T) {
	got := JitterDuration(0)
	if got != 0 {
		t.Errorf("JitterDuration(0) = %v, want 0", got)
	}
}

func TestHumanDelay(t *testing.T) {
	// Very short content should return at least 100ms.
	short := HumanDelay(10)
	if short < 100*time.Millisecond {
		t.Errorf("HumanDelay(10) = %v, want >= 100ms", short)
	}

	// Zero content should return between 500ms and 2s.
	zero := HumanDelay(0)
	if zero < 500*time.Millisecond || zero > 2*time.Second {
		t.Errorf("HumanDelay(0) = %v, want in [500ms, 2s]", zero)
	}

	// Longer content should produce longer delay on average.
	// Run multiple iterations to account for randomness.
	var shortTotal, longTotal time.Duration
	iterations := 10
	for i := 0; i < iterations; i++ {
		shortTotal += HumanDelay(100)
		longTotal += HumanDelay(10000)
	}
	shortAvg := shortTotal / time.Duration(iterations)
	longAvg := longTotal / time.Duration(iterations)
	if longAvg < shortAvg {
		t.Errorf("HumanDelay avg over %d iterations: short=%v, long=%v; expected long > short",
			iterations, shortAvg, longAvg)
	}
}

// ── Client Tests ───────────────────────────────────────────────────────────

func TestDefaultClientConfig(t *testing.T) {
	cfg := DefaultClientConfig()
	if cfg == nil {
		t.Fatal("DefaultClientConfig() returned nil")
	}
	if cfg.Profile == nil {
		t.Error("DefaultClientConfig().Profile is nil")
	}
	if cfg.Timeout != 180*time.Second {
		t.Errorf("DefaultClientConfig().Timeout = %v, want 180s", cfg.Timeout)
	}
	if !cfg.SanitizeHeaders {
		t.Error("DefaultClientConfig().SanitizeHeaders is false, want true")
	}
}

func TestNewClient_Defaults(t *testing.T) {
	client := NewDefaultClient()
	if client == nil {
		t.Fatal("NewDefaultClient() returned nil")
	}
	if client.Timeout != 180*time.Second {
		t.Errorf("NewDefaultClient().Timeout = %v, want 180s", client.Timeout)
	}
	if client.Transport == nil {
		t.Error("NewDefaultClient().Transport is nil")
	}
}

func TestNewClient_WithCustomProfile(t *testing.T) {
	client := NewClient(ClientConfig{
		Profile: ProfileFirefox120,
	})
	if client == nil {
		t.Fatal("NewClient(ProfileFirefox120) returned nil")
	}
	if client.Transport == nil {
		t.Error("Client.Transport is nil")
	}
}

func TestNewClient_WithExistingRoundTripper(t *testing.T) {
	base := http.DefaultTransport
	client := NewClient(ClientConfig{
		Profile:         ProfileChrome120,
		RoundTripper:    base,
		SanitizeHeaders: true,
	})
	if client == nil {
		t.Fatal("NewClient() with RoundTripper returned nil")
	}
}

func TestNewClient_WithJitter(t *testing.T) {
	client := NewClient(ClientConfig{
		Profile:      ProfileChrome120,
		EnableJitter: true,
		JitterMin:    10 * time.Millisecond,
		JitterMax:    50 * time.Millisecond,
	})
	if client == nil {
		t.Fatal("NewClient() with jitter returned nil")
	}
}

// ── Profile User-Agent Tests ───────────────────────────────────────────────

func TestRandomUserAgent(t *testing.T) {
	ua := randomUserAgent()
	if ua == "" {
		t.Error("randomUserAgent() returned empty string")
	}
	if !strings.Contains(ua, "Mozilla/5.0") {
		t.Errorf("randomUserAgent() = %q, expected Mozilla/5.0 prefix", ua)
	}
}

func TestProfileRandomUserAgent(t *testing.T) {
	if ProfileRandom.UserAgent == "" {
		t.Error("ProfileRandom.UserAgent is empty")
	}
	if !strings.Contains(ProfileRandom.UserAgent, "Mozilla/5.0") {
		t.Errorf("ProfileRandom.UserAgent = %q, expected Mozilla/5.0", ProfileRandom.UserAgent)
	}
}
