package stealth

import (
	"fmt"
	"net/http"
	"strings"
)

// HeaderSanitizer cleans outbound HTTP headers to remove proxy fingerprints
// and inject browser-typical headers. It operates as both a validator (check
// that headers are clean) and a mutator (apply the profile's header set).
type HeaderSanitizer struct {
	profile    *Profile
	customUA   string
	extra      map[string]string
	preserve   []string // headers to keep even if they'd normally be stripped
}

// HeaderSanitizerOption configures a HeaderSanitizer.
type HeaderSanitizerOption func(*HeaderSanitizer)

// WithCustomUserAgent overrides the profile's default User-Agent.
func WithCustomUserAgent(ua string) HeaderSanitizerOption {
	return func(hs *HeaderSanitizer) {
		hs.customUA = ua
	}
}

// WithExtraHeader adds an additional header to inject on every request.
func WithExtraHeader(key, value string) HeaderSanitizerOption {
	return func(hs *HeaderSanitizer) {
		if hs.extra == nil {
			hs.extra = make(map[string]string)
		}
		hs.extra[key] = value
	}
}

// PreserveHeader prevents the sanitizer from stripping the given header.
func PreserveHeader(key string) HeaderSanitizerOption {
	return func(hs *HeaderSanitizer) {
		hs.preserve = append(hs.preserve, strings.ToLower(key))
	}
}

// NewHeaderSanitizer creates a new HeaderSanitizer from the given profile.
//
// The sanitizer will:
//   - Remove proxy-identifying headers (X-Forwarded-For, Via, etc.)
//   - Inject the profile's User-Agent and Sec-CH-UA headers
//   - Set Accept, Accept-Language, and Accept-Encoding to browser values
//   - Remove headers that real browsers don't send
func NewHeaderSanitizer(profile *Profile, opts ...HeaderSanitizerOption) *HeaderSanitizer {
	if profile == nil {
		profile = DefaultProfile
	}
	hs := &HeaderSanitizer{profile: profile}
	for _, opt := range opts {
		opt(hs)
	}
	return hs
}

// HeadersToStrip lists headers that identify HTTP clients as proxies or
// automation tools. Real browsers never send these.
var HeadersToStrip = []string{
	"X-Forwarded-For",
	"X-Forwarded-Proto",
	"X-Forwarded-Host",
	"X-Real-IP",
	"X-Proxy-User-IP",
	"Via",
	"X-Via",
	"Proxy-Connection",
	"X-Proxy-Agent",
	"X-Request-ID",
	"CF-Connecting-IP",
	"CF-IPCountry",
	"CF-Ray",
	"CF-Visitor",
	"True-Client-IP",
	"X-Originating-IP",
	"X-Remote-IP",
	"X-Remote-Addr",
	"X-Client-IP",
	"X-Host",
	"X-Correlation-ID",
	"X-Trace-ID",
	"X-Amzn-Trace-Id",
	"X-Cache",
	"X-Served-By",
}

// HeadersToInject lists headers that real browsers typically send.
// Missing these can flag a client as non-browser.
var HeadersToInject = []string{
	"Accept",
	"Accept-Language",
	"Accept-Encoding",
	"User-Agent",
	"Sec-CH-UA",
	"Sec-CH-UA-Platform",
	"Sec-CH-UA-Mobile",
	"Sec-Fetch-Site",
	"Sec-Fetch-Mode",
	"Sec-Fetch-Dest",
	"Upgrade-Insecure-Requests",
}

// Clean removes proxy-identifying headers from the request and returns
// a list of what was removed.
func (hs *HeaderSanitizer) Clean(h http.Header) []string {
	var removed []string
	preserve := make(map[string]bool)
	for _, k := range hs.preserve {
		preserve[k] = true
	}

	for _, hdr := range HeadersToStrip {
		lower := strings.ToLower(hdr)
		if preserve[lower] {
			continue
		}
		if v := h.Get(hdr); v != "" {
			h.Del(hdr)
			removed = append(removed, fmt.Sprintf("%s: %s", hdr, v))
		}
		// Also check lowercase (some proxies send lowercase headers)
		if v := h.Get(lower); v != "" && !preserve[lower] {
			h.Del(lower)
		}
	}

	return removed
}

