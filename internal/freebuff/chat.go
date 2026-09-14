package freebuff

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/ferdiunal/freebuff-proxy/internal/cache"
	"github.com/ferdiunal/freebuff-proxy/internal/openai"
)

// cliUserAgent is the User-Agent the official Freebuff CLI pins on chat
// calls alone (ai-sdk openai-compatible client). Upstream's free-mode gate
// 403s chat requests that do not carry it with free_mode_cli_required.
const cliUserAgent = "ai-sdk/openai-compatible/1.0.0/codebuff"

// cliSystemMarker is the canonical identity prefix the CLI puts at the root
// of every free-mode system prompt. Upstream requires the first system
// message to OPEN with it (position 0, after whitespace trim only).
const cliSystemMarker = "You are Buffy, the strategic coding assistant. You are the AI agent behind the product, Freebuff, a tool where users can chat with you to code with AI for free."

const (
	chatEndpointPath       = "/api/v1/chat/completions"
	agentRunsEndpointPath  = "/api/v1/agent-runs"
	freebuffCostMode       = "free"
	defaultFreeAgentID     = "base2-free"
	chatErrorStageAgentRun = "agent_run"
	chatErrorStageChat     = "chat"
)

// Static snapshot of upstream's free-mode (agent, model) pairings. The
// trefeon fork resolves these from a live registry (dots→dashes naming, e.g.
// z-ai/glm-5.3-flash → base2-free-glm-5-3-flash); until this proxy grows
// one, keep this map in sync with upstream's free tier. Retired models
// (minimax-m2.7, kimi-k2.6) were dropped upstream 2026-09.
var freebuffAgentIDsByModel = map[string]string{
	"z-ai/glm-5.3-flash":         "base2-free-glm-5-3-flash",
	"deepseek/deepseek-v4-pro":   "base2-free-deepseek",
	"deepseek/deepseek-v4-flash": "base2-free-deepseek-flash",
	"freebuff-chat-verified":     "base2-free",
}

var canonicalFreebuffModelsByAlias = map[string]string{
	"deepseek-v4-pro":        "deepseek/deepseek-v4-pro",
	"deepseek-v4-flash":      "deepseek/deepseek-v4-flash",
	"deepseek-v3.1-terminus": "deepseek/deepseek-v4-pro",
}

// Complete, Freebuff upstream sohbet uç noktasına Codebuff CLI uyumlu non-stream istek gönderir.
//
// Hata durumunda (session_expired, waiting_room_queued vb.) otomatik oturum
// kurtarma dener. Ported from codebuff-proxy's retryWithFreshFreebuffSession().
//
// ## Kullanım örneği
//
// ```go
//
//	session := freebuff.Session{InstanceID: "freebuff-proxy"}
//	text, err := client.Complete(ctx, token, session, openai.ChatCompletionRequest{
//		Model:    "deepseek/deepseek-v4-pro",
//		Messages: []openai.ChatMessage{{Role: "user", Content: "Merhaba"}},
//	})
//
//	if err != nil {
//		return err
//	}
//
// fmt.Println(text)
// ```
func (c *Client) Complete(ctx context.Context, token string, activeSession Session, req openai.ChatCompletionRequest) (string, error) {
	upstreamChatReq := normalizeChatCompletionRequest(req)
	upstreamChatReq.Model = modelForActiveSession(activeSession, upstreamChatReq.Model)
	runID, err := c.startAgentRun(ctx, token, upstreamChatReq.Model)
	if err != nil {
		return "", err
	}

	upstreamReq, err := buildUpstreamChatRequest(upstreamChatReq, false, runID, activeSession)
	if err != nil {
		return "", err
	}

	resp, err := c.doChatRequest(ctx, token, upstreamReq, "application/json")
	if err != nil {
		return "", err
	}

	// Read body before deciding on recovery to preserve error context.
	bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	resp.Body.Close()

	if readErr != nil && resp.StatusCode >= 400 {
		return "", chatDecodeError()
	}

	// ── Session Recovery ────────────────────────────────────────────────────
	// Only trigger on session-specific error codes (not all 409/429).
	if resp.StatusCode >= 400 && ShouldRecoverSessionFromResponse(resp.StatusCode, bodyBytes) {
		cfg := DefaultRecoverSessionConfig()
		cfg.InstanceID = activeSession.InstanceID
		newSession, retryOK := c.RetryWithFreshSession(ctx, token, upstreamChatReq.Model, cfg)
		if retryOK {
			return c.doComplete(ctx, token, newSession, upstreamChatReq)
		}
		return "", makeChatStatusError(resp.StatusCode, bodyBytes)
	}

	// ── Normal Error Path ───────────────────────────────────────────────────
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", makeChatStatusError(resp.StatusCode, bodyBytes)
	}

	// ── Success Path ────────────────────────────────────────────────────────
	var payload openai.ChatCompletionResponse
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return "", chatDecodeError()
	}

	if len(payload.Choices) == 0 || payload.Choices[0].Message == nil {
		return "", chatDecodeError()
	}

	return payload.Choices[0].Message.Content, nil
}

