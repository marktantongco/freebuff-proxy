// Package dashboard provides a real-time health and metrics dashboard for all
// freebuff proxies. It probes all proxy endpoints in parallel, exposes a JSON API,
// streams live updates via SSE, and serves a beautiful embedded web UI.
//
// Usage:
//
//	import "github.com/ferdiunal/freebuff-proxy/internal/dashboard"
//
//	engine := dashboard.NewProbeEngine(5 * time.Second)
//	engine.Start()
//	handler := dashboard.NewHandler(engine)
//	http.Handle("/dashboard/", handler)
package dashboard

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"
)

// ── Configuration ───────────────────────────────────────────────────────────

// ProxyTarget defines a proxy endpoint to monitor.
// ProxyTarget defines a proxy endpoint to monitor.
type ProxyTarget struct {
	Name     string `json:"name"`
	Port     int    `json:"port"`
	Addr     string `json:"addr"`
	Type     string `json:"type"`       // language/runtime
	HealthEP string `json:"health_ep"`  // health endpoint path
	ModelEP  string `json:"model_ep"`   // models endpoint path (optional)
	APIKey   string `json:"-"`  // Bearer token for authenticated endpoints (never serialized)
	Tags     string `json:"tags"`       // comma-separated features
}

// DefaultProxyTargets lists all known freebuff proxy endpoints.
var DefaultProxyTargets = []ProxyTarget{
	{Name: "freebuff-proxy",   Port: 1455, Addr: "127.0.0.1:1455", Type: "Go (Fiber)",    HealthEP: "/healthz", ModelEP: "/v1/models",    APIKey: os.Getenv("FREEBUFF_PROXY_API_KEY"), Tags: "session-mgmt,oauth,anthropic,openai"},
	{Name: "codebuff-proxy",   Port: 3211, Addr: "127.0.0.1:3211", Type: "TS (Convex)",   HealthEP: "/healthz", ModelEP: "/v1/models",    Tags: "run-cache,freebuff-auto-recovery,sse"},
	{Name: "freebuff-proxy-8080", Port: 8080, Addr: "127.0.0.1:8080", Type: "Go (Fiber)",    HealthEP: "/healthz", ModelEP: "/v1/models",    APIKey: os.Getenv("FREEBUFF_PROXY_API_KEY"), Tags: "stealth,multi-instance,ja3"},
	{Name: "jcode-proxy",      Port: 8088, Addr: "127.0.0.1:8088", Type: "Go (net/http)", HealthEP: "/health",  ModelEP: "/v1/models",    Tags: "api-bridge,streaming"},
	{Name: "jcode-daemon",     Port: 1456, Addr: "127.0.0.1:1456", Type: "Go (binary)",   HealthEP: "/health",  Tags: "ai-agent,gateway"},
	{Name: "warp-litellm",     Port: 4001, Addr: "127.0.0.1:4001", Type: "Go (net/http)", HealthEP: "/health",  ModelEP: "/v1/models",    Tags: "load-balancing,fallback"},
	{Name: "mimo-unified",     Port: 8877, Addr: "127.0.0.1:8877", Type: "Python",        HealthEP: "/health",  Tags: "free-tier,reasoning-cache"},
}

// ── Probe Result Types ──────────────────────────────────────────────────────

// ProxyStatus holds the result of probing a single proxy.
type ProxyStatus struct {
	Proxy     ProxyTarget    `json:"proxy"`
	Up        bool           `json:"up"`
	LatencyMs int64           `json:"latency_ms"`
	Error     string         `json:"error,omitempty"`
	Raw       map[string]any `json:"raw,omitempty"`
	Models    int            `json:"models,omitempty"`
	CheckedAt time.Time      `json:"checked_at"`
}

// ProbeResult holds the complete result of probing all proxies.
type ProbeResult struct {
	Statuses []ProxyStatus `json:"statuses"`
	Summary  ProbeSummary  `json:"summary"`
	Time     time.Time     `json:"time"`
}

// ProbeSummary aggregates probe results across all proxies.
type ProbeSummary struct {
	Total     int            `json:"total"`
	Up        int            `json:"up"`
	Down      int            `json:"down"`
	AvgMs     float64        `json:"avg_latency_ms"`
	MaxMs     int64          `json:"max_latency_ms"`
	MinMs     int64          `json:"min_latency_ms"`
	ByType    map[string]int `json:"by_type"`
	ByStatus  map[string]int `json:"by_status"`
}

// ── Probe Engine ────────────────────────────────────────────────────────────

