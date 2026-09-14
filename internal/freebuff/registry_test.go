package freebuff

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeUpstreamFreeAgents is a miniature free-agents.ts: the root map with
// computed keys referencing constants defined across the other files.
const fakeUpstreamFreeAgents = `import type { CostMode } from './model-config'

export const FREE_COST_MODE = 'free' as const

export const FREEBUFF_ROOT_AGENT_ID_BY_MODEL: Record<string, string> = {
  [FREEBUFF_MIMO_V25_MODEL_ID]: 'base2-free-mimo',
  [FREEBUFF_GLM_V53_FLASH_MODEL_ID]: 'base2-free-glm-5-3-flash',
  [FREEBUFF_GEMINI_38_FLASH_MODEL_ID]: 'base2-free-gemini-3-8-flash',
  [FREEBUFF_UNRESOLVED_MODEL_ID]: 'base2-free-unresolved',
  'stray/inline-model': 'base2-free-inline',
}

export function rootAgentForModel(model: string): string {
  return FREEBUFF_ROOT_AGENT_ID_BY_MODEL[model] ?? 'base2-free'
}
`

// fakeUpstreamModels is a miniature freebuff-models.ts: one direct literal,
// one object-property alias, one deliberately unresolved constant.
const fakeUpstreamModels = `export const FREEBUFF_GLM_V53_FLASH_MODEL_ID = 'z-ai/glm-5.3-flash'
export const FREEBUFF_MIMO_V25_MODEL_ID = mimoModels.mimoV25
export const FREEBUFF_GEMINI_38_FLASH_MODEL_ID = GEMINI_3_8_FLASH_LITE_MODEL_ID
export const FREEBUFF_UNRESOLVED_MODEL_ID = missingModels.neverDefined
`

// fakeUpstreamGemini is a miniature gemini.ts.
const fakeUpstreamGemini = `export const GEMINI_3_8_FLASH_LITE_MODEL_ID = 'google/gemini-3.8-flash-lite'
`

// fakeUpstreamModelConfig is a miniature model-config.ts with the object map.
const fakeUpstreamModelConfig = `export const mimoModels = {
  mimoV25: 'mimo/mimo-v2.5',
  mimoV25Pro: 'mimo/mimo-v2.5-pro',
} as const
`

func registryTestFiles() map[string]string {
	return map[string]string{
		"free-agents.ts":     fakeUpstreamFreeAgents,
		"freebuff-models.ts": fakeUpstreamModels,
		"gemini.ts":          fakeUpstreamGemini,
		"model-config.ts":    fakeUpstreamModelConfig,
	}
}

func newRegistryTestServer(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for name, body := range files {
		mux.HandleFunc("/CodebuffAI/freebuff/main/common/src/constants/"+name, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte(body))
		})
	}
	return httptest.NewServer(mux)
}

func TestParseRootAgentMap(t *testing.T) {
	joined := strings.Join([]string{
		fakeUpstreamFreeAgents,
		fakeUpstreamModels,
		fakeUpstreamGemini,
		fakeUpstreamModelConfig,
	}, "\n")

	got := parseRootAgentMap(joined)
	if got == nil {
		t.Fatal("parseRootAgentMap returned nil for well-formed sources")
	}

	want := map[string]string{
		// object-property alias chain (FREEBUFF_MIMO_V25_MODEL_ID → mimoModels.mimoV25)
		"mimo/mimo-v2.5": "base2-free-mimo",
		// direct literal
		"z-ai/glm-5.3-flash": "base2-free-glm-5-3-flash",
		// cross-file literal
		"google/gemini-3.8-flash-lite": "base2-free-gemini-3-8-flash",
	}
	// Plain-string keys and unresolved constants are not extracted by design:
	// reRootEntry only matches computed [CONST] keys, so 'stray/inline-model'
	// is skipped (it would fall back to defaultFreeAgentID at runtime).
	for model, agent := range want {
		if got[model] != agent {
			t.Errorf("pairing for %q: got %q, want %q", model, got[model], agent)
		}
	}
	// Unresolved constant keys must be skipped, not invented.
	if agent, ok := got["base2-free-unresolved"]; ok {
		t.Errorf("unresolved constant should be skipped, got agent %q", agent)
	}
	if agent, ok := got["stray/inline-model"]; ok {
		t.Errorf("plain-string key should be skipped (computed-key regex), got agent %q", agent)
	}
}