// doComplete sends a chat request and decodes the response.
// Shared between initial call and session recovery retry.
func (c *Client) doComplete(ctx context.Context, token string, session Session, req openai.ChatCompletionRequest) (string, error) {
	runID, err := c.startAgentRun(ctx, token, req.Model)
	if err != nil {
		return "", err
	}

	upstreamReq, err := buildUpstreamChatRequest(req, false, runID, session)
	if err != nil {
		return "", err
	}

	resp, err := c.doChatRequest(ctx, token, upstreamReq, "application/json")
	if err != nil {
		return "", err
	}

	bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	resp.Body.Close()

	if readErr != nil && resp.StatusCode >= 400 {
		return "", chatDecodeError()
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", makeChatStatusError(resp.StatusCode, bodyBytes)
	}

	var payload openai.ChatCompletionResponse
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return "", chatDecodeError()
	}

	if len(payload.Choices) == 0 || payload.Choices[0].Message == nil {
		return "", chatDecodeError()
	}

	return payload.Choices[0].Message.Content, nil
}

// Stream, Freebuff upstream sohbet uç noktasından Codebuff CLI metadata'lı SSE delta akışı okur.
//
// Hata durumunda (session_expired, waiting_room_queued vb.) otomatik oturum
// kurtarma dener. Ported from codebuff-proxy's retryWithFreshFreebuffSession().
//
// ## Kullanım örneği
//
// ```go
//
//	session := freebuff.Session{InstanceID: "freebuff-proxy"}
//	deltas, errs := client.Stream(ctx, token, session, openai.ChatCompletionRequest{
//		Model:    "deepseek/deepseek-v4-pro",
//		Messages: []openai.ChatMessage{{Role: "user", Content: "Merhaba"}},
//	})
//
//	for delta := range deltas {
//		fmt.Print(delta)
//	}
//
//	if err := <-errs; err != nil {
//		return err
//	}
//
// ```
func (c *Client) Stream(ctx context.Context, token string, activeSession Session, req openai.ChatCompletionRequest) (<-chan string, <-chan error) {
	upstreamChatReq := normalizeChatCompletionRequest(req)
	upstreamChatReq.Model = modelForActiveSession(activeSession, upstreamChatReq.Model)
	runID, err := c.startAgentRun(ctx, token, upstreamChatReq.Model)
	if err != nil {
		return failedChatStream(err)
	}

	upstreamReq, err := buildUpstreamChatRequest(upstreamChatReq, true, runID, activeSession)
	if err != nil {
		return failedChatStream(err)
	}

	resp, err := c.doChatRequest(ctx, token, upstreamReq, "text/event-stream")
	if err != nil {
		return failedChatStream(err)
	}

	// ── Session Recovery for Stream ─────────────────────────────────────────
	// Only read body on error paths; success paths pass SSE body through.
	if resp.StatusCode >= 400 {
		bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()

		if readErr != nil {
			return failedChatStream(chatDecodeError())
		}

		if ShouldRecoverSessionFromResponse(resp.StatusCode, bodyBytes) {
			cfg := DefaultRecoverSessionConfig()
			cfg.InstanceID = activeSession.InstanceID
			newSession, retryOK := c.RetryWithFreshSession(ctx, token, upstreamChatReq.Model, cfg)
			if retryOK {
				return c.doStream(ctx, token, newSession, upstreamChatReq)
			}
			return failedChatStream(makeChatStatusError(resp.StatusCode, bodyBytes))
		}
		return failedChatStream(makeChatStatusError(resp.StatusCode, bodyBytes))
	}

	// ── Success Path — pass body through for SSE streaming ───────────────────
	return c.streamFromBody(resp)
}

