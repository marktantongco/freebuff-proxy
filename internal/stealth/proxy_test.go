package stealth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── ProxyEntry Tests ───────────────────────────────────────────────────────

func TestProxyEntryURL_WithAuth(t *testing.T) {
	e := &ProxyEntry{Host: "192.168.1.1", Port: 1080, User: "user1", Pass: "pass1"}
	want := "socks5://user1:pass1@192.168.1.1:1080"
	if got := e.URL(); got != want {
		t.Errorf("URL() = %q, want %q", got, want)
	}
}

func TestProxyEntryURL_WithoutAuth(t *testing.T) {
	e := &ProxyEntry{Host: "10.0.0.1", Port: 1080}
	want := "socks5://10.0.0.1:1080"
	if got := e.URL(); got != want {
		t.Errorf("URL() = %q, want %q", got, want)
	}
}

func TestProxyEntryURL_IPv6(t *testing.T) {
	e := &ProxyEntry{Host: "::1", Port: 1080}
	want := "socks5://::1:1080"
	if got := e.URL(); got != want {
		t.Errorf("URL() for IPv6 = %q, want %q", got, want)
	}
}

func TestProxyEntryDialer_InvalidURL(t *testing.T) {
	// A host with spaces is invalid.
	e := &ProxyEntry{Host: "invalid host", Port: 1080}
	_, err := e.Dialer()
	if err == nil {
		t.Error("Dialer() with invalid host: expected error, got nil")
	}
}

// ── parseProxyList Tests ───────────────────────────────────────────────────

func TestParseProxyList_StandardFormat(t *testing.T) {
	data := "192.168.1.1:1080:user1:pass1\n10.0.0.1:1081:user2:pass2"
	entries := parseProxyList(data)

	if len(entries) != 2 {
		t.Fatalf("parseProxyList() returned %d entries, want 2", len(entries))
	}

	if entries[0].Host != "192.168.1.1" || entries[0].Port != 1080 || entries[0].User != "user1" || entries[0].Pass != "pass1" {
		t.Errorf("Entry 0 = %+v, want {Host:192.168.1.1 Port:1080 User:user1 Pass:pass1}", entries[0])
	}
	if entries[1].Host != "10.0.0.1" || entries[1].Port != 1081 || entries[1].User != "user2" || entries[1].Pass != "pass2" {
		t.Errorf("Entry 1 = %+v, want {Host:10.0.0.1 Port:1081 User:user2 Pass:pass2}", entries[1])
	}
}

func TestParseProxyList_WithCountry(t *testing.T) {
	data := "192.168.1.1:1080:user1:pass1:US\n10.0.0.1:1081:user2:pass2:DE"
	entries := parseProxyList(data)

	if len(entries) != 2 {
		t.Fatalf("parseProxyList() returned %d entries, want 2", len(entries))
	}

	if entries[0].Country != "US" {
		t.Errorf("Entry 0 Country = %q, want %q", entries[0].Country, "US")
	}
	if entries[1].Country != "DE" {
		t.Errorf("Entry 1 Country = %q, want %q", entries[1].Country, "DE")
	}
}

func TestParseProxyList_EmptyLinesAndComments(t *testing.T) {
	data := "# comment line\n\n192.168.1.1:1080:user1:pass1\n# another comment\n\n"
	entries := parseProxyList(data)

	if len(entries) != 1 {
		t.Fatalf("parseProxyList() returned %d entries, want 1", len(entries))
	}
}

func TestParseProxyList_LessThanFourParts(t *testing.T) {
	data := "192.168.1.1:1080:user1\n192.168.1.1:1080\ninvalid"
	entries := parseProxyList(data)

	if len(entries) != 0 {
		t.Errorf("parseProxyList() returned %d entries, want 0", len(entries))
	}
}

func TestParseProxyList_EmptyInput(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{"empty string", ""},
		{"only whitespace", "  \n  \n"},
		{"only comments", "# comment\n# another"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := parseProxyList(tt.data)
			if len(entries) != 0 {
				t.Errorf("parseProxyList() returned %d entries, want 0", len(entries))
			}
		})
	}
}

func TestParseProxyList_WhitespaceTrimming(t *testing.T) {
	data := "  192.168.1.1:1080:user1:pass1  \n  # indented comment\n10.0.0.1:1081:user2:pass2"
	entries := parseProxyList(data)

	if len(entries) != 2 {
		t.Fatalf("parseProxyList() returned %d entries, want 2", len(entries))
	}
}

