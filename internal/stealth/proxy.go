package stealth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

// ProxyEntry is a single SOCKS5 proxy with credentials.
type ProxyEntry struct {
	Host     string    `json:"host"`
	Port     int       `json:"port"`
	User     string    `json:"user,omitempty"`
	Pass     string    `json:"-"`
	Country  string    `json:"country,omitempty"`
	LastUsed time.Time `json:"last_used"`
	Failures int       `json:"failures"`
	Alive    bool      `json:"alive"`
}

// URL returns the SOCKS5 URL for this proxy entry.
func (p *ProxyEntry) URL() string {
	if p.User != "" {
		return fmt.Sprintf("socks5://%s:%s@%s:%d", p.User, p.Pass, p.Host, p.Port)
	}
	return fmt.Sprintf("socks5://%s:%d", p.Host, p.Port)
}

// Dialer returns a proxy.Dialer for this entry.
func (p *ProxyEntry) Dialer() (proxy.Dialer, error) {
	u, err := url.Parse(p.URL())
	if err != nil {
		return nil, err
	}
	return proxy.FromURL(u, proxy.Direct)
}

// ProxyPool manages a rotating pool of SOCKS5 proxies with background refresh.
//
// Features:
//   - Round-robin proxy rotation
//   - Background fetch from Webshare/remote URL
//   - TCP health checking (lightweight connect)
//   - Failure tracking and eviction
//   - Geo filtering (strict_geo mode)
//   - External geo-verification via ip-api.com (geo_verify mode)
//   - Thread-safe operations
type ProxyPool struct {
	mu         sync.RWMutex
	proxies    []*ProxyEntry
	current    int
	refreshURL string
	interval   time.Duration
	strictGeo  bool
	geoVerify  bool

	// HTTP client for fetching proxy lists (no stealth needed for fetch).
	fetchClient *http.Client
}

// ProxyPoolConfig configures a ProxyPool.
type ProxyPoolConfig struct {
	// RefreshURL is the URL to fetch SOCKS5 proxies from (Webshare format).
	RefreshURL string

	// RefreshInterval is how often to refresh the proxy list. Default: 30 min.
	RefreshInterval time.Duration

	// StrictGeo if true, proxies without US country code are rejected.
	// If false, geo-unknown proxies are accepted. Default: false.
	StrictGeo bool

	// GeoVerify if true, performs external geo-verification via ip-api.com
	// batch API instead of trusting the proxy list's self-reported country.
	// Only effective when StrictGeo is true. Default: false.
	GeoVerify bool

	// MaxFailures before a proxy is evicted. Default: 3.
	MaxFailures int
}

// NewProxyPool creates a new ProxyPool with the given config.
func NewProxyPool(cfg ProxyPoolConfig) *ProxyPool {
	if cfg.RefreshInterval <= 0 {
		cfg.RefreshInterval = 30 * time.Minute
	}
	if cfg.MaxFailures <= 0 {
		cfg.MaxFailures = 3
	}
	return &ProxyPool{
		refreshURL: cfg.RefreshURL,
		interval:   cfg.RefreshInterval,
		strictGeo:  cfg.StrictGeo,
		geoVerify:  cfg.GeoVerify,
		fetchClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        5,
				IdleConnTimeout:     30 * time.Second,
				DisableKeepAlives:   true,
			},
		},
	}
}

// Start begins the background refresh goroutine.
func (p *ProxyPool) Start(ctx context.Context) {
	if p.refreshURL == "" {
		return
	}
	// Initial fetch.
	if err := p.Refresh(ctx); err != nil {
		log.Printf("[proxy-pool] Initial refresh failed: %v", err)
	}
	// Periodic refresh.
	go func() {
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := p.Refresh(ctx); err != nil {
					log.Printf("[proxy-pool] Refresh failed: %v", err)
				}
			}
		}
	}()
}

