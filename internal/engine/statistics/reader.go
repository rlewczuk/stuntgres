package statistics

import (
	"stuntgres/internal/engine/ids"
	"time"
)

// Reader is transaction-owned and serialized by transaction admission. Returned
// facts are values; no caller can mutate a retained capture or published map.
type Reader struct {
	mode       string
	generation uint64
	cache      map[Key]factRead
	full       bool
	stamp      time.Time
	activity   map[ids.SessionToken]Activity
	defaults   []captureDefault
	shared     [SharedKindCount]SharedFact
	slru       [SLRUCount]SLRUFact
	kinds      uint8
	backends   map[ids.SessionToken]backendRead
}
type factRead struct {
	fact   Fact
	exists bool
}
type backendRead struct {
	fact   BackendFact
	exists bool
}

func (r *Reader) Configure(mode string, generation uint64) {
	if r.mode != mode || r.generation != generation {
		r.Clear()
		r.mode, r.generation = mode, generation
	}
}
func (r *Reader) Clear() {
	mode, generation := r.mode, r.generation
	*r = Reader{mode: mode, generation: generation}
}
func (r *Reader) Timestamp() time.Time { return r.stamp }
func (r *Reader) NeedsSnapshot() bool  { return r.mode == "snapshot" && !r.full }

// capture is the standalone single-provider fallback, under the provider mutex.
// Cluster-aware consumers first Capture their complete accessible domain.
func (r *Reader) capture(p *Provider, now time.Time) {
	r.captureLocked(p, true, Key{})
	r.full, r.stamp = true, now
}
func (r *Reader) missing(p *Provider, category Category) Fact {
	for _, d := range r.defaults {
		if d.provider == p {
			reset := d.fact.ResetAt
			if category == IO {
				reset = d.ioReset
			}
			if category == WAL {
				reset = d.walReset
			}
			return missingFact(category, reset, d.fact.Generation)
		}
	}
	return Fact{}
}
func (r *Reader) Read(p *Provider, k Key, now time.Time) Fact {
	f, _ := r.ReadEntry(p, k, now)
	return f
}

// ReadEntry retains negative entries as well as counters. Membership must never
// come from a newer provider cut than the corresponding cached fact.
func (r *Reader) ReadEntry(p *Provider, k Key, now time.Time) (Fact, bool) {
	if r.mode == "none" {
		return p.ReadEntry(k)
	}
	if f, ok := r.cache[k]; ok {
		return f.fact, f.exists
	}
	if r.full {
		return r.missing(p, k.Category), false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.mode == "snapshot" {
		r.capture(p, now)
		if f, ok := r.cache[k]; ok {
			return f.fact, f.exists
		}
		return r.missing(p, k.Category), false
	}
	if r.cache == nil {
		r.cache = make(map[Key]factRead)
	}
	f, exists := p.readEntry(k)
	r.cache[k] = factRead{f, exists}
	return f, exists
}
func (r *Reader) ReadShared(p *Provider, k SharedKind, now time.Time) SharedFact {
	if k >= SharedKindCount {
		return SharedFact{}
	}
	if k == RecoveryPrefetch || r.mode == "none" {
		return p.ReadShared(k)
	}
	if r.kinds&(1<<k) != 0 {
		return r.shared[k]
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.mode == "snapshot" {
		r.capture(p, now)
	} else {
		r.shared[k] = p.shared[k]
		if k == SLRU {
			r.slru = p.slru
		}
		r.kinds |= 1 << k
	}
	return r.shared[k]
}
func (r *Reader) ReadSLRU(p *Provider, k SLRUKind, now time.Time) SLRUFact {
	if k >= SLRUCount {
		return SLRUFact{}
	}
	if r.mode == "none" {
		return p.ReadSLRU(k)
	}
	r.ReadShared(p, SLRU, now)
	return r.slru[k]
}

// ReadSLRUs admits the entire family to the existing cumulative reader.
func (r *Reader) ReadSLRUs(p *Provider, now time.Time) [SLRUCount]SLRUFact {
	if r.mode == "none" {
		return p.ReadSLRUs()
	}
	r.ReadShared(p, SLRU, now)
	return r.slru
}
func (r *Reader) ReadBackend(p *Provider, token ids.SessionToken, now time.Time) (BackendFact, bool) {
	if r.mode == "none" {
		return p.ReadBackend(token)
	}
	if f, ok := r.backends[token]; ok {
		return f.fact, f.exists
	}
	if r.full {
		return BackendFact{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.mode == "snapshot" {
		r.capture(p, now)
		f := r.backends[token]
		return f.fact, f.exists
	}
	if r.backends == nil {
		r.backends = make(map[ids.SessionToken]backendRead)
	}
	f, ok := p.backends[token]
	r.backends[token] = backendRead{f, ok}
	return f, ok
}

// CaptureSource describes the accessible capture domain: all current-database
// objects, and the database category alone from every other database.
type CaptureSource struct {
	Provider *Provider
	Database Key
	Shared   Key // Optional shared-database fact in this same provider lock.
	All      bool
}
type captureDefault struct {
	provider          *Provider
	fact              Fact
	ioReset, walReset time.Time
}

func (r *Reader) captureLocked(p *Provider, all bool, database Key) {
	if r.cache == nil {
		r.cache = make(map[Key]factRead)
	}
	r.defaults = append(r.defaults, captureDefault{provider: p, fact: Fact{ResetAt: p.reset, Generation: p.generation}, ioReset: p.shared[SharedIO].ResetAt, walReset: p.shared[SharedWAL].ResetAt})
	if all {
		for key := range p.facts {
			r.cache[key] = factRead{p.read(key), true}
		}
		r.shared, r.slru = p.shared, p.slru
		r.kinds = (1 << SharedKindCount) - 1
		r.backends = make(map[ids.SessionToken]backendRead, len(p.backends))
		for token, f := range p.backends {
			r.backends[token] = backendRead{f, true}
		}
	} else {
		f, exists := p.readEntry(database)
		r.cache[database] = factRead{f, exists}
	}
}

// Capture is called under the cluster membership authority. Provider locks are
// leaf-only and acquired in the caller's stable database order.
func (r *Reader) Capture(sources []CaptureSource, now time.Time) {
	if !r.NeedsSnapshot() {
		return
	}
	for _, source := range sources {
		p := source.Provider
		p.mu.Lock()
		r.captureLocked(p, source.All, source.Database)
		if !source.All && source.Shared != (Key{}) {
			f, exists := p.readEntry(source.Shared)
			r.cache[source.Shared] = factRead{f, exists}
		}
		p.mu.Unlock()
	}
	r.full, r.stamp = true, now
}
func (r *Reader) Activities(p *Provider) map[ids.SessionToken]Activity {
	if r.activity == nil {
		r.activity = p.CaptureActivities()
	}
	result := make(map[ids.SessionToken]Activity, len(r.activity))
	for k, a := range r.activity {
		result[k] = a
	}
	return result
}

// ActivityCaptured is independent of cumulative mode and timestamp.
func (r *Reader) ActivityCaptured() bool { return r.activity != nil }

// InstallActivities takes ownership of a complete unmasked, enriched cut.
func (r *Reader) InstallActivities(activities map[ids.SessionToken]Activity) {
	if r.activity == nil {
		r.activity = activities
	}
}