func TestParseProxyList_TrailingNewline(t *testing.T) {
	data := "192.168.1.1:1080:user1:pass1\n"
	entries := parseProxyList(data)

	if len(entries) != 1 {
		t.Fatalf("parseProxyList() returned %d entries, want 1", len(entries))
	}
}

// ── PoolStats Tests ────────────────────────────────────────────────────────

func TestPoolStats_EmptyPool(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	stats := p.Stats().(PoolStats)

	if stats.Total != 0 {
		t.Errorf("Stats().Total = %d, want 0", stats.Total)
	}
	if stats.Alive != 0 {
		t.Errorf("Stats().Alive = %d, want 0", stats.Alive)
	}
	if stats.Dead != 0 {
		t.Errorf("Stats().Dead = %d, want 0", stats.Dead)
	}
	if len(stats.ByCountry) != 0 {
		t.Errorf("Stats().ByCountry = %v, want empty", stats.ByCountry)
	}
}

func TestPoolStats_MixedProxies(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	p.proxies = []*ProxyEntry{
		{Host: "1.1.1.1", Port: 1080, Alive: true, Country: "US"},
		{Host: "2.2.2.2", Port: 1081, Alive: true, Country: "DE"},
		{Host: "3.3.3.3", Port: 1082, Alive: false, Country: "US"},
	}

	stats := p.Stats().(PoolStats)

	if stats.Total != 3 {
		t.Errorf("Stats().Total = %d, want 3", stats.Total)
	}
	if stats.Alive != 2 {
		t.Errorf("Stats().Alive = %d, want 2", stats.Alive)
	}
	if stats.Dead != 1 {
		t.Errorf("Stats().Dead = %d, want 1", stats.Dead)
	}
	if stats.ByCountry["US"] != 2 {
		t.Errorf("Stats().ByCountry[US] = %d, want 2", stats.ByCountry["US"])
	}
	if stats.ByCountry["DE"] != 1 {
		t.Errorf("Stats().ByCountry[DE] = %d, want 1", stats.ByCountry["DE"])
	}
}

func TestPoolStats_ByCountryOmitempty(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	p.proxies = []*ProxyEntry{
		{Host: "1.1.1.1", Port: 1080, Alive: true},
		{Host: "2.2.2.2", Port: 1081, Alive: true},
	}
	stats := p.Stats().(PoolStats)

	if len(stats.ByCountry) != 0 {
		t.Errorf("Stats().ByCountry = %v, want empty for proxies without country", stats.ByCountry)
	}
}

// ── Len Tests ──────────────────────────────────────────────────────────────

func TestLen_Empty(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	if got := p.Len(); got != 0 {
		t.Errorf("Len() = %d, want 0", got)
	}
}

func TestLen_WithProxies(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	p.proxies = []*ProxyEntry{
		{Host: "1.1.1.1", Port: 1080},
		{Host: "2.2.2.2", Port: 1081},
	}
	if got := p.Len(); got != 2 {
		t.Errorf("Len() = %d, want 2", got)
	}
}

// ── Next Round-Robin Tests ─────────────────────────────────────────────────

func TestNext_EmptyPool(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	if got := p.Next(); got != nil {
		t.Errorf("Next() = %v, want nil for empty pool", got)
	}
}

func TestNext_RoundRobin(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	p.proxies = []*ProxyEntry{
		{Host: "1.1.1.1", Port: 1080, Alive: true},
		{Host: "2.2.2.2", Port: 1081, Alive: true},
		{Host: "3.3.3.3", Port: 1082, Alive: true},
	}

	// Should cycle through all three in order.
	got1 := p.Next()
	if got1.Host != "1.1.1.1" {
		t.Errorf("Next() #1 = %s, want 1.1.1.1", got1.Host)
	}
	got2 := p.Next()
	if got2.Host != "2.2.2.2" {
		t.Errorf("Next() #2 = %s, want 2.2.2.2", got2.Host)
	}
	got3 := p.Next()
	if got3.Host != "3.3.3.3" {
		t.Errorf("Next() #3 = %s, want 3.3.3.3", got3.Host)
	}
	// Fourth call wraps around.
	got4 := p.Next()
	if got4.Host != "1.1.1.1" {
		t.Errorf("Next() #4 = %s, want 1.1.1.1 (wrap around)", got4.Host)
	}
}