// ProbeEngine periodically probes all proxy targets in parallel and stores results.
type ProbeEngine struct {
	mu          sync.RWMutex
	interval    time.Duration
	lastResult  *ProbeResult
	history     []*ProbeResult
	maxHistory  int
	subs        map[chan *ProbeResult]struct{}
	subsMu      sync.Mutex
	targets     []ProxyTarget
}

// NewProbeEngine creates a new ProbeEngine with the given probe interval.
func NewProbeEngine(interval time.Duration, targets []ProxyTarget) *ProbeEngine {
	if targets == nil {
		targets = DefaultProxyTargets
	}
	return &ProbeEngine{
		interval:   interval,
		maxHistory: 60,
		subs:       make(map[chan *ProbeResult]struct{}),
		targets:    targets,
	}
}

// Start begins probing on a background goroutine. The first probe runs immediately.
func (e *ProbeEngine) Start() {
	e.probeAll()
	ticker := time.NewTicker(e.interval)
	go func() {
		for range ticker.C {
			e.probeAll()
		}
	}()
}

// Subscribe returns a channel that receives new probe results.
func (e *ProbeEngine) Subscribe() chan *ProbeResult {
	ch := make(chan *ProbeResult, 4)
	e.subsMu.Lock()
	e.subs[ch] = struct{}{}
	e.subsMu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel.
func (e *ProbeEngine) Unsubscribe(ch chan *ProbeResult) {
	e.subsMu.Lock()
	delete(e.subs, ch)
	e.subsMu.Unlock()
}

// GetLastResult returns the most recent probe result (or nil if none).
func (e *ProbeEngine) GetLastResult() *ProbeResult {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.lastResult
}

// GetHistory returns all stored probe results.
func (e *ProbeEngine) GetHistory() []*ProbeResult {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]*ProbeResult, len(e.history))
	copy(out, e.history)
	return out
}

func (e *ProbeEngine) probeAll() {
	result := &ProbeResult{Time: time.Now()}

	var wg sync.WaitGroup
	ch := make(chan ProxyStatus, len(e.targets))

	for _, target := range e.targets {
		wg.Add(1)
		go func(t ProxyTarget) {
			defer wg.Done()
			ch <- probeProxy(t)
		}(target)
	}

	wg.Wait()
	close(ch)

	for status := range ch {
		result.Statuses = append(result.Statuses, status)
	}

	sort.Slice(result.Statuses, func(i, j int) bool {
		return result.Statuses[i].Proxy.Port < result.Statuses[j].Proxy.Port
	})

	result.Summary = computeSummary(result.Statuses)

	e.mu.Lock()
	e.lastResult = result
	e.history = append(e.history, result)
	if len(e.history) > e.maxHistory {
		e.history = e.history[len(e.history)-e.maxHistory:]
	}
	e.mu.Unlock()

	e.subsMu.Lock()
	for sub := range e.subs {
		select {
		case sub <- result:
		default:
		}
	}
	e.subsMu.Unlock()
}

func probeProxy(target ProxyTarget) ProxyStatus {
	start := time.Now()
	status := ProxyStatus{
		Proxy:     target,
		CheckedAt: time.Now(),
	}

	client := &http.Client{Timeout: 5 * time.Second}
	bearer := ""
	if target.APIKey != "" {
		bearer = "Bearer " + target.APIKey
	}

	body, err := fetchWithClient(client, fmt.Sprintf("http://%s%s", target.Addr, target.HealthEP), bearer)
	if err != nil {
		body, err = fetchWithClient(client, fmt.Sprintf("http://%s/", target.Addr), bearer)
		if err != nil {
			status.Error = err.Error()
			status.LatencyMs = time.Since(start).Milliseconds()
			return status
		}
	}

	status.LatencyMs = time.Since(start).Milliseconds()
	status.Up = true

	if len(body) > 0 && body[0] == '{' {
		var raw map[string]any
		if json.Unmarshal(body, &raw) == nil {
			status.Raw = raw
		}
	}

	if target.ModelEP != "" {
		body3, err3 := fetchWithClient(client, fmt.Sprintf("http://%s%s", target.Addr, target.ModelEP), bearer)
		if err3 == nil && len(body3) > 0 {
			var modelsResp map[string]any
			if json.Unmarshal(body3, &modelsResp) == nil {
				if data, ok := modelsResp["data"].([]any); ok {
					status.Models = len(data)
				}
				if data2, ok := modelsResp["models"].([]any); ok {
					status.Models = len(data2)
				}
			}
		}
	}

	return status
}

func fetchWithClient(client *http.Client, url string, bearer string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if bearer != "" {
		req.Header.Set("Authorization", bearer)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 65536))
}

