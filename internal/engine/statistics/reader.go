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
}

func (r *Reader) Configure(mode string, generation uint64) {
	if r.mode != mode || r.generation != generation {
		r.Clear()
		r.mode, r.generation = mode, generation
	}
}
func (r *Reader) Clear()               { r.cache = nil; r.full = false; r.stamp = time.Time{}; r.activity = nil }
func (r *Reader) Timestamp() time.Time { return r.stamp }
func (r *Reader) NeedsSnapshot() bool  { return r.mode == "snapshot" && !r.full }
func (r *Reader) Read(p *Provider, k Key, now time.Time) Fact {
	if r.mode == "none" {
		return p.Read(k)
	}
	if f, ok := r.cache[k]; ok {
		return f
	}
	if r.full {
		return Fact{ResetAt: r.reset, Generation: r.epoch}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.mode == "snapshot" {
		r.cache = make(map[Key]Fact, len(p.facts))
		for key := range p.facts {
			r.cache[key] = p.read(key)
		}
		r.reset, r.epoch = p.reset, p.generation
		r.full, r.stamp = true, now
		if f, ok := r.cache[k]; ok {
			return f
		}
		return Fact{ResetAt: r.reset, Generation: r.epoch}
	}
	if r.cache == nil {
		r.cache = make(map[Key]Fact)
	}
	f := p.read(k)
	r.cache[k] = f
	return f
}
func (r *Reader) Activities(p *Provider) map[ids.SessionToken]Activity {
	if r.activity == nil {
		p.mu.Lock()
		r.activity = make(map[ids.SessionToken]Activity, len(p.activities))
		for k, a := range p.activities {
			r.activity[k] = a
		}
		p.mu.Unlock()
	}
	result := make(map[ids.SessionToken]Activity, len(r.activity))
	for k, a := range r.activity {
		result[k] = a
	}
	return result
}
