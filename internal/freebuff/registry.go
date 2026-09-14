package freebuff

// Live registry of upstream free-mode (model → agent) pairings.
//
// Minimal backport of trefeon/freebuff-proxy backend/internal/registry: the
// authoritative source is CodebuffAI/freebuff's TS constants on GitHub
// (FREEBUFF_ROOT_AGENT_ID_BY_MODEL in common/src/constants/free-agents.ts).
// We fetch the source files, resolve string-literal and object-property
// constants, and extract the model→agent map. raw.githubusercontent is
// mirrored through jsDelivr; both are tried per file.
//
// Failure semantics (mirrors trefeon): a failed refresh keeps the previous
// registry state; when nothing has ever loaded, agentIDForModel falls back
// to the hard-coded snapshot below and finally defaultFreeAgentID.

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Overridable in tests via httptest servers.
var (
	registryRawBase      = "https://raw.githubusercontent.com/CodebuffAI/freebuff/main/common/src/constants/"
	registryJsDelivrBase = "https://cdn.jsdelivr.net/gh/CodebuffAI/freebuff@main/common/src/constants/"
)

// registrySourceFiles are fetched in order and joined before parsing:
// free-agents.ts defines FREEBUFF_ROOT_AGENT_ID_BY_MODEL; model ids it
// references live in freebuff-models.ts, gemini.ts and model-config.ts.
var registrySourceFiles = []string{
	"free-agents.ts",
	"freebuff-models.ts",
	"gemini.ts",
	"model-config.ts",
}

const (
	registryFetchTimeout = 30 * time.Second
	registryMaxBytes     = 2 << 20 // 2 MiB per source; larger fails the fetch and keeps prior state
	registryRefreshEvery = 6 * time.Hour
)

// maxAliasDepth mirrors JS `if (depth > 8) return null` — alias chains
// longer than 8 hops do not resolve.
const maxAliasDepth = 8

// Regexes mirror trefeon's parse.go (itself a port of the reference JS):
// (?s) = JS `s` flag. The root map uses computed keys ([CONST]) whose values
// are plain agent-id strings.
var (
	reRootBlock = regexp.MustCompile(`(?s)export const FREEBUFF_ROOT_AGENT_ID_BY_MODEL[^=]*?=\s*\{([\s\S]*?)\n\}`)
	reRootEntry = regexp.MustCompile(`\[([A-Za-z_][A-Za-z0-9_]*)\]\s*:\s*'([^']+)'`)

	// export const NAME = 'literal' — line-anchored: a lazy DOTALL variant
	// would jump to the NEXT quoted literal in the file when a const's RHS is
	// an identifier (e.g. `= mimoModels.mimoV25`), poisoning the map. Real
	// sources keep every const on one line.
	reLiteral = regexp.MustCompile(`(?m)^export const\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*'((?:[^'\\]|\\.)*)'`)

	// export const NAME = obj.prop / export const NAME = OTHER_CONST —
	// identifier aliases resolved recursively (depth-capped like the JS).
	reAlias = regexp.MustCompile(`(?m)^export const\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\s*(?:as\s+const)?\s*$`)

	// export const NAME = { key: 'value', ... } — object-property constants
	// (e.g. mimoModels = { mimoV25: 'mimo/mimo-v2.5' }).
	reObject   = regexp.MustCompile(`(?s)export const\s+([A-Za-z_][A-Za-z0-9_]*)[^=]*?=\s*\{([\s\S]*?)\n\}`)
	reObjEntry = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*:\s*'([^']+)'`)
)

// agentRegistry is the concurrency-safe model→agent map behind agentIDForModel.
type agentRegistry struct {
	mu           sync.RWMutex
	modelToAgent map[string]string
	updatedAt    time.Time
}

var liveAgentRegistry = &agentRegistry{modelToAgent: map[string]string{}}

func (r *agentRegistry) get(model string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	agent, ok := r.modelToAgent[model]
	return agent, ok
}

func (r *agentRegistry) set(m map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.modelToAgent = m
	r.updatedAt = time.Now()
}

func (r *agentRegistry) size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.modelToAgent)
}

func (r *agentRegistry) age() time.Duration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.updatedAt.IsZero() {
		return 0
	}
	return time.Since(r.updatedAt)
}

// fetchRegistrySource reads one TS file, trying raw.githubusercontent first
// and falling back to the jsDelivr mirror (mirrors trefeon: raw is throttled
// or blocked in some regions).
func fetchRegistrySource(ctx context.Context, client *http.Client, file string) (string, error) {
	var lastErr error
	for _, base := range []string{registryRawBase, registryJsDelivrBase} {
		srcURL := base + file
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srcURL, nil)
		if err != nil {
			return "", fmt.Errorf("build registry request %s: %w", file, err)
		}
		text, err := func() (string, error) {
			resp, err := client.Do(req)
			if err != nil {
				return "", fmt.Errorf("fetch %s: %w", srcURL, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return "", fmt.Errorf("fetch %s: unexpected status %d", srcURL, resp.StatusCode)
			}
			body, err := io.ReadAll(io.LimitReader(resp.Body, registryMaxBytes+1))
			if err != nil {
				return "", fmt.Errorf("read %s: %w", srcURL, err)
			}
			if len(body) > registryMaxBytes {
				return "", fmt.Errorf("read %s: source exceeds %d bytes", srcURL, registryMaxBytes)
			}
			return string(body), nil
		}()
		if err == nil {
			return text, nil
		}
		lastErr = err
	}
	return "", lastErr
}

