package freebuff

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/ferdiunal/freebuff-proxy/internal/cache"
	"github.com/ferdiunal/freebuff-proxy/internal/stealth"
)

const (
	sessionEndpointPath          = "/api/v1/freebuff/session"
	headerAuthorization          = "Authorization"
	headerInstanceID             = "x-freebuff-instance-id"
	headerModel                  = "x-freebuff-model"
	defaultResponseHeaderTimeout = 30 * time.Second
)

// Client, Freebuff oturum ve sohbet uç noktalarına istek gönderen HTTP istemcisidir.
//
// RunCache, tekrarlanan agent run ID oluşturma çağrılarını önlemek için
// FNV-1a hash anahtarlarıyla run_id'leri önbelleğe alır.
// Ported from codebuff-proxy's run_id caching pattern.
//
// ## Kullanım örneği
//
// ```go
// client, err := freebuff.NewClient("https://proxy.example.com", nil, nil)
//
//	if err != nil {
//		return err
//	}
//
// session, err := client.StartSession(ctx, token, "instance-1", "claude-sonnet")
//
//	if err != nil {
//		return err
//	}
//
// fmt.Println(session.Status)
// ```
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	RunCache   *cache.RunCache
}

// NewClient, temel Freebuff proxy adresinden yeni bir oturum istemcisi oluşturur.
// runCache parametresi isteğe bağlıdır; nil değer kabul edilir (önbellekleme devre dışı).
// stealthProfile boş değilse, JA3 TLS fingerprint impersonation etkinleştirilir.
// proxyPool varsa, tüm upstream bağlantıları SOCKS5 proxy pool üzerinden
// round-robin yönlendirilir.
func NewClient(baseURL string, httpClient *http.Client, runCache *cache.RunCache, stealthProfile string, proxyPool *stealth.ProxyPool) (*Client, error) {
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse freebuff base url: %w", err)
	}

	if parsedURL.Scheme == "" || parsedURL.Host == "" {
		return nil, fmt.Errorf("parse freebuff base url: missing scheme or host")
	}

	if httpClient == nil {
		httpClient = newHTTPClient(stealthProfile, proxyPool)
	}

	return &Client{
		baseURL:    parsedURL,
		httpClient: httpClient,
		RunCache:   runCache,
	}, nil
}

// newHTTPClient creates an HTTP client with optional JA3 stealth transport
// and optional SOCKS5 proxy rotation.
// If stealthProfile is non-empty, it creates a stealth client that impersonates
// the specified browser's TLS fingerprint (chrome120, firefox120, safari17, random).
// If proxyPool is non-nil, each outbound connection routes through a rotating
// SOCKS5 proxy from the pool.
func newHTTPClient(stealthProfile string, proxyPool *stealth.ProxyPool) *http.Client {
	if stealthProfile == "" && proxyPool == nil {
		return defaultHTTPClient()
	}

	var profile *stealth.Profile
	switch stealthProfile {
	case "chrome120":
		profile = stealth.ProfileChrome120
	case "firefox120":
		profile = stealth.ProfileFirefox120
	case "safari17":
		profile = stealth.ProfileSafari17
	case "random":
		profile = stealth.ProfileRandom
	default:
		if proxyPool == nil {
			return defaultHTTPClient()
		}
		// No stealth profile, but have a proxy pool — use default profile.
		profile = stealth.DefaultProfile
	}

	return stealth.NewClient(stealth.ClientConfig{
		Profile:         profile,
		Timeout:         defaultResponseHeaderTimeout * 6, // 180s
		SanitizeHeaders: true,
		ProxyPool:       proxyPool,
	})
}

// GetSession, mevcut Freebuff oturum durumunu getirir.
func defaultHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = defaultResponseHeaderTimeout

	return &http.Client{Transport: transport}
}

func (c *Client) GetSession(ctx context.Context, token string, instanceID string) (Session, error) {
	return c.doSessionRequest(ctx, http.MethodGet, token, instanceID, "")
}

// StartSession, istenen model için yeni Freebuff oturumu başlatır.
func (c *Client) StartSession(ctx context.Context, token string, instanceID string, model string) (Session, error) {
	return c.doSessionRequest(ctx, http.MethodPost, token, instanceID, model)
}

// EndSession, mevcut Freebuff oturumunu sonlandırır.
func (c *Client) EndSession(ctx context.Context, token string, instanceID string) (Session, error) {
	return c.doSessionRequest(ctx, http.MethodDelete, token, instanceID, "")
}

func (c *Client) doSessionRequest(ctx context.Context, method string, token string, instanceID string, model string) (Session, error) {
	requestURL := c.baseURL.ResolveReference(&url.URL{Path: sessionEndpointPath})

	req, err := http.NewRequestWithContext(ctx, method, requestURL.String(), nil)
	if err != nil {
		return Session{}, fmt.Errorf("build freebuff session request: %w", err)
	}

	if token != "" {
		req.Header.Set(headerAuthorization, "Bearer "+token)
	}
	if instanceID != "" {
		req.Header.Set(headerInstanceID, instanceID)
	}
	if model != "" {
		req.Header.Set(headerModel, model)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Session{}, fmt.Errorf("send freebuff session request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		apiErr, err := decodeAPIError(resp)
		if err != nil {
			return Session{}, fmt.Errorf("decode freebuff error response: %w", err)
		}

		return Session{}, apiErr
	}

	var session Session
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return Session{}, fmt.Errorf("decode freebuff session response: %w", err)
	}

	return session, nil
}

func decodeAPIError(resp *http.Response) (*APIError, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read freebuff error response: %w", err)
	}

	apiErr := &APIError{
		StatusCode: resp.StatusCode,
		Message:    http.StatusText(resp.StatusCode),
	}

	if len(body) == 0 {
		return apiErr, nil
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return apiErr, nil
	}

	if code, ok := payload["code"].(string); ok {
		apiErr.Code = code
	}
	if message, ok := payload["message"].(string); ok {
		apiErr.Message = message
	}
	if apiErr.Code == "" {
		if code, ok := payload["error"].(string); ok {
			apiErr.Code = code
		}
	}
	if apiErr.Message == "" {
		if message, ok := payload["error"].(string); ok {
			apiErr.Message = message
		}
	}

	return apiErr, nil
}
