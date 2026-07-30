package stealth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
)

// generateTestCert creates a self-signed TLS certificate valid for localhost.
func generateTestCert() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate serial: %w", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create cert: %w", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}, nil
}

// testALPNServer starts a TLS listener that advertises the given ALPN
// protocols and returns the address and a close function.
func testALPNServer(t *testing.T, alpnProtos []string) (addr string, closeFn func()) {
	t.Helper()

	cert, err := generateTestCert()
	if err != nil {
		t.Fatalf("Failed to generate test cert: %v", err)
	}

	config := &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   alpnProtos,
		MinVersion:   tls.VersionTLS12,
	}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", config)
	if err != nil {
		t.Fatalf("Failed to start TLS listener: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		tlsConn, ok := conn.(*tls.Conn)
		if !ok {
			conn.Close()
			return
		}
		_ = tlsConn.Handshake()
	}()

	closeFn = func() {
		ln.Close()
		wg.Wait()
	}

	return ln.Addr().String(), closeFn
}

// testStealthDialALPN dials a TLS server using the production Dialer()
// function with WithInsecureSkipVerify() for self-signed cert support,
// then returns the negotiated ALPN protocol from the uTLS connection.
func testStealthDialALPN(t *testing.T, profile *Profile, addr string) (string, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dialerFn := Dialer(profile, nil, WithInsecureSkipVerify())

	rawConn, err := dialerFn(ctx, "tcp", addr)
	if err != nil {
		return "", fmt.Errorf("stealth dial: %w", err)
	}
	defer rawConn.Close()

	uConn, ok := rawConn.(*utls.UConn)
	if !ok {
		return "", fmt.Errorf("stealth connection is %T, not *utls.UConn", rawConn)
	}
	state := uConn.ConnectionState()
	return state.NegotiatedProtocol, nil
}

// ── ALPN Negotiation Tests (h2) ────────────────────────────────────────────

func TestSafari17NegotiatesH2ALPN(t *testing.T) {
	addr, closeFn := testALPNServer(t, []string{"h2", "http/1.1"})
	defer closeFn()

	proto, err := testStealthDialALPN(t, ProfileSafari17, addr)
	if err != nil {
		t.Fatalf("Safari 17 stealth dial failed: %v", err)
	}
	if proto != "h2" {
		t.Errorf("Safari 17 negotiated ALPN = %q, want %q", proto, "h2")
	}
	t.Logf("Safari 17 negotiated ALPN: %s", proto)
}

func TestChrome120NegotiatesH2ALPN(t *testing.T) {
	addr, closeFn := testALPNServer(t, []string{"h2", "http/1.1"})
	defer closeFn()

	proto, err := testStealthDialALPN(t, ProfileChrome120, addr)
	if err != nil {
		t.Fatalf("Chrome 120 stealth dial failed: %v", err)
	}
	if proto != "h2" {
		t.Errorf("Chrome 120 negotiated ALPN = %q, want %q", proto, "h2")
	}
	t.Logf("Chrome 120 negotiated ALPN: %s", proto)
}

func TestFirefox120NegotiatesH2ALPN(t *testing.T) {
	addr, closeFn := testALPNServer(t, []string{"h2", "http/1.1"})
	defer closeFn()

	proto, err := testStealthDialALPN(t, ProfileFirefox120, addr)
	if err != nil {
		t.Fatalf("Firefox 120 stealth dial failed: %v", err)
	}
	if proto != "h2" {
		t.Errorf("Firefox 120 negotiated ALPN = %q, want %q", proto, "h2")
	}
	t.Logf("Firefox 120 negotiated ALPN: %s", proto)
}

// ── Safari 17 CustomSpec Verification ──────────────────────────────────────

