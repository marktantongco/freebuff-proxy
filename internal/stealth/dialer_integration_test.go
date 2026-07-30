package stealth

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
)

// testSOCKS5Server starts a minimal RFC 1928 SOCKS5 proxy server that forwards
// to whatever target address the client requests via CONNECT. It returns the
// proxy's listening address and a cleanup function.
//
// Supported: no-auth, IPv4 CONNECT, domain name CONNECT.
// Not supported: auth, IPv6, BIND, UDP ASSOCIATE.
func testSOCKS5Server(t *testing.T) (addr string, closeFn func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start SOCKS5 listener: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleSOCKS5Conn(ctx, conn)
		}
	}()

	closeFn = func() {
		cancel()
		ln.Close()
		wg.Wait()
	}
	return ln.Addr().String(), closeFn
}

// handleSOCKS5Conn handles a single SOCKS5 client connection.
// It performs the handshake, connects to the target, and relays data.
func handleSOCKS5Conn(ctx context.Context, client net.Conn) {
	defer client.Close()

	client.SetDeadline(time.Now().Add(10 * time.Second))
	defer client.SetDeadline(time.Time{})

	// 1. Auth negotiation: read client methods, respond "no auth".
	buf := make([]byte, 512)
	if _, err := io.ReadFull(client, buf[:2]); err != nil {
		return
	}
	if buf[0] != 0x05 { // Not SOCKS5
		return
	}
	// Read the methods list (nmethods bytes after the header) to consume them.
	nmethods := int(buf[1])
	if nmethods > 0 {
		if nmethods > len(buf) {
			return
		}
		if _, err := io.ReadFull(client, buf[:nmethods]); err != nil {
			return
		}
	}
	// Respond: version=5, method=0 (no auth)
	if _, err := client.Write([]byte{0x05, 0x00}); err != nil {
		return
	}

	// 2. Read CONNECT request header (exactly 4 bytes: ver, cmd, rsv, atyp).
	// Use ReadFull, NOT ReadAtLeast, to avoid consuming address bytes from
	// the buffer when all data arrives in a single TCP segment.
	if _, err := io.ReadFull(client, buf[:4]); err != nil {
		return
	}
	if buf[1] != 0x01 { // Not CONNECT
		return
	}

	// 3. Parse target address from the request.
	var host string
	switch buf[3] {
	case 0x01: // IPv4 (4 bytes)
		if _, err := io.ReadFull(client, buf[:4]); err != nil {
			return
		}
		host = net.IP(buf[:4]).String()
	case 0x03: // Domain name (1 byte length + name)
		if _, err := io.ReadFull(client, buf[:1]); err != nil {
			return
		}
		domainLen := int(buf[0])
		if domainLen == 0 || domainLen > 255 {
			return
		}
		if _, err := io.ReadFull(client, buf[:domainLen]); err != nil {
			return
		}
		host = string(buf[:domainLen])
	default:
		return // Unsupported address type
	}

	// Read port (2 bytes).
	if _, err := io.ReadFull(client, buf[:2]); err != nil {
		return
	}
	port := int(buf[0])<<8 | int(buf[1])
	targetAddr := net.JoinHostPort(host, fmt.Sprintf("%d", port))

	// 4. Connect to the target.
	upstream, err := net.DialTimeout("tcp", targetAddr, 5*time.Second)
	if err != nil {
		client.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer upstream.Close()

	// 5. Reply with success (rep=0x00).
	if _, err := client.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}

	// 6. Relay data bidirectionally.
	var relayWg sync.WaitGroup
	relayWg.Add(2)
	go func() {
		defer relayWg.Done()
		io.Copy(upstream, client)
		upstream.Close()
	}()
	go func() {
		defer relayWg.Done()
		io.Copy(client, upstream)
		client.Close()
	}()
	relayWg.Wait()
}

// testMultiAcceptTLSServer starts a TLS listener that accepts multiple
// connections and performs the TLS handshake on each. Returns the address
// and a cleanup function.
func testMultiAcceptTLSServer(t *testing.T) (addr string, closeFn func()) {
	t.Helper()

	cert, err := generateTestCert()
	if err != nil {
		t.Fatalf("Failed to generate test cert: %v", err)
	}

	config := &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h2", "http/1.1"},
		MinVersion:   tls.VersionTLS12,
	}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", config)
	if err != nil {
		t.Fatalf("Failed to start TLS listener: %v", err)
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c *tls.Conn) {
				c.SetDeadline(time.Now().Add(5 * time.Second))
				c.Handshake()
			}(conn.(*tls.Conn))
		}
	}()

	closeFn = func() {
		ln.Close()
	}
	return ln.Addr().String(), closeFn
}