// geoVerifyBatchRequest describes a single IP lookup request for ip-api.com batch API.
type geoVerifyBatchRequest struct {
	Query string `json:"query"`
}

// geoVerifyBatchResponse describes a single response item from ip-api.com batch API.
type geoVerifyBatchResponse struct {
	Status      string `json:"status"`
	CountryCode string `json:"countryCode"`
	Query       string `json:"query"`
}

// geoVerifyProxies verifies the country of each proxy in the given slice
// by sending their IPs to ip-api.com batch API. Returns only proxies that
// are located in the US. On API failure, returns nil (fail-closed) so the
// caller's strict_geo check will correctly reject all entries.
func (p *ProxyPool) geoVerifyProxies(ctx context.Context, entries []*ProxyEntry) []*ProxyEntry {
	if len(entries) == 0 {
		return entries
	}

	// Build batch request.
	reqs := make([]geoVerifyBatchRequest, len(entries))
	for i, e := range entries {
		reqs[i] = geoVerifyBatchRequest{Query: e.Host}
	}

	body, err := json.Marshal(reqs)
	if err != nil {
		log.Printf("[proxy-pool] geo-verify marshal error: %v — returning empty (fail-closed)", err)
		return nil
	}

	verifyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(verifyCtx, "POST",
		"http://ip-api.com/batch", bytes.NewReader(body))
	if err != nil {
		log.Printf("[proxy-pool] geo-verify request error: %v — returning empty (fail-closed)", err)
		return nil
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.fetchClient.Do(httpReq)
	if err != nil {
		log.Printf("[proxy-pool] geo-verify http error: %v — returning empty (fail-closed)", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[proxy-pool] geo-verify status %d — returning empty (fail-closed)", resp.StatusCode)
		return nil
	}

	var results []geoVerifyBatchResponse
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		log.Printf("[proxy-pool] geo-verify decode error: %v — returning empty (fail-closed)", err)
		return nil
	}

	// Build lookup of US IPs.
	usIPs := make(map[string]bool, len(results))
	for _, r := range results {
		if r.Status == "success" && r.CountryCode == "US" {
			usIPs[r.Query] = true
		}
	}

	// Filter to only US proxies.
	var verified []*ProxyEntry
	for _, e := range entries {
		if usIPs[e.Host] {
			e.Country = "US"
			verified = append(verified, e)
		}
	}

	log.Printf("[proxy-pool] geo-verify: %d/%d proxies are US-located", len(verified), len(entries))
	return verified
}

// Refresh fetches a fresh proxy list from the configured URL, health-checks
// each proxy via TCP connect, and replaces the pool. In strict_geo mode,
// non-US proxies cause an error (fail closed). In non-strict mode, geo-unknown
// proxies are accepted (fail open). When geo_verify is enabled, proxy
// countries are verified via ip-api.com batch API before filtering.
func (p *ProxyPool) Refresh(ctx context.Context) error {
	if p.refreshURL == "" {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", p.refreshURL, nil)
	if err != nil {
		return fmt.Errorf("fetch proxy list: %w", err)
	}
	resp, err := p.fetchClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch proxy list: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB max
	if err != nil {
		return fmt.Errorf("read proxy list: %w", err)
	}

	entries := parseProxyList(string(data))
	if len(entries) == 0 {
		if p.strictGeo {
			return fmt.Errorf("proxy pool: empty proxy list with strict_geo enabled")
		}
		return nil
	}

	// External geo-verification (ip-api.com) if enabled.
	if p.geoVerify && p.strictGeo {
		entries = p.geoVerifyProxies(ctx, entries)
		if len(entries) == 0 {
			return fmt.Errorf("proxy pool: no US proxies after geo-verify (strict_geo)")
		}
	}

	// Health-check and filter.
	var healthy []*ProxyEntry
	for _, entry := range entries {
		if p.strictGeo && entry.Country != "" && !strings.EqualFold(entry.Country, "US") {
			continue // Reject non-US in strict mode
		}
		if p.healthCheck(entry) {
			entry.Alive = true
			healthy = append(healthy, entry)
		}
	}

	if len(healthy) == 0 {
		if p.strictGeo {
			return fmt.Errorf("proxy pool: no healthy US proxies after refresh (strict_geo)")
		}
		// In non-strict mode, accept all entries even if health-check failed.
		// This is the "fail open" fallback.
		healthy = entries
		for _, e := range healthy {
			e.Alive = false
		}
	}

	p.mu.Lock()
	p.proxies = healthy
	p.current = 0
	p.mu.Unlock()

	log.Printf("[proxy-pool] Refreshed: %d healthy proxies (strict_geo=%v, geo_verify=%v)",
		len(healthy), p.strictGeo, p.geoVerify)
	return nil
}

// Next returns the next healthy proxy in round-robin order, or nil if empty.
func (p *ProxyPool) Next() *ProxyEntry {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.proxies) == 0 {
		return nil
	}

	// Try up to len(proxies) times to find a healthy proxy.
	for i := 0; i < len(p.proxies); i++ {
		entry := p.proxies[p.current]
		p.current = (p.current + 1) % len(p.proxies)
		if entry.Alive && entry.Failures < 3 {
			entry.LastUsed = time.Now()
			return entry
		}
	}

	// All proxies are dead or unhealthy — return the first one anyway.
	entry := p.proxies[0]
	entry.LastUsed = time.Now()
	return entry
}

// MarkFailure increments the failure count for the given proxy.
// Proxies with too many failures are skipped by Next().
func (p *ProxyPool) MarkFailure(entry *ProxyEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.proxies {
		if e.Host == entry.Host && e.Port == entry.Port {
			e.Failures++
			if e.Failures >= 3 {
				e.Alive = false
			}
			break
		}
	}
}

// MarkSuccess resets the failure count for the given proxy.
func (p *ProxyPool) MarkSuccess(entry *ProxyEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.proxies {
		if e.Host == entry.Host && e.Port == entry.Port {
			e.Failures = 0
			e.Alive = true
			break
		}
	}
}

// Stats returns current pool statistics.
type PoolStats struct {
	Total   int            `json:"total"`
	Alive   int            `json:"alive"`
	Dead    int            `json:"dead"`
	ByCountry map[string]int `json:"by_country,omitempty"`
}

// Stats returns current pool statistics as a map for /healthz display.
func (p *ProxyPool) Stats() any {
	if p == nil {
		return map[string]any{"configured": false}
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	s := PoolStats{
		Total:     len(p.proxies),
		ByCountry: make(map[string]int),
	}
	for _, e := range p.proxies {
		if e.Alive {
			s.Alive++
		} else {
			s.Dead++
		}
		if e.Country != "" {
			s.ByCountry[e.Country]++
		}
	}
	return s
}

// Len returns the total number of proxies in the pool.
func (p *ProxyPool) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.proxies)
}

// healthCheck performs a lightweight TCP connect to verify the proxy is reachable.
func (p *ProxyPool) healthCheck(entry *ProxyEntry) bool {
	addr := net.JoinHostPort(entry.Host, fmt.Sprintf("%d", entry.Port))
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// parseProxyList parses proxy entries from text in Webshare format:
//   host:port:user:pass or host:port:user:pass:country
// Lines starting with # are comments.
func parseProxyList(data string) []*ProxyEntry {
	var entries []*ProxyEntry
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) < 4 {
			continue
		}
		entry := &ProxyEntry{
			Host: parts[0],
			Port: parseInt(parts[1]),
			User: parts[2],
			Pass: parts[3],
		}
		if len(parts) >= 5 {
			entry.Country = strings.TrimSpace(parts[4])
		}
		entries = append(entries, entry)
	}
	return entries
}

func parseInt(s string) int {
	var n int
	fmt.Sscanf(s, "%d", &n)
	return n
}