func computeSummary(statuses []ProxyStatus) ProbeSummary {
	s := ProbeSummary{
		ByType:   make(map[string]int),
		ByStatus: make(map[string]int),
		MinMs:    -1,
	}
	for _, st := range statuses {
		s.Total++
		s.ByType[st.Proxy.Type]++
		if st.Up {
			s.Up++
			s.ByStatus["up"]++
			s.AvgMs += float64(st.LatencyMs)
			if st.LatencyMs > s.MaxMs {
				s.MaxMs = st.LatencyMs
			}
			if s.MinMs == -1 || st.LatencyMs < s.MinMs {
				s.MinMs = st.LatencyMs
			}
		} else {
			s.Down++
			s.ByStatus["down"]++
			if s.MinMs == -1 {
				s.MinMs = 0
			}
		}
	}
	if s.Up > 0 {
		s.AvgMs = s.AvgMs / float64(s.Up)
	}
	if s.MinMs == -1 {
		s.MinMs = 0
	}
	return s
}

// ── Dashboard HTML (embedded) ───────────────────────────────────────────────

//go:embed dashboard.html
var dashboardHTML string

// ── HTTP Handler ────────────────────────────────────────────────────────────

// NewHandler returns an http.Handler that serves the dashboard UI and API endpoints
// mounted at the given path prefix (e.g., "/dashboard").
//
// Routes:
//   - GET {prefix}/           — dashboard HTML UI
//   - GET {prefix}/api/status — JSON probe results
//   - GET {prefix}/api/status/stream — SSE live stream
//   - GET {prefix}/api/proxies — configured proxy targets
//   - GET {prefix}/api/status/history — probe history
func NewHandler(engine *ProbeEngine, pathPrefix string) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc(pathPrefix+"/api/status", handleStatus(engine))
	mux.HandleFunc(pathPrefix+"/api/status/stream", handleSSE(engine))
	mux.HandleFunc(pathPrefix+"/api/proxies", handleProxies(engine))
	mux.HandleFunc(pathPrefix+"/api/status/history", handleHistory(engine))
	mux.HandleFunc(pathPrefix+"/", handleDashboardRoot(pathPrefix))

	return mux
}

// handleDashboardRoot returns a handler that serves the dashboard HTML for the
// configured prefix path. It handles both "/" (root prefix "") and "/dashboard/".
func handleDashboardRoot(prefix string) http.HandlerFunc {
	// Determine which paths are valid for the HTML dashboard.
	validPaths := map[string]bool{
		"/": true,
	}
	if prefix != "" {
		validPaths[prefix+"/"] = true
		validPaths[prefix] = true
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if !validPaths[r.URL.Path] {
			http.NotFound(w, r)
			return
		}
		tmpl, err := template.New("dashboard").Parse(dashboardHTML)
		if err != nil {
			http.Error(w, "Template error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		tmpl.Execute(w, map[string]any{
			"Title": "Freebuff Unified Dashboard",
		})
	}
}

func handleStatus(engine *ProbeEngine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result := engine.GetLastResult()
		if result == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no data yet"})
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func handleSSE(engine *ProbeEngine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "SSE not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		ch := engine.Subscribe()
		defer engine.Unsubscribe(ch)

		if result := engine.GetLastResult(); result != nil {
			data, _ := json.Marshal(result)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}

		for {
			select {
			case result := <-ch:
				data, _ := json.Marshal(result)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	}
}

func handleProxies(engine *ProbeEngine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"count":   len(engine.targets),
			"proxies": engine.targets,
		})
	}
}

func handleHistory(engine *ProbeEngine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		history := engine.GetHistory()
		writeJSON(w, http.StatusOK, map[string]any{
			"count":   len(history),
			"history": history,
		})
	}
}

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/dashboard/" && r.URL.Path != "/dashboard" {
		http.NotFound(w, r)
		return
	}
	tmpl, err := template.New("dashboard").Parse(dashboardHTML)
	if err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.Execute(w, map[string]any{
		"Title": "Freebuff Unified Dashboard",
	})
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// LogMiddleware logs requests that take longer than 100ms.
func LogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &loggingResponseWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(lw, r)
		dur := time.Since(start)
		if dur > 100*time.Millisecond {
			log.Printf("[dashboard] %s %s → %d (%s)", r.Method, r.URL.Path, lw.status, dur.Round(time.Millisecond))
		}
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
}

func (l *loggingResponseWriter) WriteHeader(status int) {
	l.status = status
	l.ResponseWriter.WriteHeader(status)
}
