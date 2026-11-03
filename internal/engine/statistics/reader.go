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
	cache      map[Key]Fact
	full       bool
	stamp      time.Time
	reset      time.Time
	epoch      uint64
	activity   map[ids.SessionToken]Activity
	shared     [SharedKindCount]SharedFact
	slru       [SLRUCount]SLRUFact
	kinds      uint8
	backends   map[ids.SessionToken]backendRead
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

// capture is the sole all-kind snapshot publication, under the provider mutex.
func (r *Reader) capture(p *Provider, now time.Time) {
	r.cache = make(map[Key]Fact, len(p.facts))
	for key := range p.facts {
		r.cache[key] = p.read(key)
	}
	r.shared, r.slru = p.shared, p.slru
	r.kinds = (1 << SharedKindCount) - 1
	r.backends = make(map[ids.SessionToken]backendRead, len(p.backends))
	for token, f := range p.backends {
		r.backends[token] = backendRead{f, true}
	}
	r.reset, r.epoch = p.reset, p.generation
	r.full, r.stamp = true, now
}
func (r *Reader) missing(category Category) Fact {
	reset := r.reset
	if category == IO {
		reset = r.shared[SharedIO].ResetAt
	}
	if category == WAL {
		reset = r.shared[SharedWAL].ResetAt
	}
	return missingFact(category, reset, r.epoch)
}
func (r *Reader) Read(p *Provider, k Key, now time.Time) Fact {
	if r.mode == "none" {
		return p.Read(k)
	}
	if f, ok := r.cache[k]; ok {
		return f
	}
	if r.full {
		return r.missing(k.Category)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.mode == "snapshot" {
		r.capture(p, now)
		if f, ok := r.cache[k]; ok {
			return f
		}
		return r.missing(k.Category)
	}
	if r.cache == nil {
		r.cache = make(map[Key]Fact)
	}
	f := p.read(k)
	r.cache[k] = f
	return f
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
