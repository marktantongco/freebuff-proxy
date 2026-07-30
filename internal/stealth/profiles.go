// Package stealth provides JA3 TLS fingerprint impersonation, header sanitization,
// and timing jitter for HTTP clients. It is a drop-in replacement for http.Client
// that makes requests indistinguishable from real browsers at the transport layer.
//
// Usage:
//
//	client := stealth.NewClient(stealth.ProfileChrome120)
//	resp, err := client.Get("https://example.com")
//
// Available profiles:
//   - ProfileChrome120 — mimics Chrome 120 on Windows
//   - ProfileSafari17  — mimics Safari 17 on macOS (via randomized ALPN)
//   - ProfileFirefox120 — mimics Firefox 120 on Linux
package stealth

import (
	"math/rand"
	"time"

	utls "github.com/refraction-networking/utls"
)

// ProfileID is a unique identifier for a browser fingerprint profile.
type ProfileID string

const (
	ProfileIDChrome120   ProfileID = "chrome120"
	ProfileIDSafari17    ProfileID = "safari17"
	ProfileIDFirefox120  ProfileID = "firefox120"
)

// Profile defines a complete browser TLS fingerprint including:
//   - The utls ClientHelloID used for TLS handshake impersonation
//   - Realistic User-Agent string matching the browser
//   - Associated Accept and Sec-CH-UA headers
//   - Optional CustomSpec for precise ClientHello fingerprint control
type Profile struct {
	// ID is the unique identifier for this profile.
	ID ProfileID

	// ClientHelloID is the utls preset that controls the TLS fingerprint.
	// Used when CustomSpec is nil.
	ClientHelloID utls.ClientHelloID

	// CustomSpec provides an exact ClientHello specification for precise
	// TLS fingerprint control. When non-nil, this is used with utls.HelloCustom
	// via ApplyPreset() instead of the ClientHelloID preset.
	//
	// This is useful for profiles where utls doesn't have a matching preset
	// (e.g., Safari 17) or when you need exact control over cipher suites,
	// curves, and extensions.
	CustomSpec *utls.ClientHelloSpec

	// UserAgent is the browser User-Agent header value.
	UserAgent string

	// SecChUA is the Sec-CH-UA header value for this browser.
	SecChUA string

	// SecChUAPlatform is the Sec-CH-UA-Platform header value.
	SecChUAPlatform string

	// AcceptLanguage is the Accept-Language header value.
	AcceptLanguage string

	// AcceptEncoding is the Accept-Encoding header value.
	AcceptEncoding string
}

// Pre-built browser profiles.
//
// Each profile embeds a utls ClientHelloID that corresponds to a known
// browser TLS fingerprint. These presets configure cipher suites,
// elliptic curves, signature algorithms, compression methods, and
// TLS extensions to exactly match the target browser's TLS handshake.
//
// To verify fingerprints against a live endpoint:
//
//	curl -x http://127.0.0.1:9090 https://tls.peet.ws/api/all
var (
	// ProfileChrome120 mimics Chrome 120 on Windows 10.
	// Uses utls.HelloChrome_120 for exact JA3 impersonation.
	ProfileChrome120 = &Profile{
		ID:              ProfileIDChrome120,
		ClientHelloID:   utls.HelloChrome_120,
		UserAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		SecChUA:         `"Not_A Brand";v="8", "Chromium";v="120", "Google Chrome";v="120"`,
		SecChUAPlatform: `"Windows"`,
		AcceptLanguage:  "en-US,en;q=0.9",
		AcceptEncoding:  "gzip, deflate, br",
	}

	// ProfileSafari17 mimics Safari 17 on macOS Sonoma.
	// utls v1.6.7 does not have a dedicated Safari preset, so we use
	// a custom ClientHelloSpec with Safari-typical cipher suites and
	// ALPN for h2 + http/1.1, applied via utls.HelloCustom.
	ProfileSafari17 = &Profile{
		ID:            ProfileIDSafari17,
		ClientHelloID: utls.HelloCustom,
		CustomSpec:    safari17Spec(),
		UserAgent:     "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15",
		SecChUA:         "", // Safari does not send Sec-CH-UA
		SecChUAPlatform: "",
		AcceptLanguage:  "en-US,en;q=0.9",
		AcceptEncoding:  "gzip, deflate, br",
	}

	// ProfileFirefox120 mimics Firefox 120 on Linux.
	// Uses utls.HelloFirefox_120 for exact JA3 impersonation.
	ProfileFirefox120 = &Profile{
		ID:              ProfileIDFirefox120,
		ClientHelloID:   utls.HelloFirefox_120,
		UserAgent:       "Mozilla/5.0 (X11; Linux x86_64; rv:120.0) Gecko/20100101 Firefox/120.0",
		SecChUA:         "", // Firefox does not send Sec-CH-UA
		SecChUAPlatform: "",
		AcceptLanguage:  "en-US,en;q=0.5",
		AcceptEncoding:  "gzip, deflate, br",
	}

	// ProfileRandom picks a random fingerprint on each connection.
	// Use this to distribute requests across multiple fingerprints.
	// The User-Agent is randomized to match the fingerprint distribution.
	ProfileRandom = &Profile{
		ID:              "random",
		ClientHelloID:   utls.HelloRandomized,
		UserAgent:       randomUserAgent(),
		SecChUA:         "",
		SecChUAPlatform: "",
		AcceptLanguage:  "en-US,en;q=0.9",
		AcceptEncoding:  "gzip, deflate, br",
	}

	// DefaultProfile is the profile used when nil is passed to NewClient.
	DefaultProfile = ProfileChrome120
)