// fetchRegistrySources joins all source files into one text blob.
func fetchRegistrySources(ctx context.Context, client *http.Client) (string, error) {
	texts := make([]string, 0, len(registrySourceFiles))
	for _, file := range registrySourceFiles {
		text, err := fetchRegistrySource(ctx, client, file)
		if err != nil {
			return "", err
		}
		texts = append(texts, text)
	}
	return strings.Join(texts, "\n"), nil
}

// parseRootAgentMap extracts FREEBUFF_ROOT_AGENT_ID_BY_MODEL from the joined
// TS sources. Computed keys ([CONST]) resolve through string literals and
// object properties; entries whose key cannot be resolved are skipped (they
// simply fall back to defaultFreeAgentID, matching upstream's `?? 'base2-free'`).
func parseRootAgentMap(text string) map[string]string {
	block := reRootBlock.FindStringSubmatch(text)
	if block == nil {
		return nil
	}

	// Collect constants: direct string literals, object-property maps and
	// identifier aliases (name → obj.prop or another const name).
	literals := map[string]string{}
	for _, m := range reLiteral.FindAllStringSubmatch(text, -1) {
		literals[m[1]] = m[2]
	}
	aliases := map[string]string{}
	for _, m := range reAlias.FindAllStringSubmatch(text, -1) {
		aliases[m[1]] = m[2]
	}
	objects := map[string]map[string]string{}
	for _, m := range reObject.FindAllStringSubmatch(text, -1) {
		name := m[1]
		if objects[name] == nil {
			objects[name] = map[string]string{}
		}
		for _, e := range reObjEntry.FindAllStringSubmatch(m[2], -1) {
			objects[name][e[1]] = e[2]
		}
	}

	// resolve follows ident → literal | alias → obj.prop | alias → ident,
	// capped at maxAliasDepth hops exactly like the reference JS.
	var resolve func(ident string, depth int) (string, bool)
	resolve = func(ident string, depth int) (string, bool) {
		if depth > maxAliasDepth {
			return "", false
		}
		if v, ok := literals[ident]; ok {
			return v, true
		}
		target, ok := aliases[ident]
		if !ok {
			return "", false
		}
		if dot := strings.IndexByte(target, '.'); dot > 0 {
			objName, prop := target[:dot], target[dot+1:]
			if obj, ok := objects[objName]; ok {
				if v, ok := obj[prop]; ok {
					return v, true
				}
			}
			return "", false
		}
		return resolve(target, depth+1)
	}

	out := map[string]string{}
	for _, e := range reRootEntry.FindAllStringSubmatch(block[1], -1) {
		modelID, ok := resolve(e[1], 0)
		if !ok {
			continue
		}
		out[modelID] = e[2]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// refreshAgentRegistry fetches + parses + stores. On any failure it returns
// the error and leaves the previous registry state untouched.
func refreshAgentRegistry(ctx context.Context) error {
	client := &http.Client{Timeout: registryFetchTimeout}
	text, err := fetchRegistrySources(ctx, client)
	if err != nil {
		return err
	}
	parsed := parseRootAgentMap(text)
	if parsed == nil {
		return fmt.Errorf("FREEBUFF_ROOT_AGENT_ID_BY_MODEL not found in registry sources")
	}
	liveAgentRegistry.set(parsed)
	return nil
}

var registryStartOnce sync.Once

// StartAgentRegistry launches the background registry refresher: one fetch
// at startup, then every registryRefreshEvery. It is idempotent (guarded by
// sync.Once) and never blocks the caller; failures only log — the static
// fallback map keeps agent resolution working offline.
func StartAgentRegistry(ctx context.Context) {
	registryStartOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(registryRefreshEvery)
			defer ticker.Stop()

			if err := refreshAgentRegistry(ctx); err != nil {
				log.Printf("[agent-registry] initial refresh failed (using static fallback): %v", err)
			} else {
				log.Printf("[agent-registry] refreshed %d model→agent pairings", liveAgentRegistry.size())
			}

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := refreshAgentRegistry(ctx); err != nil {
						log.Printf("[agent-registry] refresh failed (keeping previous state, age %s): %v",
							liveAgentRegistry.age().Round(time.Second), err)
						continue
					}
					log.Printf("[agent-registry] refreshed %d model→agent pairings", liveAgentRegistry.size())
				}
			}
		}()
	})
}