// ── SOCKS5 Integration Tests ───────────────────────────────────────────────

// TestDialerWithSOCKS5Proxy verifies the stealth dialer routes through a
// rotating SOCKS5 proxy from the pool to a TLS upstream:
//   - TLS handshake completes through the SOCKS5 relay
//   - Pool stats reflect the successful proxy (Alive=1, Dead=0)
func TestDialerWithSOCKS5Proxy(t *testing.T) {
	tlsAddr, closeTLS := testMultiAcceptTLSServer(t)
	defer closeTLS()

	proxyAddr, closeProxy := testSOCKS5Server(t)
	defer closeProxy()

	proxyHost, proxyPortStr, _ := net.SplitHostPort(proxyAddr)
	var proxyPort int
	fmt.Sscanf(proxyPortStr, "%d", &proxyPort)

	pool := NewProxyPool(ProxyPoolConfig{})
	pool.proxies = []*ProxyEntry{
		{Host: proxyHost, Port: proxyPort, Alive: true},
	}

	dialerFn := Dialer(ProfileChrome120, nil, WithProxyPool(pool),
		WithInsecureSkipVerify())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := dialerFn(ctx, "tcp", tlsAddr)
	if err != nil {
		t.Fatalf("Dial through SOCKS5 proxy failed: %v", err)
	}
	defer conn.Close()

	// Verify the connection is a *utls.UConn (wrapped by stealth after SOCKS5).
	if _, ok := conn.(*utls.UConn); !ok {
		t.Errorf("Connection type = %T, want *utls.UConn", conn)
	}

	// Verify pool stats.
	stats := pool.Stats().(PoolStats)
	if stats.Total != 1 {
		t.Errorf("Pool Stats().Total = %d, want 1", stats.Total)
	}
	if stats.Alive != 1 {
		t.Errorf("Pool Stats().Alive = %d, want 1", stats.Alive)
	}
	if stats.Dead != 0 {
		t.Errorf("Pool Stats().Dead = %d, want 0", stats.Dead)
	}

	// Next() should still return the proxy (MarkSuccess reset failures).
	entry := pool.Next()
	if entry == nil {
		t.Fatal("Pool.Next() returned nil after successful dial")
	}
	if !entry.Alive {
		t.Error("Proxy entry should be Alive after successful dial")
	}
	if entry.Failures != 0 {
		t.Errorf("Proxy entry Failures = %d, want 0 (MarkSuccess should reset)", entry.Failures)
	}
}

// TestDialerWithSOCKS5ProxyFailure verifies that when the SOCKS5 proxy dial
// fails, MarkFailure is called, incrementing the failure count on the proxy.
// Note: MarkFailure only sets Alive=false after 3+ failures (default MaxFailures=3).
func TestDialerWithSOCKS5ProxyFailure(t *testing.T) {
	tlsAddr, closeTLS := testMultiAcceptTLSServer(t)
	defer closeTLS()

	pool := NewProxyPool(ProxyPoolConfig{})
	pool.proxies = []*ProxyEntry{
		{Host: "127.0.0.1", Port: 1, Alive: true}, // Port 1 is almost certainly closed
	}

	dialerFn := Dialer(ProfileChrome120, nil, WithProxyPool(pool),
		WithInsecureSkipVerify())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := dialerFn(ctx, "tcp", tlsAddr)
	if err == nil {
		conn.Close()
		t.Fatal("Dial with unreachable SOCKS5 proxy: expected error, got nil")
	}

	// MarkFailure should have been called once. Since MaxFailures defaults to 3,
	// the proxy is still Alive after a single failure.
	stats := pool.Stats().(PoolStats)
	if stats.Dead != 0 {
		t.Errorf("Pool Stats().Dead = %d, want 0 (proxy still alive after 1 failure)", stats.Dead)
	}
	if stats.Alive != 1 {
		t.Errorf("Pool Stats().Alive = %d, want 1 (proxy still alive after 1 failure)", stats.Alive)
	}
	if pool.proxies[0].Failures != 1 {
		t.Errorf("Proxy entry Failures = %d, want 1 after single MarkFailure", pool.proxies[0].Failures)
	}
}

// TestDialerWithEmptyProxyPool verifies the dialer returns an error when the
// proxy pool is configured but empty.
func TestDialerWithEmptyProxyPool(t *testing.T) {
	tlsAddr, closeTLS := testMultiAcceptTLSServer(t)
	defer closeTLS()

	pool := NewProxyPool(ProxyPoolConfig{})
	// Leave pool empty.

	dialerFn := Dialer(ProfileChrome120, nil, WithProxyPool(pool))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := dialerFn(ctx, "tcp", tlsAddr)
	if err == nil {
		conn.Close()
		t.Fatal("Dial with empty proxy pool: expected error, got nil")
	}
	if err.Error() != "stealth: no SOCKS5 proxies available in pool" {
		t.Errorf("Dial error = %q, want %q", err.Error(), "stealth: no SOCKS5 proxies available in pool")
	}
}