// streamFromBody creates delta/errs channels from the SSE response body.
func (c *Client) streamFromBody(resp *http.Response) (<-chan string, <-chan error) {
	deltas := make(chan string)
	errs := make(chan error, 1)

	go func() {
		defer close(deltas)
		defer close(errs)
		defer resp.Body.Close()

		if err := scanChatStream(context.Background(), resp.Body, deltas); err != nil {
			sendChatError(context.Background(), errs, err)
		}
	}()

	return deltas, errs
}

// doStream sends a streaming chat request and returns delta/errs channels.
// Shared between initial call and session recovery retry.
func (c *Client) doStream(ctx context.Context, token string, session Session, req openai.ChatCompletionRequest) (<-chan string, <-chan error) {
	runID, err := c.startAgentRun(ctx, token, req.Model)
	if err != nil {
		return failedChatStream(err)
	}

	upstreamReq, err := buildUpstreamChatRequest(req, true, runID, session)
	if err != nil {
		return failedChatStream(err)
	}

	resp, err := c.doChatRequest(ctx, token, upstreamReq, "text/event-stream")
	if err != nil {
		return failedChatStream(err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer resp.Body.Close()
		return failedChatStream(chatStatusError(resp, chatErrorStageChat))
	}

	deltas := make(chan string)
	errs := make(chan error, 1)

	go func() {
		defer close(deltas)
		defer close(errs)
		defer resp.Body.Close()

		if err := scanChatStream(ctx, resp.Body, deltas); err != nil {
			sendChatError(ctx, errs, err)
		}
	}()

	return deltas, errs
}

type startAgentRunRequest struct {
	Action         string   `json:"action"`
	AgentID        string   `json:"agentId"`
	AncestorRunIDs []string `json:"ancestorRunIds"`
}

type startAgentRunResponse struct {
	RunID string `json:"runId"`
}

type upstreamChatRequest struct {
	Model            string               `json:"model"`
	Messages         []openai.ChatMessage `json:"messages"`
	Stream           bool                 `json:"stream,omitempty"`
	Temperature      *float64             `json:"temperature,omitempty"`
	MaxTokens        *int                 `json:"max_tokens,omitempty"`
	Tools            []json.RawMessage    `json:"tools,omitempty"`
	ToolChoice       any                  `json:"tool_choice,omitempty"`
	CodebuffMetadata codebuffMetadata     `json:"codebuff_metadata"`
}

type codebuffMetadata struct {
	RunID              string `json:"run_id"`
	ClientID           string `json:"client_id"`
	CostMode           string `json:"cost_mode"`
	FreebuffInstanceID string `json:"freebuff_instance_id,omitempty"`
}

func (c *Client) startAgentRun(ctx context.Context, token string, model string) (string, error) {
	// ── Run ID Cache Lookup ─────────────────────────────────────────────────
	// Ported from codebuff-proxy: cache the run_id by hashed API key + agent ID
	// to avoid redundant API calls on every chat request.
	if c.RunCache != nil {
		agentID := agentIDForModel(model)
		hashedKey := c.RunCache.HashFNV1a(token)
		cacheKey := cache.BuildAgentRunCacheKey(hashedKey, agentID, "freebuff-proxy")
		if cached, ok := c.RunCache.Get(cacheKey); ok {
			return cached, nil
		}
	}

	// ── API Call ────────────────────────────────────────────────────────────
	agentID := agentIDForModel(model)
	resp, err := c.doJSONRequest(ctx, token, agentRunsEndpointPath, startAgentRunRequest{
		Action:         "START",
		AgentID:        agentID,
		AncestorRunIDs: []string{},
	}, "application/json")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", chatStatusError(resp, chatErrorStageAgentRun)
	}

	var payload startAgentRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", chatDecodeError()
	}
	if strings.TrimSpace(payload.RunID) == "" {
		return "", chatDecodeError()
	}

	// ── Cache the run_id ────────────────────────────────────────────────────
	if c.RunCache != nil {
		hashedKey := c.RunCache.HashFNV1a(token)
		cacheKey := cache.BuildAgentRunCacheKey(hashedKey, agentID, "freebuff-proxy")
		c.RunCache.Set(cacheKey, payload.RunID)
	}

	return payload.RunID, nil
}

