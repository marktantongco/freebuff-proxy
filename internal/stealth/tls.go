package stealth

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	utls "github.com/refraction-networking/utls"
)

// dialer implements a net/http DialTLSContext function that uses utls to
// impersonate a specific browser's TLS fingerprint.
//
// When proxyPool is set, each outbound connection routes through a rotating
// SOCKS5 proxy from the pool before applying the JA3 TLS fingerprint.
type dialer struct {
	profile            *Profile
	resolveFN          func(ctx context.Context, network, addr string) (net.Conn, error)
	insecureSkipVerify bool // set true for tests with self-signed certs
	proxyPool          *ProxyPool
}

// Dial creates a TLS connection to the given address using the configured
// browser fingerprint profile. It wraps a standard TCP connection with utls
// and applies the ClientHello preset (or custom spec) before performing the
// TLS handshake.
//
// When the profile has a CustomSpec, it uses utls.HelloCustom + ApplyPreset
// for precise control over cipher suites, curves, and extensions (including
// ALPN negotiation for HTTP/2). Otherwise it uses the profile's ClientHelloID
// preset.
//
// The returned connection is a *utls.UConn and can be type-asserted if
// low-level access is needed.
func (d *dialer) Dial(ctx context.Context, network, addr string) (net.Conn, error) {
	// Determine the dial function to use for the raw TCP connection.
	// If proxyPool is configured, route through a rotating SOCKS5 proxy.
	var rawConn net.Conn
	var err error
	var proxyEntry *ProxyEntry

	if d.proxyPool != nil {
		// Get next proxy from the pool (round-robin).
		entry := d.proxyPool.Next()
		if entry == nil {
			return nil, fmt.Errorf("stealth: no SOCKS5 proxies available in pool")
		}
		proxyEntry = entry

		// Create a SOCKS5 dialer for the proxy entry.
		proxyDialer, dialErr := entry.Dialer()
		if dialErr != nil {
			d.proxyPool.MarkFailure(entry)
			return nil, fmt.Errorf("stealth: create SOCKS5 dialer: %w", dialErr)
		}

		// Dial the upstream through the SOCKS5 proxy.
		rawConn, err = proxyDialer.Dial(network, addr)
		if err != nil {
			d.proxyPool.MarkFailure(entry)
			return nil, fmt.Errorf("stealth: SOCKS5 proxy dial to %s failed: %w", addr, err)
		}
	} else {
		// No proxy — use the configured resolver or default TCP dialer.
		dialFN := d.resolveFN
		if dialFN == nil {
			dialFN = (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
				DualStack: true,
			}).DialContext
		}
		rawConn, err = dialFN(ctx, network, addr)
		if err != nil {
			return nil, fmt.Errorf("stealth: tcp dial failed: %w", err)
		}
	}

	// Extract the hostname for SNI.
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		rawConn.Close()
		if proxyEntry != nil {
			d.proxyPool.MarkFailure(proxyEntry)
		}
		return nil, fmt.Errorf("stealth: invalid address %q: %w", addr, err)
	}

	// Get the utls ClientHelloID from the profile.
	helloID := d.profile.ClientHelloID

	// Wrap the TCP connection with utls.
	uConn := utls.UClient(rawConn, &utls.Config{
		ServerName:         host,
		InsecureSkipVerify: d.insecureSkipVerify,
		MinVersion:         tls.VersionTLS12,
	}, helloID)

	// If the profile has a custom spec, apply it via ApplyPreset.
	// This provides precise control over the ClientHello including ALPN.
	if d.profile.CustomSpec != nil {
		if err := uConn.ApplyPreset(d.profile.CustomSpec); err != nil {
			rawConn.Close()
			if proxyEntry != nil {
				d.proxyPool.MarkFailure(proxyEntry)
			}
			return nil, fmt.Errorf("stealth: apply custom spec failed: %w", err)
		}
	}

	// Perform the TLS handshake.
	if err := uConn.HandshakeContext(ctx); err != nil {
		rawConn.Close()
		if proxyEntry != nil {
			d.proxyPool.MarkFailure(proxyEntry)
		}
		return nil, fmt.Errorf("stealth: tls handshake failed: %w", err)
	}

	// Mark the proxy as successful.
	if proxyEntry != nil {
		d.proxyPool.MarkSuccess(proxyEntry)
	}

	return uConn, nil
}

// DialerOption configures the dialer returned by Dialer.
type DialerOption func(*dialer)

// WithInsecureSkipVerify enables InsecureSkipVerify on the TLS config.
// Useful for tests with self-signed certificates.
func WithInsecureSkipVerify() DialerOption {
	return func(d *dialer) {
		d.insecureSkipVerify = true
	}
}

// WithProxyPool attaches a rotating SOCKS5 proxy pool to the dialer.
// When set, each outbound connection picks the next proxy from the pool
// in round-robin order, providing IP rotation and geo-evasion.
// The proxy connection is established first, then wrapped with utls for
// JA3 TLS fingerprint impersonation.
func WithProxyPool(pool *ProxyPool) DialerOption {
	return func(d *dialer) {
		d.proxyPool = pool
	}
}

// Dialer returns a DialTLSContext function compatible with http.Transport.
// This is the low-level interface; most users should use NewClient() instead.
//
// Example:
//
//	tr := &http.Transport{
//	    DialTLSContext: stealth.Dialer(stealth.ProfileChrome120, nil),
//	}
//	client := &http.Client{Transport: tr}
func Dialer(profile *Profile, resolver func(context.Context, string, string) (net.Conn, error), opts ...DialerOption) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if profile == nil {
		profile = DefaultProfile
	}
	d := &dialer{
		profile:   profile,
		resolveFN: resolver,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d.Dial
}