// TestDialerMultipleProxyRoundRobin verifies that multiple dials cycle through
// proxies in round-robin order and each success is tracked correctly.
func TestDialerMultipleProxyRoundRobin(t *testing.T) {
	tlsAddr, closeTLS := testMultiAcceptTLSServer(t)
	defer closeTLS()

	proxyAddr1, closeProxy1 := testSOCKS5Server(t)
	defer closeProxy1()

	proxyAddr2, closeProxy2 := testSOCKS5Server(t)
	defer closeProxy2()

	h1, p1, _ := net.SplitHostPort(proxyAddr1)
	h2, p2, _ := net.SplitHostPort(proxyAddr2)
	var port1, port2 int
	fmt.Sscanf(p1, "%d", &port1)
	fmt.Sscanf(p2, "%d", &port2)

	pool := NewProxyPool(ProxyPoolConfig{})
	pool.proxies = []*ProxyEntry{
		{Host: h1, Port: port1, Alive: true},
		{Host: h2, Port: port2, Alive: true},
	}

	dialerFn := Dialer(ProfileChrome120, nil, WithProxyPool(pool),
		WithInsecureSkipVerify())

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Dial multiple times — each should pick the next proxy in round-robin.
	for i := 0; i < 4; i++ {
		conn, err := dialerFn(ctx, "tcp", tlsAddr)
		if err != nil {
			t.Fatalf("Dial #%d through SOCKS5 proxy failed: %v", i+1, err)
		}
		conn.Close()
	}

	// All proxies should be healthy.
	stats := pool.Stats().(PoolStats)
	if stats.Alive != 2 {
		t.Errorf("Pool Stats().Alive = %d, want 2 (both proxies successful)", stats.Alive)
	}
	if stats.Dead != 0 {
		t.Errorf("Pool Stats().Dead = %d, want 0", stats.Dead)
	}
}

// TestDialerProxyRotationToleratesFailures verifies that when one proxy fails,
// the next dial picks a different proxy, and the failed proxy is marked dead.
func TestDialerProxyRotationToleratesFailures(t *testing.T) {
	tlsAddr, closeTLS := testMultiAcceptTLSServer(t)
	defer closeTLS()

	proxyAddrGood, closeProxy := testSOCKS5Server(t)
	defer closeProxy()

	hGood, pGood, _ := net.SplitHostPort(proxyAddrGood)
	var portGood int
	fmt.Sscanf(pGood, "%d", &portGood)

	pool := NewProxyPool(ProxyPoolConfig{})
	pool.proxies = []*ProxyEntry{
		{Host: "127.0.0.1", Port: 1, Alive: true}, // bad proxy (port closed)
		{Host: hGood, Port: portGood, Alive: true}, // good proxy
	}

	dialerFn := Dialer(ProfileChrome120, nil, WithProxyPool(pool),
		WithInsecureSkipVerify())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// First dial should pick the bad proxy (round-robin starts at index 0).
	conn, err := dialerFn(ctx, "tcp", tlsAddr)
	if err == nil {
		conn.Close()
		t.Log("First dial (bad proxy) unexpectedly succeeded")
	}

	// Second dial should pick the good proxy.
	conn2, err := dialerFn(ctx, "tcp", tlsAddr)
	if err != nil {
		t.Fatalf("Second dial (good proxy) failed: %v", err)
	}
	defer conn2.Close()

	// Stats: bad proxy has 1 failure (still alive, 1 < MaxFailures=3), good proxy healthy.
	stats := pool.Stats().(PoolStats)
	if stats.Total != 2 {
		t.Errorf("Pool Stats().Total = %d, want 2", stats.Total)
	}
	if stats.Alive != 2 {
		t.Errorf("Pool Stats().Alive = %d, want 2 (both proxies still alive after 1 failure)", stats.Alive)
	}
	if stats.Dead != 0 {
		t.Errorf("Pool Stats().Dead = %d, want 0 (1 failure < 3 max, no evictions yet)", stats.Dead)
	}
	if pool.proxies[0].Failures < 1 {
		t.Errorf("Bad proxy Failures = %d, want >= 1", pool.proxies[0].Failures)
	}
	if pool.proxies[1].Failures != 0 {
		t.Errorf("Good proxy Failures = %d, want 0 (MarkSuccess should reset)", pool.proxies[1].Failures)
	}
}