func normalizeChatCompletionRequest(req openai.ChatCompletionRequest) openai.ChatCompletionRequest {
	req.Model = CanonicalModelName(req.Model)
	return req
}

func modelForActiveSession(session Session, fallback string) string {
	for _, model := range []string{session.Model, session.CurrentModel} {
		if strings.TrimSpace(model) != "" {
			return CanonicalModelName(model)
		}
	}

	return fallback
}

// CanonicalModelName, Freebuff model alias'ını upstream'in beklediği kanonik model adına çevirir.
//
// ## Kullanım örneği
//
// ```go
// model := freebuff.CanonicalModelName("deepseek-v4-pro")
// fmt.Println(model) // deepseek/deepseek-v4-pro
// ```
func CanonicalModelName(model string) string {
	if canonicalModel, ok := canonicalFreebuffModelsByAlias[model]; ok {
		return canonicalModel
	}

	return model
}

func buildUpstreamChatRequest(req openai.ChatCompletionRequest, stream bool, runID string, activeSession Session) (upstreamChatRequest, error) {
	clientID := clientIDForRun(runID)

	// Free-mode gate: the first system message must OPEN with the CLI's
	// canonical identity marker (position 0). Prepend only when no system
	// message already opens with it — never clobber a canonical prompt.
	alreadyMarked := false
	for _, m := range req.Messages {
		if m.Role != "system" {
			continue
		}
		if strings.HasPrefix(strings.TrimLeft(m.Content, " \t\n\r"), cliSystemMarker) {
			alreadyMarked = true
			break
		}
	}
	if !alreadyMarked {
		msgs := make([]openai.ChatMessage, 0, len(req.Messages)+1)
		msgs = append(msgs, openai.ChatMessage{Role: "system", Content: cliSystemMarker})
		msgs = append(msgs, req.Messages...)
		req.Messages = msgs
	}

	return upstreamChatRequest{
		Model:       req.Model,
		Messages:    req.Messages,
		Stream:      stream,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Tools:       req.Tools,
		ToolChoice:  req.ToolChoice,
		CodebuffMetadata: codebuffMetadata{
			RunID:              runID,
			ClientID:           clientID,
			CostMode:           freebuffCostMode,
			FreebuffInstanceID: activeSession.InstanceID,
		},
	}, nil
}

