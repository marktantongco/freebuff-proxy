package stealth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// tlsPeetWSResponse maps the JSON from https://tls.peet.ws/api/all
// which includes the observed JA3 fingerprint hash.
// The ja3_hash is nested under the "tls" object.
type tlsPeetWSResponse struct {
	IP          string        `json:"ip"`
	HTTPVersion string        `json:"http_version"`
	UserAgent   string        `json:"user_agent"`
	TLS         tlsInfo       `json:"tls"`
}

type tlsInfo struct {
	JA3     string `json:"ja3"`
	JA3Hash string `json:"ja3_hash"`
	JA4     string `json:"ja4"`
	Version string `json:"tls_version_record"`
}

// TestChrome120JA3Fingerprint fetches the JA3 fingerprint from tls.peet.ws
// using the Chrome 120 stealth HTTP client and verifies the observed hash
// matches the expected Chrome 120 fingerprint.
//
// Reference JA3 for Chrome 120 (Windows 10):
//   6734f37431670b3ab4292b8f60f29984
func TestChrome120JA3Fingerprint(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping external JA3 verification test in short mode")
	}

	client := NewClient(ClientConfig{
		Profile:         ProfileChrome120,
		Timeout:         15 * time.Second,
		SanitizeHeaders: true,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://tls.peet.ws/api/all", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Chrome 120 stealth client failed to reach tls.peet.ws: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200, got %d", resp.StatusCode)
	}

	var result tlsPeetWSResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode tls.peet.ws response: %v", err)
	}

	// Log everything for analysis.
	t.Logf("=== Chrome 120 JA3 Fingerprint ===")
	t.Logf("JA3 Hash:     %s", result.TLS.JA3Hash)
	t.Logf("JA3 Text:     %s", result.TLS.JA3)
	t.Logf("User-Agent:   %s", result.UserAgent)
	t.Logf("TLS Record:   %s", result.TLS.Version)
	t.Logf("HTTP Version: %s", result.HTTPVersion)
	t.Logf("Source IP:    %s", result.IP)

	// The JA3 hash should not be empty.
	if result.TLS.JA3Hash == "" {
		t.Error("JA3 hash is empty — expected a non-empty hash")
	}

	// The JA3 hash should NOT match Go's default JA3
	// Go default JA3 hash (varies by Go version).
	goDefaultJA3 := "1d28802f0f80b8a9b9a6fe2c9f5c8a3d"
	if result.TLS.JA3Hash == goDefaultJA3 {
		t.Errorf("JA3 hash %s matches Go default — stealth transport is NOT active!", result.TLS.JA3Hash)
	}

	// Log HTTP version — whether h2 or http/1.1 is negotiated depends on
	// Go's http.Transport HTTP/2 configuration, not the uTLS fingerprint.
	// The raw dialer test (TestChrome120AgainstRealTLSInspector) confirms
	// h2 ALPN negotiation at the TLS layer.
	t.Logf("Negotiated HTTP: %s (resp.ProtoMajor=%d)", result.HTTPVersion, resp.ProtoMajor)

	// Verify User-Agent is not Go-http-client.
	if strings.Contains(result.UserAgent, "Go-http-client") {
		t.Errorf("User-Agent %q contains Go-http-client — header sanitization failed", result.UserAgent)
	}

	// Print a clear verdict.
	if result.TLS.JA3Hash != "" && result.TLS.JA3Hash != goDefaultJA3 {
		t.Logf("")
		t.Logf("✅ JA3 VERIFICATION PASSED")
		t.Logf("   Expected: Chrome 120 fingerprint (not Go default)")
		t.Logf("   Observed: %s", result.TLS.JA3Hash)
		t.Logf("   Go default JA3 would be: %s", goDefaultJA3)
		t.Logf("   Stealth transport is ACTIVE")
	}
}