func TestParseRootAgentMap_MissingBlock(t *testing.T) {
	if got := parseRootAgentMap("export const SOMETHING = 'x'"); got != nil {
		t.Errorf("expected nil when root block absent, got %v", got)
	}
}

func TestRefreshAgentRegistry_LiveFetch(t *testing.T) {
	srv := newRegistryTestServer(t, registryTestFiles())
	defer srv.Close()

	// Point both sources at the test server; restore globals after.
	oldRaw, oldCDN := registryRawBase, registryJsDelivrBase
	registryRawBase = srv.URL + "/CodebuffAI/freebuff/main/common/src/constants/"
	registryJsDelivrBase = srv.URL + "/unused-mirror/"
	defer func() { registryRawBase, registryJsDelivrBase = oldRaw, oldCDN }()

	// Reset the shared registry so the test starts from empty.
	oldMap := liveAgentRegistry
	liveAgentRegistry = &agentRegistry{modelToAgent: map[string]string{}}
	defer func() { liveAgentRegistry = oldMap }()

	if err := refreshAgentRegistry(context.Background()); err != nil {
		t.Fatalf("refreshAgentRegistry: %v", err)
	}

	agent, ok := liveAgentRegistry.get("z-ai/glm-5.3-flash")
	if !ok || agent != "base2-free-glm-5-3-flash" {
		t.Errorf("glm pairing after refresh: got %q ok=%v", agent, ok)
	}
	agent, ok = liveAgentRegistry.get("mimo/mimo-v2.5")
	if !ok || agent != "base2-free-mimo" {
		t.Errorf("mimo pairing after refresh: got %q ok=%v", agent, ok)
	}
}

func TestRefreshAgentRegistry_KeepsPreviousStateOnFailure(t *testing.T) {
	// A server that always 500s — refresh must fail and leave state intact.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	oldRaw, oldCDN := registryRawBase, registryJsDelivrBase
	registryRawBase = srv.URL + "/constants/"
	registryJsDelivrBase = srv.URL + "/mirror/"
	defer func() { registryRawBase, registryJsDelivrBase = oldRaw, oldCDN }()

	oldMap := liveAgentRegistry
	liveAgentRegistry = &agentRegistry{modelToAgent: map[string]string{
		"z-ai/glm-5.3-flash": "base2-free-glm-5-3-flash",
	}}
	defer func() { liveAgentRegistry = oldMap }()

	if err := refreshAgentRegistry(context.Background()); err == nil {
		t.Fatal("expected refresh to fail against a 500ing server")
	}
	agent, ok := liveAgentRegistry.get("z-ai/glm-5.3-flash")
	if !ok || agent != "base2-free-glm-5-3-flash" {
		t.Errorf("previous state must survive a failed refresh, got %q ok=%v", agent, ok)
	}
}

func TestAgentIDForModel_FallbackChain(t *testing.T) {
	oldMap := liveAgentRegistry
	liveAgentRegistry = &agentRegistry{modelToAgent: map[string]string{
		"mimo/mimo-v2.5": "base2-free-mimo",
	}}
	defer func() { liveAgentRegistry = oldMap }()

	cases := []struct {
		model string
		want  string
	}{
		{"mimo/mimo-v2.5", "base2-free-mimo"},              // live registry wins
		{"z-ai/glm-5.3-flash", "base2-free-glm-5-3-flash"}, // static snapshot fallback
		{"totally/unknown", "base2-free"},                  // default last
	}
	for _, tc := range cases {
		if got := agentIDForModel(tc.model); got != tc.want {
			t.Errorf("agentIDForModel(%q) = %q, want %q", tc.model, got, tc.want)
		}
	}
}