// clientIDForRun derives the per-run client session id deterministically from
// the run_id, in the SDK-faithful 13-char base36 shape (the CLI's
// Math.random().toString(36).substring(2,15) equivalent). Deriving — not
// drawing fresh per call — is what keeps the CLI invariant: one run_id always
// pairs with ONE client_id, because startAgentRun caches run_id per
// token+agent and a fresh draw per call would fan one run out across N ids,
// which upstream refuses as free_mode_run_fanout. Other shapes —
// "freebuff-proxy-<hex>", sess:/run: prefixes, bare hex — are what upstream
// fingerprints as a proxy (#103) and rejects.
func clientIDForRun(runID string) string {
	sum := sha256.Sum256([]byte(runID))
	n := new(big.Int).SetBytes(sum[:])
	mod := new(big.Int).Exp(big.NewInt(36), big.NewInt(13), nil)
	id := n.Mod(n, mod).Text(36)
	for len(id) < 13 {
		id = "0" + id
	}
	return id
}

func agentIDForModel(model string) string {
	// Live registry first (refreshed from upstream's TS constants every 6h);
	// static snapshot as offline fallback; defaultFreeAgentID last.
	if agentID, ok := liveAgentRegistry.get(CanonicalModelName(model)); ok {
		return agentID
	}
	if agentID, ok := freebuffAgentIDsByModel[CanonicalModelName(model)]; ok {
		return agentID
	}

	return defaultFreeAgentID
}

func (c *Client) doChatRequest(ctx context.Context, token string, req upstreamChatRequest, accept string) (*http.Response, error) {
	return c.doJSONRequest(ctx, token, chatEndpointPath, req, accept)
}

func (c *Client) doJSONRequest(ctx context.Context, token string, path string, payload any, accept string) (*http.Response, error) {
	if strings.TrimSpace(token) == "" {
		return nil, &APIError{
			StatusCode: http.StatusUnauthorized,
			Code:       "freebuff_auth_missing",
			Message:    "Freebuff kimlik bilgisi bulunamadı",
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, chatEncodeError()
	}

	requestURL := c.baseURL.ResolveReference(&url.URL{Path: path})

	// Transport-level retry: transient dial/TLS/reset blips (dead proxy pick,
	// handshake storm leftovers) are retried once with a short backoff instead
	// of surfacing as user-visible timeouts. Never retried: 4xx/5xx responses
	// (only transport errors), caller cancellation, or caller deadlines.
	var lastErr error
	for attempt := 0; attempt < maxTransportAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(transportRetryDelay):
			case <-ctx.Done():
				return nil, &APIError{
					Code:    "upstream_chat_unavailable",
					Message: "Freebuff sohbet upstream isteği iptal edildi",
				}
			}
		}

		// Fresh body reader per attempt: bytes.Reader is consumed by the first send.
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL.String(), bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("build freebuff chat request: %w", err)
		}

		httpReq.Header.Set(headerAuthorization, "Bearer "+token)
		httpReq.Header.Set("Content-Type", "application/json")
		if path == chatEndpointPath {
			// The CLI pins the ai-sdk UA on chat calls alone; every other
			// endpoint keeps Go's default (or the caller's) UA.
			httpReq.Header.Set("User-Agent", cliUserAgent)
		}
		if accept != "" {
			httpReq.Header.Set("Accept", accept)
		}

		resp, err := c.httpClient.Do(httpReq)
		if err == nil {
			return resp, nil
		}

		lastErr = err
		if !isTransportError(err) || ctx.Err() != nil {
			break
		}
	}

	_ = lastErr
	return nil, &APIError{
		Code:    "upstream_chat_unavailable",
		Message: "Freebuff sohbet upstream isteği başarısız oldu",
	}
}

// isTransportError reports whether err is a low-level transport failure worth
// retrying (dial failures, TLS handshake errors, connection resets, EOF).
// HTTP status errors never reach this path — only client.Do failures do.
func isTransportError(err error) bool {
	if err == nil {
		return false
	}
	// Caller cancellation/deadline is never retryable.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE)
}