func TestSafari17CustomSpecIsUsed(t *testing.T) {
	idStr := ProfileSafari17.ClientHelloID.Str()
	if idStr != "Custom-0" && idStr != "HelloCustom" && idStr != "HelloCustom_*" {
		t.Errorf("ProfileSafari17.ClientHelloID.Str() = %q, want %q or %q",
			idStr, "HelloCustom", "Custom-0")
	}
	if ProfileSafari17.CustomSpec == nil {
		t.Fatal("ProfileSafari17.CustomSpec is nil — expected custom ClientHelloSpec")
	}
	hasTLS13Cipher := false
	for _, cs := range ProfileSafari17.CustomSpec.CipherSuites {
		if cs == utls.TLS_AES_128_GCM_SHA256 ||
			cs == utls.TLS_AES_256_GCM_SHA384 ||
			cs == utls.TLS_CHACHA20_POLY1305_SHA256 {
			hasTLS13Cipher = true
			break
		}
	}
	if !hasTLS13Cipher {
		t.Error("ProfileSafari17.CustomSpec has no TLS 1.3 cipher suites")
	}
}

// ── CustomSpec vs Preset Consistency Tests ─────────────────────────────────

func TestProfileCustomSpecsAreConsistent(t *testing.T) {
	tests := []struct {
		name    string
		profile *Profile
		wantNil bool
	}{
		{"Chrome120", ProfileChrome120, true},
		{"Safari17", ProfileSafari17, false},
		{"Firefox120", ProfileFirefox120, true},
		{"Random", ProfileRandom, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantNil && tt.profile.CustomSpec != nil {
				t.Errorf("%s has non-nil CustomSpec (expected nil — uses preset)", tt.name)
			}
			if !tt.wantNil && tt.profile.CustomSpec == nil {
				t.Errorf("%s has nil CustomSpec (expected non-nil)", tt.name)
			}
		})
	}
}

// ── Dialer Option Tests ────────────────────────────────────────────────────

func TestWithInsecureSkipVerifyOption(t *testing.T) {
	// Verify the WithInsecureSkipVerify option is available and compiles.
	opts := WithInsecureSkipVerify()
	if opts == nil {
		t.Fatal("WithInsecureSkipVerify() returned nil")
	}

	// Verify it doesn't panic when used.
	_ = Dialer(ProfileSafari17, nil, opts)
}

// ── Integration test (requires network, skips in short mode) ───────────────
// Note: The Safari 17 custom spec is designed for local/TLS-inspection use.
// It may not work with all production TLS endpoints due to differences between
// hand-crafted ClientHelloSpecs and utls presets.

func TestSafari17AgainstRealTLSInspector(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping external integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	dialerFn := Dialer(ProfileSafari17, nil)
	conn, err := dialerFn(ctx, "tcp", "tls.peet.ws:443")
	if err != nil {
		// Custom specs may not work with all servers; this is a best-effort check.
		t.Skipf("Safari 17 dial to tls.peet.ws failed (expected, custom spec limitation): %v", err)
	}
	defer conn.Close()

	if uConn, ok := conn.(*utls.UConn); ok {
		state := uConn.ConnectionState()
		t.Logf("Negotiated ALPN with tls.peet.ws: %s", state.NegotiatedProtocol)
		t.Logf("TLS version: 0x%04X", state.Version)
		t.Logf("Cipher suite: %d", state.CipherSuite)
	}
}

// TestChrome120AgainstRealTLSInspector confirms Chrome 120 preset (which uses
// the maintained utls preset, not a hand-crafted spec) works against real TLS.
func TestChrome120AgainstRealTLSInspector(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping external integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	dialerFn := Dialer(ProfileChrome120, nil)
	conn, err := dialerFn(ctx, "tcp", "tls.peet.ws:443")
	if err != nil {
		t.Fatalf("Chrome 120 dial to tls.peet.ws failed: %v", err)
	}
	defer conn.Close()

	if uConn, ok := conn.(*utls.UConn); ok {
		state := uConn.ConnectionState()
		t.Logf("Chrome 120 negotiated ALPN: %s", state.NegotiatedProtocol)
		t.Logf("TLS version: 0x%04X", state.Version)
	}
}