// Inject adds browser-typical headers to the request based on the profile.
// It will not overwrite existing headers unless force is true.
func (hs *HeaderSanitizer) Inject(h http.Header, force bool) {
	setOrSkip := func(key, value string) {
		if value == "" {
			// Don't inject empty headers; they'd be an identifiable pattern.
			// If a caller sets CustomUserAgent, we use it; otherwise we skip.
			return
		}
		if force || h.Get(key) == "" {
			h.Set(key, value)
		}
	}

	// Determine User-Agent.
	ua := hs.profile.UserAgent
	if hs.customUA != "" {
		ua = hs.customUA
	}

	// Always set User-Agent; if both profile and custom are empty,
	// generate a random one to avoid an identifiable blank UA.
	if ua == "" {
		ua = randomUserAgent()
	}
	setOrSkip("User-Agent", ua)

	// Inject Sec-CH-UA headers (Chrome-derived browsers only).
	if hs.profile.SecChUA != "" {
		setOrSkip("Sec-CH-UA", hs.profile.SecChUA)
	}
	if hs.profile.SecChUAPlatform != "" {
		setOrSkip("Sec-CH-UA-Platform", hs.profile.SecChUAPlatform)
	}
	setOrSkip("Sec-CH-UA-Mobile", "?0")

	// Standard browser headers.
	setOrSkip("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	setOrSkip("Accept-Language", hs.profile.AcceptLanguage)
	setOrSkip("Accept-Encoding", hs.profile.AcceptEncoding)

	// Sec-Fetch headers (browser-initiated request metadata).
	setOrSkip("Sec-Fetch-Site", "cross-site")
	setOrSkip("Sec-Fetch-Mode", "navigate")
	setOrSkip("Sec-Fetch-Dest", "document")
	setOrSkip("Upgrade-Insecure-Requests", "1")

	// Extra user-defined headers.
	for k, v := range hs.extra {
		h.Set(k, v)
	}
}

// Sanitize performs a full clean + inject on the given headers.
// Returns the list of removed headers for logging/inspection.
func (hs *HeaderSanitizer) Sanitize(h http.Header) []string {
	removed := hs.Clean(h)
	hs.Inject(h, false)
	return removed
}

// CheckIdentifiers scans headers for patterns that identify automation
// or proxy tools. Returns a list of suspicious header values found.
func CheckIdentifiers(h http.Header) []string {
	var findings []string

	// Check for Go's default User-Agent.
	if ua := h.Get("User-Agent"); strings.Contains(strings.ToLower(ua), "go-http-client") {
		findings = append(findings, fmt.Sprintf("Go default User-Agent detected: %s", ua))
	}

	// Check for Python requests.
	if ua := h.Get("User-Agent"); strings.Contains(strings.ToLower(ua), "python-requests") {
		findings = append(findings, fmt.Sprintf("Python requests User-Agent detected: %s", ua))
	}

	// Check for curl.
	if ua := h.Get("User-Agent"); strings.HasPrefix(strings.ToLower(ua), "curl/") {
		findings = append(findings, fmt.Sprintf("curl User-Agent detected: %s", ua))
	}

	// Check for known proxy/automation headers.
	proxyHeaders := []string{"X-Forwarded-For", "Via", "X-Real-IP", "CF-Connecting-IP"}
	for _, hdr := range proxyHeaders {
		if v := h.Get(hdr); v != "" {
			findings = append(findings, fmt.Sprintf("Proxy header present: %s: %s", hdr, v))
		}
	}

	return findings
}