func failedChatStream(err error) (<-chan string, <-chan error) {
	deltas := make(chan string)
	close(deltas)

	errs := make(chan error, 1)
	if err != nil {
		errs <- err
	}
	close(errs)

	return deltas, errs
}

func scanChatStream(ctx context.Context, body io.Reader, deltas chan<- string) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var dataLines []string
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			finished, err := handleChatStreamEvent(ctx, dataLines, deltas)
			dataLines = nil
			if err != nil || finished {
				return err
			}
			continue
		}

		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}

	if err := scanner.Err(); err != nil {
		return &APIError{Code: "upstream_chat_error", Message: "Freebuff sohbet akışı okunamadı"}
	}

	_, err := handleChatStreamEvent(ctx, dataLines, deltas)
	return err
}

func handleChatStreamEvent(ctx context.Context, dataLines []string, deltas chan<- string) (bool, error) {
	if len(dataLines) == 0 {
		return false, nil
	}

	payload := strings.Join(dataLines, "\n")
	if payload == "[DONE]" {
		return true, nil
	}

	var errorEvent struct {
		Error *openai.APIErrorObject `json:"error"`
	}
	if err := json.Unmarshal([]byte(payload), &errorEvent); err != nil {
		return false, chatDecodeError()
	}
	if errorEvent.Error != nil {
		return false, chatStreamEventError(errorEvent.Error.Code)
	}

	var chunk openai.ChatCompletionChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return false, chatDecodeError()
	}

	if len(chunk.Choices) == 0 || chunk.Choices[0].Delta == nil || chunk.Choices[0].Delta.Content == "" {
		return false, nil
	}

	select {
	case deltas <- chunk.Choices[0].Delta.Content:
	case <-ctx.Done():
		return true, &APIError{Code: "upstream_chat_unavailable", Message: "Freebuff sohbet akışı iptal edildi"}
	}

	return false, nil
}

func sendChatError(ctx context.Context, errs chan<- error, err error) {
	if err == nil {
		return
	}

	select {
	case errs <- err:
	case <-ctx.Done():
	}
}

func chatStatusError(resp *http.Response, stage string) *APIError {
	statusCode := resp.StatusCode
	if code, ok := safeUpstreamErrorCode(resp.Body); ok {
		return &APIError{StatusCode: statusCode, Code: code, Message: safeUpstreamErrorMessages[code]}
	}

	apiErr := &APIError{StatusCode: statusCode}
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		apiErr.Code = "freebuff_auth_failed"
		apiErr.Message = "Freebuff sohbet yetkilendirmesi başarısız oldu"
	case http.StatusTooManyRequests:
		apiErr.Code = "freebuff_rate_limited"
		apiErr.Message = "Freebuff sohbet limiti aşıldı"
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		apiErr.Code = "upstream_chat_unavailable"
		apiErr.Message = "Freebuff sohbet upstream geçici olarak kullanılamıyor"
	default:
		if stage == chatErrorStageAgentRun {
			apiErr.Code = "upstream_agent_run_error"
			apiErr.Message = fmt.Sprintf("Freebuff agent run upstream hatası: status %d", statusCode)
		} else {
			apiErr.Code = "upstream_chat_error"
			apiErr.Message = fmt.Sprintf("Freebuff sohbet upstream hatası: status %d", statusCode)
		}
	}

	return apiErr
}

var safeUpstreamErrorMessages = map[string]string{
	"freebuff_update_required":       "Freebuff oturum bilgisi eksik veya eski",
	"session_expired":                "Freebuff oturumu süresi doldu",
	"session_model_mismatch":         "Freebuff oturum modeli istek modeliyle eşleşmiyor",
	"session_superseded":             "Freebuff oturumu başka bir oturum tarafından değiştirildi",
	"waiting_room_queued":            "Freebuff oturumu hâlâ kuyrukta",
	"waiting_room_required":          "Freebuff bekleme odası oturumu gerekli",
	"free_mode_invalid_agent_model": "Free mode yalnızca belirli agent ve model kombinasyonlarında kullanılabilir",
}