// randomUserAgent generates a randomized browser User-Agent by picking a
// known browser version string. This helps avoid fingerprinting via
// User-Agent rotation patterns and provides a safe fallback when the
// profile's User-Agent is empty.
func randomUserAgent() string {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	agents := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:120.0) Gecko/20100101 Firefox/120.0",
		"Mozilla/5.0 (X11; Linux x86_64; rv:120.0) Gecko/20100101 Firefox/120.0",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15",
	}
	return agents[rng.Intn(len(agents))]
}

// safari17Spec returns the custom ClientHelloSpec for Safari 17 on macOS.
// This provides precise TLS fingerprint control including ALPN for HTTP/2.
func safari17Spec() *utls.ClientHelloSpec {
	return &utls.ClientHelloSpec{
		CipherSuites: []uint16{
			utls.TLS_AES_128_GCM_SHA256,
			utls.TLS_AES_256_GCM_SHA384,
			utls.TLS_CHACHA20_POLY1305_SHA256,
			utls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			utls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			utls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			utls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			utls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			utls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			utls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
			utls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
			utls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
			utls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
			utls.TLS_RSA_WITH_AES_256_GCM_SHA384,
			utls.TLS_RSA_WITH_AES_128_GCM_SHA256,
			utls.TLS_RSA_WITH_AES_256_CBC_SHA,
			utls.TLS_RSA_WITH_AES_128_CBC_SHA,
		},
		CompressionMethods: []byte{0},
		Extensions: []utls.TLSExtension{
			&utls.SNIExtension{},
			&utls.ExtendedMasterSecretExtension{},
			&utls.RenegotiationInfoExtension{Renegotiation: utls.RenegotiateOnceAsClient},
			&utls.SupportedCurvesExtension{Curves: []utls.CurveID{
				utls.X25519,
				utls.CurveP256,
				utls.CurveP384,
				utls.CurveP521,
			}},
			&utls.SupportedPointsExtension{SupportedPoints: []byte{0}},
			&utls.ALPNExtension{AlpnProtocols: []string{"h2", "http/1.1"}},
			&utls.StatusRequestExtension{},
			&utls.SignatureAlgorithmsExtension{SupportedSignatureAlgorithms: []utls.SignatureScheme{
				utls.ECDSAWithP256AndSHA256,
				utls.PSSWithSHA256,
				utls.PKCS1WithSHA256,
				utls.ECDSAWithP384AndSHA384,
				utls.ECDSAWithSHA1,
				utls.PSSWithSHA384,
				utls.PSSWithSHA512,
				utls.PKCS1WithSHA384,
				utls.PKCS1WithSHA512,
				utls.PKCS1WithSHA1,
			}},
			&utls.SCTExtension{},
			&utls.KeyShareExtension{KeyShares: []utls.KeyShare{
				{Group: utls.X25519},
			}},
			&utls.SupportedVersionsExtension{Versions: []uint16{
				utls.GREASE_PLACEHOLDER,
				utls.VersionTLS13,
				utls.VersionTLS12,
			}},
			&utls.UtlsGREASEExtension{},
			&utls.UtlsPaddingExtension{GetPaddingLen: utls.BoringPaddingStyle},
		},
	}
}

// AllProfiles returns all non-random profiles for iteration.
func AllProfiles() []*Profile {
	return []*Profile{
		ProfileChrome120,
		ProfileSafari17,
		ProfileFirefox120,
	}
}

// ProfileFromID returns the profile matching the given ID, or nil if not found.
func ProfileFromID(id ProfileID) *Profile {
	switch id {
	case ProfileIDChrome120:
		return ProfileChrome120
	case ProfileIDSafari17:
		return ProfileSafari17
	case ProfileIDFirefox120:
		return ProfileFirefox120
	default:
		return nil
	}
}

// RandomPick returns a random profile from AllProfiles.
func RandomPick() *Profile {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	all := AllProfiles()
	return all[rng.Intn(len(all))]
}