func TestNext_SkipsDeadProxies(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	p.proxies = []*ProxyEntry{
		{Host: "1.1.1.1", Port: 1080, Alive: false, Failures: 3},
		{Host: "2.2.2.2", Port: 1081, Alive: true},
		{Host: "3.3.3.3", Port: 1082, Alive: false, Failures: 5},
	}

	got := p.Next()
	if got.Host != "2.2.2.2" {
		t.Errorf("Next() = %s, want 2.2.2.2 (only alive proxy)", got.Host)
	}

	// Second call should return the same alive proxy again (round-robin wraps).
	got2 := p.Next()
	if got2.Host != "2.2.2.2" {
		t.Errorf("Next() after wrap = %s, want 2.2.2.2 again", got2.Host)
	}
}

func TestNext_FallsBackToFirstWhenAllDead(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	p.proxies = []*ProxyEntry{
		{Host: "1.1.1.1", Port: 1080, Alive: false, Failures: 4},
		{Host: "2.2.2.2", Port: 1081, Alive: false, Failures: 5},
	}

	got := p.Next()
	if got == nil {
		t.Fatal("Next() = nil, want fallback to first proxy")
	}
	if got.Host != "1.1.1.1" {
		t.Errorf("Next() = %s, want 1.1.1.1 (fallback to first dead)", got.Host)
	}
}

func TestNext_UpdatesLastUsed(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	p.proxies = []*ProxyEntry{
		{Host: "1.1.1.1", Port: 1080, Alive: true},
	}

	before := p.proxies[0].LastUsed
	time.Sleep(time.Millisecond) // Ensure time advances.
	p.Next()
	after := p.proxies[0].LastUsed

	if !after.After(before) {
		t.Error("Next() did not update LastUsed")
	}
}

func TestNext_ConcurrentSafe(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	p.proxies = []*ProxyEntry{
		{Host: "1.1.1.1", Port: 1080, Alive: true},
		{Host: "2.2.2.2", Port: 1081, Alive: true},
		{Host: "3.3.3.3", Port: 1082, Alive: true},
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				e := p.Next()
				if e == nil {
					t.Error("Next() returned nil during concurrent access")
					return
				}
			}
		}()
	}
	wg.Wait()
}

// ── MarkFailure / MarkSuccess Tests ────────────────────────────────────────

func TestMarkFailure_IncrementsCount(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	entry := &ProxyEntry{Host: "1.1.1.1", Port: 1080, Alive: true}
	p.proxies = []*ProxyEntry{entry}

	p.MarkFailure(entry)

	if entry.Failures != 1 {
		t.Errorf("After MarkFailure, Failures = %d, want 1", entry.Failures)
	}
}

func TestMarkFailure_EvictsAfterThree(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	entry := &ProxyEntry{Host: "1.1.1.1", Port: 1080, Alive: true}
	p.proxies = []*ProxyEntry{entry}

	for i := 1; i <= 3; i++ {
		p.MarkFailure(entry)
		if i < 3 && !entry.Alive {
			t.Errorf("After %d failures, Alive = false, want true", i)
		}
	}

	if entry.Alive {
		t.Error("After 3 MarkFailures, Alive = true, want false (evicted)")
	}
	if entry.Failures != 3 {
		t.Errorf("After 3 MarkFailures, Failures = %d, want 3", entry.Failures)
	}
}

func TestMarkSuccess_ResetsFailures(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	entry := &ProxyEntry{Host: "1.1.1.1", Port: 1080, Alive: true, Failures: 2}
	p.proxies = []*ProxyEntry{entry}

	p.MarkSuccess(entry)

	if entry.Failures != 0 {
		t.Errorf("After MarkSuccess, Failures = %d, want 0", entry.Failures)
	}
	if !entry.Alive {
		t.Error("After MarkSuccess, Alive = false, want true")
	}
}

func TestMarkSuccess_RestoresDeadProxy(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	entry := &ProxyEntry{Host: "1.1.1.1", Port: 1080, Alive: false, Failures: 5}
	p.proxies = []*ProxyEntry{entry}

	p.MarkSuccess(entry)

	if entry.Alive != true {
		t.Error("MarkSuccess on dead proxy: Alive still false, want true")
	}
}

