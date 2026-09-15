package pool

import (
	"time"

	"freebuff-proxy/backend/internal/upstream"
)

// modelUnfitTTL is how long a (egress, model) pair stays marked unfit after
// an upstream limited_ip refusal. A var (not const) so tests can shrink it.
var modelUnfitTTL = 5 * time.Minute

// defaultEgress is the fallback egress identity when the config does not
// name one: the direct connection (SOCKS5/HTTP proxy support was removed —
// the pool has exactly one egress). UNFIT_EGRESS overrides this so a proxy
// re-introduction can key per egress without changing the registry shape.
const defaultEgress = "direct"

// egress returns the pool's configured egress identity (UNFIT_EGRESS;
// "direct" when unset). Loaded per call so a dashboard config swap is
// picked up without a restart — consistent with every other cfg reader.
func (p *Pool) egress() string {
	if v := p.cfg.Load().UnfitEgress; v != "" {
		return v
	}
	return defaultEgress
}

// unfitKey is one (egress, model) pair in the model-unfit registry.
type unfitKey struct {
	egress string
	model  string
}

// unfitEntry is one registry entry: when the (egress, model) pair is unfit
// until, when it was marked (created), and the refusal that marked it (for
// surfacing to the client).
type unfitEntry struct {
	created time.Time
	until   time.Time
	err     *upstream.LimitedIpError
}

// MarkModelUnfit records that the pool's egress cannot serve model for the
// next modelUnfitTTL (upstream limited_ip refusal: the session row is fine,
// but this egress IP cannot serve this model). nil lie is tolerated; when
// non-nil a COPY is stored (the registry never aliases the caller's error
// object — SEC-3) with its Model set to the marked model so the surfaced
// error is self-describing.
func (p *Pool) MarkModelUnfit(model string, lie *upstream.LimitedIpError) {
	var stored *upstream.LimitedIpError
	if lie != nil {
		cp := *lie
		cp.Model = model
		stored = &cp
	}
	eg := p.egress()
	now := time.Now()
	p.unfitMu.Lock()
	defer p.unfitMu.Unlock()
	p.unfit[unfitKey{egress: eg, model: model}] = unfitEntry{created: now, until: now.Add(modelUnfitTTL), err: stored}
	p.logger.Debug("pool: model marked unfit on egress", "egress", eg, "model", model, "until", now.Add(modelUnfitTTL).Format(time.RFC3339))
}

// ClearModelUnfit removes the unfit mark for model unconditionally.
func (p *Pool) ClearModelUnfit(model string) {
	eg := p.egress()
	p.unfitMu.Lock()
	defer p.unfitMu.Unlock()
	delete(p.unfit, unfitKey{egress: eg, model: model})
	p.logger.Debug("pool: model unfit mark cleared", "egress", eg, "model", model)
}

// ClearModelUnfitBefore removes the unfit mark for model only when it was
// created no later than the given instant. A chat admitted before a later
// mark succeeding is NOT proof the mark is stale (the egress may have been
// limited right after its admission), so its success must not clear the
// mark; only a chat admitted after the mark (or in the same clock tick —
// program order guarantees the mark ran first) and still succeeding proves
// the pair is servable again. Younger marks are left for the 5-min TTL.
func (p *Pool) ClearModelUnfitBefore(model string, before time.Time) {
	eg := p.egress()
	p.unfitMu.Lock()
	defer p.unfitMu.Unlock()
	key := unfitKey{egress: eg, model: model}
	if e, ok := p.unfit[key]; ok && !e.created.After(before) {
		delete(p.unfit, key)
	}
}

// ModelUnfit reports whether model is currently marked unfit on the pool's
// egress. The zero time.Time means "not unfit"; a nil error means the entry
// was marked with no refusal detail. Expired entries are purged lazily. The
// error is returned as a COPY: callers must never mutate registry state
// (SEC-1) — the refusal window they surface is e.until, not the error's.
func (p *Pool) ModelUnfit(model string) (time.Time, *upstream.LimitedIpError) {
	key := unfitKey{egress: p.egress(), model: model}
	now := time.Now()
	p.unfitMu.Lock()
	defer p.unfitMu.Unlock()
	e, ok := p.unfit[key]
	if !ok {
		return time.Time{}, nil
	}
	if !now.Before(e.until) {
		delete(p.unfit, key)
		return time.Time{}, nil
	}
	if e.err == nil {
		return e.until, nil
	}
	cp := *e.err
	return e.until, &cp
}
