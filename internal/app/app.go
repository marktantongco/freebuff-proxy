// Package app, yapılandırmadan çalıştırılabilir Fiber uygulamasını kurar.
//
// ## Kullanım örneği
//
// ```go
// cfg, err := config.Load()
// if err != nil { return err }
// fiberApp, err := app.NewApp(cfg)
// if err != nil { return err }
// _ = fiberApp.Listen(cfg.Addr)
// ```
package app

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/ferdiunal/freebuff-proxy/internal/cache"
	"github.com/ferdiunal/freebuff-proxy/internal/config"
	"github.com/ferdiunal/freebuff-proxy/internal/credentials"
	"github.com/ferdiunal/freebuff-proxy/internal/dashboard"
	"github.com/ferdiunal/freebuff-proxy/internal/freebuff"
	"github.com/ferdiunal/freebuff-proxy/internal/httpapi"
	"github.com/ferdiunal/freebuff-proxy/internal/session"
	"github.com/ferdiunal/freebuff-proxy/internal/stealth"
)

const defaultInstanceID = "freebuff-proxy"

// NewApp, uygulama bağımlılıklarını doğrular ve HTTP API Fiber uygulamasını döndürür.
//
// RunCache, codebuff-proxy'den taşınan run_id önbellekleme deseniyle
// tekrarlanan agent run ID oluşturma çağrılarını önler.
//
// StealthConfig etkinleştirilmişse, upstream istekler JA3 TLS fingerprint
// impersonation ile yapılır (freebuff-stealth).
//
// ProxyURL ayarlanmışsa, SOCKS5 proxy pool otomatik olarak başlatılır
// ve tüm upstream bağlantıları proxy üzerinden yönlendirilir.
//
// DashboardConfig etkinleştirilmişse, ayrı bir HTTP sunucusunda
// birleşik proxy monitoring dashboard çalıştırılır.
//
// ## Kullanım örneği
//
// ```go
// fiberApp, err := app.NewApp(cfg)
//
//	if err != nil {
//		return err
//	}
//
// err = fiberApp.Listen(cfg.Addr)
// ```
func NewApp(cfg config.Config) (*fiber.App, error) {
	ctx := context.Background()
	store := credentials.FileStore{Path: cfg.CredentialsPath}

	// ── Run Cache ──────────────────────────────────────────────────────────
	runCache := cache.NewRunCache(cache.DefaultConfig())

	// ── SOCKS5 Proxy Pool ──────────────────────────────────────────────────
	// Use concrete type for internal operations (Start, Len, NewClient),
	// and PoolStatsProvider interface for Options so nil stays nil.
	var proxyPoolInternal *stealth.ProxyPool
	var proxyPoolOpt httpapi.PoolStatsProvider
	if cfg.Stealth.ProxyURL != "" {
		proxyPoolInternal = stealth.NewProxyPool(stealth.ProxyPoolConfig{
			RefreshURL:      cfg.Stealth.ProxyURL,
			RefreshInterval: time.Duration(cfg.Stealth.ProxyRefreshMins) * time.Minute,
			StrictGeo:       cfg.Stealth.StrictGeo,
			GeoVerify:       cfg.Stealth.GeoVerify,
		})
		proxyPoolInternal.Start(ctx)
		proxyPoolOpt = proxyPoolInternal
		log.Printf("[proxy-pool] Started with %d initial proxies (strict_geo=%v, geo_verify=%v)",
			proxyPoolInternal.Len(), cfg.Stealth.StrictGeo, cfg.Stealth.GeoVerify)
	}

	// ── Stealth HTTP Client (JA3 fingerprint impersonation) ────────────────
	stealthProfile := ""
	if cfg.Stealth.Enabled {
		stealthProfile = cfg.Stealth.Profile
	}

	client, err := freebuff.NewClient(cfg.APIBaseURL, nil, runCache, stealthProfile, proxyPoolInternal)
	if err != nil {
		return nil, err
	}

	// ── Session Manager + Chat Service ─────────────────────────────────────
	manager := session.NewManager(store, client, defaultInstanceID)

	// ── Token Pool (multi-token rotation) ───────────────────────────────────
	// Reads AUTH_TOKENS from config. Exposes LenLocked() in /healthz so
	// operators can see when tokens are stuck on unexpected models.
	// Use concrete type for MarkTokenLocked callback, interface for Options.
	var tokenPoolInternal *session.Pool
	var tokenPoolOpt httpapi.PoolStatsProvider
	if len(cfg.AuthTokens) > 0 {
		tokenPoolInternal = session.NewPool(cfg.AuthTokens, 10, 55*time.Minute)
		tokenPoolOpt = tokenPoolInternal
		log.Printf("[token-pool] Initialized with %d tokens", len(cfg.AuthTokens))
	}

	// Wire OnModelLocked callback so SessionModelLocked marks the token
	// in the pool, making locked-model counts visible in /healthz.
	manager.OnModelLocked = func(token, model string) {
		if tokenPoolInternal != nil {
			tokenPoolInternal.MarkTokenLocked(token)
			log.Printf("[token-pool] Token %s locked on model %s", token, model)
		}
	}

	chat := httpapi.FreebuffChatService{
		Store:    store,
		Sessions: manager,
		Upstream: client,
	}

	fiberApp := httpapi.NewApp(httpapi.Options{
		Model:       cfg.Model,
		ProxyAPIKey: cfg.ProxyAPIKey,
		Chat:        chat,
		TokenPool:   tokenPoolOpt,
		ProxyPool:   proxyPoolOpt,
	})

	// ── Dashboard Server (separate port) ───────────────────────────────────
	if cfg.Dashboard.Enabled {
		startDashboard(cfg.Dashboard)
	}

	return fiberApp, nil
}

// startDashboard starts the unified monitoring dashboard on a separate HTTP server.
func startDashboard(dcfg config.DashboardConfig) {
	engine := dashboard.NewProbeEngine(5*time.Second, nil)
	engine.Start()

	mux := dashboard.NewHandler(engine, "")

	server := &http.Server{
		Addr:    dcfg.Addr,
		Handler: dashboard.LogMiddleware(mux),
	}

	go func() {
		log.Printf("[dashboard] Starting on %s (prefix: %s)", dcfg.Addr, dcfg.Prefix)
		log.Printf("[dashboard] Open: http://%s/", dcfg.Addr)
		log.Printf("[dashboard] API:  http://%s/api/status", dcfg.Addr)
		log.Printf("[dashboard] SSE:  http://%s/api/status/stream", dcfg.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[dashboard] Server error: %v", err)
		}
	}()
}