func TestMarkFailure_NonExistentProxy(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	p.proxies = []*ProxyEntry{{Host: "1.1.1.1", Port: 1080}}

	// Should not panic.
	p.MarkFailure(&ProxyEntry{Host: "nonexistent", Port: 9999})
}

func TestMarkSuccess_NonExistentProxy(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	p.proxies = []*ProxyEntry{{Host: "1.1.1.1", Port: 1080}}

	// Should not panic.
	p.MarkSuccess(&ProxyEntry{Host: "nonexistent", Port: 9999})
}

func TestMarkFailure_ConcurrentSafe(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	entry := &ProxyEntry{Host: "1.1.1.1", Port: 1080, Alive: true}
	p.proxies = []*ProxyEntry{entry}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				p.MarkFailure(entry)
			}
		}()
	}
	wg.Wait()
}

// ── NewProxyPool Tests ─────────────────────────────────────────────────────

func TestNewProxyPool_Defaults(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})

	if p.interval != 30*time.Minute {
		t.Errorf("Default interval = %v, want 30m", p.interval)
	}
	if p.strictGeo != false {
		t.Errorf("Default strictGeo = %v, want false", p.strictGeo)
	}
	if p.fetchClient == nil {
		t.Error("Default fetchClient is nil")
	}
	if p.fetchClient.Timeout != 30*time.Second {
		t.Errorf("Default fetchClient.Timeout = %v, want 30s", p.fetchClient.Timeout)
	}
}

func TestNewProxyPool_CustomConfig(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{
		RefreshURL:      "https://proxy.example.com/list",
		RefreshInterval: 15 * time.Minute,
		StrictGeo:       true,
		MaxFailures:     5,
	})

	if p.refreshURL != "https://proxy.example.com/list" {
		t.Errorf("refreshURL = %q, want %q", p.refreshURL, "https://proxy.example.com/list")
	}
	if p.interval != 15*time.Minute {
		t.Errorf("interval = %v, want 15m", p.interval)
	}
	if p.strictGeo != true {
		t.Error("strictGeo = false, want true")
	}
}

// ── healthCheck Tests ───────────────────────────────────────────────────────

func TestHealthCheck_ReachablePort(t *testing.T) {
	// Start a local TCP listener to test reachability.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test listener: %v", err)
	}
	defer ln.Close()

	p := NewProxyPool(ProxyPoolConfig{})
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port := 0
	fmt.Sscanf(portStr, "%d", &port)

	entry := &ProxyEntry{Host: host, Port: port}
	if !p.healthCheck(entry) {
		t.Errorf("healthCheck(%s:%d) = false, want true (listener is running)", host, port)
	}
}

func TestHealthCheck_UnreachablePort(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	// Use a port that's very unlikely to be listening.
	entry := &ProxyEntry{Host: "127.0.0.1", Port: 1}
	if p.healthCheck(entry) {
		t.Errorf("healthCheck(127.0.0.1:1) = true, want false (port should be closed)")
	}
}

func TestHealthCheck_Timeout(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	// Use a non-routable IP to test timeout.
	entry := &ProxyEntry{Host: "10.255.255.1", Port: 1080}
	start := time.Now()
	result := p.healthCheck(entry)
	elapsed := time.Since(start)

	if result {
		t.Error("healthCheck(10.255.255.1:1080) = true, want false (non-routable)")
	}
	if elapsed > 10*time.Second {
		t.Errorf("healthCheck took %v, should timeout quickly (within 5s+slack)", elapsed)
	}
}

// ── Refresh with strict_geo Tests ──────────────────────────────────────────

func TestRefresh_StrictGeoRejectsNonUS(t *testing.T) {
	// Create a test server that returns non-US proxies.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "10.0.0.1:1080:user1:pass1:DE")
		fmt.Fprintln(w, "10.0.0.2:1081:user2:pass2:FR")
	}))
	defer ts.Close()

	p := NewProxyPool(ProxyPoolConfig{
		RefreshURL: ts.URL,
		StrictGeo:  true,
	})

	err := p.Refresh(context.Background())
	if err == nil {
		t.Fatal("Refresh() with all non-US proxies and strict_geo: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no healthy US proxies") {
		t.Errorf("Refresh() error = %q, want error about 'no healthy US proxies'", err)
	}
}