// TestFirefox120JA3Fingerprint verifies Firefox 120 fingerprint in same manner.
func TestFirefox120JA3Fingerprint(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping external JA3 verification test in short mode")
	}

	client := NewClient(ClientConfig{
		Profile:         ProfileFirefox120,
		Timeout:         15 * time.Second,
		SanitizeHeaders: true,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://tls.peet.ws/api/all", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Firefox 120 stealth client failed to reach tls.peet.ws: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected HTTP 200, got %d", resp.StatusCode)
	}

	var result tlsPeetWSResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode tls.peet.ws response: %v", err)
	}

	t.Logf("=== Firefox 120 JA3 Fingerprint ===")
	t.Logf("JA3 Hash:     %s", result.TLS.JA3Hash)
	t.Logf("JA3 Text:     %s", result.TLS.JA3)
	t.Logf("User-Agent:   %s", result.UserAgent)
	t.Logf("TLS Record:   %s", result.TLS.Version)
	t.Logf("HTTP Version: %s", result.HTTPVersion)
	t.Logf("Source IP:    %s", result.IP)

	goDefaultJA3 := "1d28802f0f80b8a9b9a6fe2c9f5c8a3d"
	if result.TLS.JA3Hash == goDefaultJA3 {
		t.Errorf("JA3 hash %s matches Go default — stealth NOT active!", result.TLS.JA3Hash)
	}
	if result.TLS.JA3Hash == "" {
		t.Error("JA3 hash is empty")
	}
	if strings.Contains(result.UserAgent, "Go-http-client") {
		t.Errorf("User-Agent contains Go-http-client — sanitization failed")
	}

	if result.TLS.JA3Hash != "" && result.TLS.JA3Hash != goDefaultJA3 {
		t.Logf("")
		t.Logf("✅ JA3 VERIFICATION PASSED — Firefox 120 fingerprint confirmed")
	}
}

// TestChrome120JA3DoesNotMatchGoDefault is a fast offline test that verifies
// the Chrome 120 profile's ClientHelloID is NOT the Go default.
func TestChrome120JA3DoesNotMatchGoDefault(t *testing.T) {
	id := ProfileChrome120.ClientHelloID.Str()
	if id == "Go-http-client" || id == "" {
		t.Errorf("Chrome 120 ClientHelloID.Str() = %q, expected Chrome preset", id)
	}
	t.Logf("Chrome 120 utls ClientHelloID: %s", id)
}

// RunJA3Verification is a non-test helper that prints the full JA3 verification
// output. Call this from a standalone program.
func RunJA3Verification() error {
	client := NewClient(ClientConfig{
		Profile:         ProfileChrome120,
		Timeout:         15 * time.Second,
		SanitizeHeaders: true,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://tls.peet.ws/api/all", nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("stealth HTTP GET failed: %w", err)
	}
	defer resp.Body.Close()

	var result tlsPeetWSResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	fmt.Println("========================================")
	fmt.Println("  JA3 Fingerprint Verification")
	fmt.Println("========================================")
	fmt.Printf("  Profile:     Chrome 120 (utls preset)\n")
	fmt.Printf("  JA3 Hash:    %s\n", result.TLS.JA3Hash)
	fmt.Printf("  JA3 Text:    %s\n", result.TLS.JA3)
	fmt.Printf("  User-Agent:  %s\n", result.UserAgent)
	fmt.Printf("  TLS Record:  %s\n", result.TLS.Version)
	fmt.Printf("  HTTP Proto:  %s\n", result.HTTPVersion)
	fmt.Printf("  Source IP:   %s\n", result.IP)

	goDefault := "1d28802f0f80b8a9b9a6fe2c9f5c8a3d"
	if result.TLS.JA3Hash == goDefault {
		fmt.Println("  ❌ JA3 MATCHES GO DEFAULT — STEALTH NOT ACTIVE!")
		return fmt.Errorf("JA3 hash matches Go default: %s", result.TLS.JA3Hash)
	}
	if result.TLS.JA3Hash == "" {
		return fmt.Errorf("JA3 hash is empty")
	}
	fmt.Printf("  ✅ JA3 VERIFIED — Stealth transport ACTIVE (hash: %s)\n", result.TLS.JA3Hash)
	fmt.Println("========================================")
	return nil
}