func safeUpstreamErrorCode(body io.Reader) (string, bool) {
	if body == nil {
		return "", false
	}

	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(body, 8*1024)).Decode(&payload); err != nil {
		return "", false
	}

	for _, code := range candidateUpstreamErrorCodes(payload) {
		if _, ok := safeUpstreamErrorMessages[code]; ok {
			return code, true
		}
	}

	return "", false
}

func safeUpstreamErrorCodeFromBytes(body []byte) (string, bool) {
	if len(body) == 0 {
		return "", false
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", false
	}

	for _, code := range candidateUpstreamErrorCodes(payload) {
		if _, ok := safeUpstreamErrorMessages[code]; ok {
			return code, true
		}
	}

	return "", false
}

func candidateUpstreamErrorCodes(payload map[string]any) []string {
	var codes []string
	if code, ok := payload["code"].(string); ok {
		codes = append(codes, code)
	}
	if code, ok := payload["error"].(string); ok {
		codes = append(codes, code)
	}
	if errorObject, ok := payload["error"].(map[string]any); ok {
		if code, ok := errorObject["code"].(string); ok {
			codes = append(codes, code)
		}
	}

	return codes
}

// makeChatStatusError creates an APIError from a status code and body bytes.
// Similar to chatStatusError but works with already-consumed response body.
func makeChatStatusError(statusCode int, body []byte) *APIError {
	if code, ok := safeUpstreamErrorCodeFromBytes(body); ok {
		return &APIError{StatusCode: statusCode, Code: code, Message: safeUpstreamErrorMessages[code]}
	}

	apiErr := &APIError{StatusCode: statusCode}
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		apiErr.Code = "freebuff_auth_failed"
		apiErr.Message = "Freebuff sohbet yetkilendirmesi başarısız oldu"
	case http.StatusTooManyRequests:
		apiErr.Code = "freebuff_rate_limited"
		apiErr.Message = "Freebuff sohbet limiti aşıldı"
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		apiErr.Code = "upstream_chat_unavailable"
		apiErr.Message = "Freebuff sohbet upstream geçici olarak kullanılamıyor"
	default:
		apiErr.Code = "upstream_chat_error"
		apiErr.Message = fmt.Sprintf("Freebuff sohbet upstream hatası: status %d", statusCode)
	}

	return apiErr
}

func chatStreamEventError(code string) *APIError {
	switch code {
	case "freebuff_auth_failed":
		return &APIError{StatusCode: http.StatusUnauthorized, Code: "freebuff_auth_failed", Message: "Freebuff sohbet yetkilendirmesi başarısız oldu"}
	case "free_mode_invalid_agent_model":
		return &APIError{StatusCode: http.StatusForbidden, Code: "free_mode_invalid_agent_model", Message: "Free mode yalnızca belirli agent ve model kombinasyonlarında kullanılabilir"}
	case "freebuff_rate_limited":
		return &APIError{StatusCode: http.StatusTooManyRequests, Code: "freebuff_rate_limited", Message: "Freebuff sohbet limiti aşıldı"}
	case "upstream_chat_unavailable":
		return &APIError{StatusCode: http.StatusServiceUnavailable, Code: "upstream_chat_unavailable", Message: "Freebuff sohbet upstream geçici olarak kullanılamıyor"}
	default:
		return &APIError{StatusCode: http.StatusBadGateway, Code: "upstream_chat_error", Message: "Freebuff sohbet akışı hata döndürdü"}
	}
}

func chatEncodeError() *APIError {
	return &APIError{
		Code:    "upstream_chat_error",
		Message: "Freebuff sohbet isteği hazırlanamadı",
	}
}

func chatDecodeError() *APIError {
	return &APIError{
		Code:    "upstream_chat_error",
		Message: "Freebuff sohbet yanıtı çözülemedi",
	}
}