func TestRefresh_StrictGeoWithCountryUnknown(t *testing.T) {
	// Proxies without a country field should be accepted in strict_geo mode
	// because Country is "" which means the filter `entry.Country != ""` skips them.

	// Start a listener at test scope so it stays alive during health check.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test listener: %v", err)
	}
	defer ln.Close()
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Use the reachable port for health check.
		fmt.Fprintf(w, "%s:%s:user1:pass1\n", host, portStr)
	}))
	defer ts.Close()

	p := NewProxyPool(ProxyPoolConfig{
		RefreshURL: ts.URL,
		StrictGeo:  true,
	})

	err = p.Refresh(context.Background())
	// Should succeed because empty country means "no country data" which bypasses the strict_geo filter.
	if err != nil {
		t.Errorf("Refresh() with country-unknown proxies and strict_geo: unexpected error: %v", err)
	}
}

func TestRefresh_StrictGeoWithMixed(t *testing.T) {
	// Start a reachable TCP listener for health check.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test listener: %v", err)
	}
	defer ln.Close()
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s:%s:user1:pass1:US\n", host, portStr)
		fmt.Fprintln(w, "10.0.0.2:1081:user2:pass2:DE")
		fmt.Fprintln(w, "10.0.0.3:1082:user3:pass3:GB")
	}))
	defer ts.Close()

	p := NewProxyPool(ProxyPoolConfig{
		RefreshURL: ts.URL,
		StrictGeo:  true,
	})

	err = p.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh() with mixed proxies and strict_geo: unexpected error: %v", err)
	}

	// Only the US proxy should remain.
	if p.Len() != 1 {
		t.Errorf("After strict_geo Refresh, pool size = %d, want 1 (only US)", p.Len())
	} else {
		if p.proxies[0].Country != "US" {
			t.Errorf("Remaining proxy country = %q, want %q", p.proxies[0].Country, "US")
		}
	}
}

func TestRefresh_NonStrictGeoAcceptsAll(t *testing.T) {
	// Start a reachable TCP listener for health check.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start test listener: %v", err)
	}
	defer ln.Close()
	host, portStr, _ := net.SplitHostPort(ln.Addr().String())

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s:%s:user1:pass1:US\n", host, portStr)
		fmt.Fprintf(w, "%s:%s:user2:pass2:DE\n", host, portStr)
	}))
	defer ts.Close()

	p := NewProxyPool(ProxyPoolConfig{
		RefreshURL: ts.URL,
		StrictGeo:  false,
	})

	err = p.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh() with non-strict_geo: unexpected error: %v", err)
	}

	if p.Len() != 2 {
		t.Errorf("After non-strict_geo Refresh, pool size = %d, want 2", p.Len())
	}
}

func TestRefresh_EmptyListWithStrictGeo(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "# only comments\n# no actual proxies here")
	}))
	defer ts.Close()

	p := NewProxyPool(ProxyPoolConfig{
		RefreshURL: ts.URL,
		StrictGeo:  true,
	})

	err := p.Refresh(context.Background())
	if err == nil {
		t.Fatal("Refresh() with empty list and strict_geo: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "empty proxy list") {
		t.Errorf("Refresh() error = %q, want error about 'empty proxy list'", err)
	}
}

func TestRefresh_EmptyListWithoutStrictGeo(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return an empty list.
	}))
	defer ts.Close()

	p := NewProxyPool(ProxyPoolConfig{
		RefreshURL: ts.URL,
		StrictGeo:  false,
	})

	err := p.Refresh(context.Background())
	if err != nil {
		t.Errorf("Refresh() with empty list and non-strict_geo: unexpected error: %v", err)
	}
}

func TestRefresh_NoRefreshURL(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	err := p.Refresh(context.Background())
	if err != nil {
		t.Errorf("Refresh() with no URL: unexpected error: %v", err)
	}
}

