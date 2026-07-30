package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

var (
	userHomeDir = os.UserHomeDir
	loadDotenv  = godotenv.Load

	errDotenvLoad = errors.New("dotenv dosyası yüklenemedi")
)

// Config, proxy çalışması için gereken temel yapılandırmayı taşır.
//
// StealthConfig ve DashboardConfig, sırasıyla JA3 TLS fingerprint impersonation
// ve birleşik proxy monitoring dashboard'u için ek yapılandırma sağlar.
//
// ## Kullanım örneği
//
// ```go
// cfg, err := config.Load()
//
//	if err != nil {
//		return err
//	}
//
// fmt.Println(cfg.Addr)
// fmt.Println(cfg.CredentialsPath)
// ```
type Config struct {
	Addr            string
	APIBaseURL      string
	Model           string
	ProxyAPIKey     string
	CredentialsPath string

	// AuthTokens is a comma-separated list of Freebuff auth tokens for
	// multi-token rotation. Set AUTH_TOKENS="token1,token2,token3".
	AuthTokens []string

	// Stealth configures JA3 TLS fingerprint impersonation.
	// Set STEALTH_PROFILE to "chrome120", "firefox120", "safari17", or "random".
	// Set STEALTH_ENABLED=false to disable stealth transport.
	Stealth StealthConfig

	// Dashboard configures the built-in proxy monitoring dashboard.
	// Set DASHBOARD_ENABLED=false to disable.
	// Set DASHBOARD_ADDR to change the listen address (default: :9091).
	Dashboard DashboardConfig
}

// StealthConfig controls the JA3 TLS fingerprint impersonation layer.
type StealthConfig struct {
	Enabled bool   // Enable stealth transport (default: false)
	Profile string // Browser profile: chrome120, firefox120, safari17, random

	// ProxyURL is the SOCKS5 proxy URL or Webshare proxy list URL.
	// Set PROXY_URL to enable SOCKS5 proxy rotation.
	ProxyURL string

	// ProxyRefreshMins controls how often the proxy pool refreshes.
	// Set PROXY_REFRESH_MINS to change interval (default: 30).
	ProxyRefreshMins int

	// StrictGeo if true, ONLY US proxies are accepted.
	// Set PROXY_STRICT_GEO=true to fail-closed on non-US egress.
	StrictGeo bool

	// GeoVerify if true, performs external geo-verification via ip-api.com
	// batch API to verify proxy countries instead of trusting the proxy
	// list's self-reported country. Only effective when StrictGeo is true.
	// Set PROXY_GEO_VERIFY=true to enable.
	GeoVerify bool
}

// DashboardConfig controls the built-in proxy monitoring dashboard.
type DashboardConfig struct {
	Enabled bool   // Enable dashboard (default: true)
	Addr    string // Dashboard listen address (default: :9091)
	Prefix  string // URL path prefix (default: /dashboard)
}

// Load, .env dosyasını, varsayılan değerleri ve ortam değişkeni geçersiz kılmalarını yükler.
func Load() (Config, error) {
	if err := loadDotenv(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("load dotenv: %w", errDotenvLoad)
	}

	credentialsPath := os.Getenv("FREEBUFF_CREDENTIALS_PATH")
	if credentialsPath == "" {
		homeDir, err := userHomeDir()
		if err != nil {
			return Config{}, err
		}

		credentialsPath = filepath.Join(homeDir, ".config", "manicode", "credentials.json")
	}

	return Config{
		Addr:            envOr("FREEBUFF_PROXY_ADDR", "127.0.0.1:1455"),
		APIBaseURL:      normalizeAPIBaseURL(envOr("FREEBUFF_API_BASE_URL", "https://www.codebuff.com")),
		Model:           envOr("FREEBUFF_MODEL", "deepseek/deepseek-v4-pro"),
		ProxyAPIKey:     envOr("FREEBUFF_PROXY_API_KEY", ""),
		CredentialsPath: credentialsPath,
		AuthTokens:      splitComma(os.Getenv("AUTH_TOKENS")),
		Stealth: StealthConfig{
			Enabled:          envOr("STEALTH_ENABLED", "false") == "true",
			Profile:          envOr("STEALTH_PROFILE", "chrome120"),
			ProxyURL:         envOr("PROXY_URL", ""),
			ProxyRefreshMins: envOrInt("PROXY_REFRESH_MINS", 30),
			StrictGeo:        envOr("PROXY_STRICT_GEO", "false") == "true",
			GeoVerify:        envOr("PROXY_GEO_VERIFY", "false") == "true",
		},
		Dashboard: DashboardConfig{
			Enabled: envOr("DASHBOARD_ENABLED", "true") == "true",
			Addr:    envOr("DASHBOARD_ADDR", ":9091"),
			Prefix:  envOr("DASHBOARD_PREFIX", "/dashboard"),
		},
	}, nil
}

// envOr, boş olmayan ortam değişkeni değerini; yoksa varsayılanı döndürür.
func envOr(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	return value
}

// envOrInt, ortam değişkenini int olarak okur; yoksa varsayılanı döndürür.
func envOrInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscanf(value, "%d", &n); err != nil {
		return fallback
	}
	return n
}

// splitComma splits a comma-separated string into a trimmed string slice.
func splitComma(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func normalizeAPIBaseURL(value string) string {
	if strings.TrimRight(value, "/") == "https://codebuff.com" {
		return "https://www.codebuff.com"
	}

	return value
}