func TestRefresh_HealthCheckFailureFallback(t *testing.T) {
	// When all proxies fail health check, non-strict mode should still accept them.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Use unreachable ports so health checks fail.
		fmt.Fprintln(w, "127.0.0.1:1:user1:pass1:US")
		fmt.Fprintln(w, "127.0.0.1:2:user2:pass2:DE")
	}))
	defer ts.Close()

	p := NewProxyPool(ProxyPoolConfig{
		RefreshURL: ts.URL,
		StrictGeo:  false,
	})

	err := p.Refresh(context.Background())
	if err != nil {
		t.Errorf("Refresh() with all unhealthy and non-strict_geo: unexpected error: %v", err)
	}
	if p.Len() != 2 {
		t.Errorf("After Refresh with fallback, pool size = %d, want 2", p.Len())
	}
	for _, e := range p.proxies {
		if e.Alive {
			t.Errorf("Proxy %s:%d should be marked as dead after failed health check", e.Host, e.Port)
		}
	}
}

func TestRefresh_HTTPError(t *testing.T) {
	// Use a URL that will fail to connect.
	p := NewProxyPool(ProxyPoolConfig{
		RefreshURL: "http://127.0.0.1:1/nonexistent",
	})

	err := p.Refresh(context.Background())
	if err == nil {
		t.Error("Refresh() with unreachable URL: expected error, got nil")
	}
}

func TestStart_NoRefreshURL(t *testing.T) {
	// Start with no refresh URL should return immediately without starting goroutines.
	p := NewProxyPool(ProxyPoolConfig{})
	p.Start(context.Background())
	// No goroutine started - no way to verify other than not panicking.
}

func TestConcurrentNextAndStats(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	p.proxies = []*ProxyEntry{
		{Host: "1.1.1.1", Port: 1080, Alive: true},
		{Host: "2.2.2.2", Port: 1081, Alive: true},
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				p.Next()
				p.Len()
				p.Stats()
			}
		}()
	}
	wg.Wait()
}

func TestConcurrentMarkFailureAndNext(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{})
	entry := &ProxyEntry{Host: "1.1.1.1", Port: 1080, Alive: true}
	p.proxies = []*ProxyEntry{entry}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				p.MarkFailure(entry)
				p.Next()
			}
		}()
	}
	wg.Wait()
}

// ── parseInt Tests ─────────────────────────────────────────────────────────

func TestParseInt(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"1080", 1080},
		{"0", 0},
		{"99999", 99999},
		{"-1", -1},
		{"notanumber", 0}, // Sscanf returns 0 on failure
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := parseInt(tt.input); got != tt.want {
				t.Errorf("parseInt(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// ── ProxyPoolConfig Defaults Tests ─────────────────────────────────────────

func TestNewProxyPool_ZeroIntervalUsesDefault(t *testing.T) {
	p := NewProxyPool(ProxyPoolConfig{RefreshInterval: 0})
	if p.interval != 30*time.Minute {
		t.Errorf("interval = %v, want 30m default", p.interval)
	}
}

func TestNewProxyPool_NegativeMaxFailuresDefaults(t *testing.T) {
	// MaxFailures is not stored on the struct, but the constructor applies
	// defaults for unset values. Verify that -1 doesn't cause issues.
	_ = NewProxyPool(ProxyPoolConfig{MaxFailures: -1})
	_ = NewProxyPool(ProxyPoolConfig{MaxFailures: 0})
	_ = NewProxyPool(ProxyPoolConfig{MaxFailures: 7})
}



// ── Edge Cases Tests ───────────────────────────────────────────────────────

func TestNext_WhenOnlyDeadProxiesWithIndexWrap(t *testing.T) {
	// Verify Next() correctly wraps the current index pointer even when
	// all proxies are dead.
	p := NewProxyPool(ProxyPoolConfig{})
	p.proxies = []*ProxyEntry{
		{Host: "1.1.1.1", Port: 1080, Alive: false, Failures: 5},
		{Host: "2.2.2.2", Port: 1081, Alive: false, Failures: 5},
	}

	// First call: should return first proxy as fallback, advance current to 1.
	got1 := p.Next()
	if got1 == nil || got1.Host != "1.1.1.1" {
		t.Errorf("Next() #1 = %v, want 1.1.1.1 fallback", got1)
	}
	// current index is now 1 (wrapped from 0 after full scan).

	// Second call: should scan starting at index 1, find all dead,
	// fallback to index 0.
	got2 := p.Next()
	if got2 == nil || got2.Host != "1.1.1.1" {
		t.Errorf("Next() #2 = %v, want 1.1.1.1 fallback", got2)
	}
}
