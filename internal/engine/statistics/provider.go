// Package statistics owns published cumulative facts, never physical storage.
package statistics

import (
	"stuntgres/internal/engine/ids"
	"sync"
	"time"
)

type Key struct {
	Object   ids.ObjectRef
	Category Category
}
type Category uint8

const (
	Relation Category = iota
	Function
	IO
	WAL
)

type Fact struct {
	Inserts, Updates, Deletes, Live, Dead, Modifications, InsertsSince int64
	VacuumCount, AnalyzeCount                                          int64
	LastVacuum, LastAnalyze                                            time.Time
	Truncated                                                          bool
	VacuumTime, AnalyzeTime                                            time.Duration
	Calls, Operations                                                  int64
	TotalTime, SelfTime                                                time.Duration
	ResetAt                                                            time.Time
	Generation                                                         uint64
}

// Provider's mutex is a leaf: it never calls engine, session, clock or settings.
// Zero value is ready for use. A provider belongs to one database incarnation.
type Provider struct {
	mu         sync.Mutex
	facts      map[Key]Fact
	activities map[ids.SessionToken]Activity
	reset      time.Time
	generation uint64
	retired    map[Key]struct{}
}

func (p *Provider) Read(k Key) Fact { p.mu.Lock(); defer p.mu.Unlock(); return p.read(k) }
func (p *Provider) read(k Key) Fact {
	f := p.facts[k]
	f.ResetAt, f.Generation = p.reset, p.generation
	return f
}
func (p *Provider) Reset(at time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.facts = nil
	p.reset = at
	p.generation++
}

// Replace takes ownership of a replacement baseline after all fallible work.
// The caller must never retain or mutate facts after transfer.
func (p *Provider) Replace(at time.Time, facts map[Key]Fact) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.facts, p.reset = facts, at
	p.retired = nil
	p.generation++
}
func merge(a, b Fact) Fact {
	if b.Truncated {
		a.Live, a.Dead, a.InsertsSince = 0, 0, 0
		a.Truncated = true
	}
	a.Inserts += b.Inserts
	a.Updates += b.Updates
	a.Deletes += b.Deletes
	a.Live += b.Live
	a.Dead += b.Dead
	a.Modifications += b.Modifications
	a.InsertsSince += b.InsertsSince
	a.Calls += b.Calls
	a.Operations += b.Operations
	a.TotalTime += b.TotalTime
	a.SelfTime += b.SelfTime
	return a
}

// Writer owns its pending map and throttle. Its owner serializes all methods.
// Clock values are supplied at boundaries so disabled timing does no clock work.
type Writer struct {
	pending     map[Key]Fact
	last        time.Time
	generation  uint64
	initialized bool
	chargedSelf time.Duration // Backend-wide completed function self time; owner-serialized.
}

func (w *Writer) Pending() bool { return len(w.pending) != 0 }
func (w *Writer) Add(p *Provider, k Key, f Fact) {
	p.mu.Lock()
	generation := p.generation
	_, retired := p.retired[k]
	p.mu.Unlock()
	if retired {
		delete(w.pending, k)
		return
	}
	if !w.initialized || generation != w.generation {
		w.pending = nil
		w.generation = generation
		w.initialized = true
	}
	if w.pending == nil {
		w.pending = make(map[Key]Fact)
	}
	previous := w.pending[k]
	next := merge(previous, f)
	// A truncate resets shared estimates at flush, but every already reportable
	// insert in this writer batch still contributes to inserts-since-vacuum.
	if f.Truncated {
		next.InsertsSince = previous.InsertsSince + f.InsertsSince
	}
	w.pending[k] = next
}
func (w *Writer) Flush(p *Provider, now time.Time, force bool) bool {
	if len(w.pending) == 0 || !force && !w.last.IsZero() && now.Sub(w.last) < time.Second {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if w.generation == p.generation {
		for k, f := range w.pending {
			if _, retired := p.retired[k]; retired {
				continue
			}
			if p.facts == nil {
				p.facts = make(map[Key]Fact, len(w.pending))
			}
			next := merge(p.facts[k], f)
			if next.Live < 0 {
				next.Live = 0
			}
			if next.Dead < 0 {
				next.Dead = 0
			}
			p.facts[k] = next
		}
	}
	w.pending = nil
	w.last = now
	return true
}

type Policy struct {
	Counts, Activities, IOTiming, WALTiming bool
	Functions                               string
}

// Tuple accepts already reportable counts; execution keeps unresolved attempts
// in Ledger until transaction/subtransaction settlement.
func (w *Writer) Tuple(p *Provider, k Key, policy Policy, inserts, updates, deletes int64) {
	if policy.Counts {
		w.Add(p, k, Fact{Inserts: inserts, Updates: updates, Deletes: deletes, InsertsSince: inserts})
	}
}

// FunctionUsage retains pending-local baselines, never a pointer into a map.
// The owner must not flush while an invocation is active.
type FunctionUsage struct {
	key Key
	generation uint64
	total, chargedSelf time.Duration
}

// BeginFunction captures recursion and backend self baselines under the same
// generation authority as publication. Selection happens before this boundary.
func (w *Writer) BeginFunction(p *Provider, k Key) (FunctionUsage, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, retired := p.retired[k]; retired {
		return FunctionUsage{}, false
	}
	if !w.initialized || w.generation != p.generation {
		w.pending = nil
		w.generation, w.initialized = p.generation, true
	}
	return FunctionUsage{key: k, generation: w.generation, total: w.pending[k].TotalTime, chargedSelf: w.chargedSelf}, true
}

// EndFunction records only a normally completed body. Replacing the pending
// total with saved-prefix + elapsed removes same-key recursive overlap while
// preserving completed descendants if their caller never completes.
func (w *Writer) EndFunction(p *Provider, usage FunctionUsage, elapsed time.Duration) {
	self := elapsed - (w.chargedSelf - usage.chargedSelf)
	w.chargedSelf += self
	p.mu.Lock()
	defer p.mu.Unlock()
	if usage.generation != p.generation || w.generation != p.generation {
		return
	}
	if _, retired := p.retired[usage.key]; retired {
		delete(w.pending, usage.key)
		return
	}
	if w.pending == nil {
		w.pending = make(map[Key]Fact)
	}
	f := w.pending[usage.key]
	f.Calls++
	f.TotalTime = usage.total + elapsed
	f.SelfTime += self
	w.pending[usage.key] = f
}
func (w *Writer) Function(p *Provider, k Key, policy Policy, language string, inlined bool, total, self time.Duration) {
	if inlined || language == "internal" || policy.Functions != "all" && (policy.Functions != "pl" || language != "pl") {
		return
	}
	w.Add(p, k, Fact{Calls: 1, TotalTime: total, SelfTime: self})
}
func (w *Writer) IO(p *Provider, k Key, policy Policy, operations int64, elapsed time.Duration) {
	if k.Category == WAL && !policy.WALTiming || k.Category != WAL && !policy.IOTiming {
		elapsed = 0
	}
	w.Add(p, k, Fact{Operations: operations, TotalTime: elapsed})
}

type Activity struct {
	Command          ids.CommandIdentity
	Query            string
	Start            time.Time
	Active, Disabled bool
}

func (p *Provider) Command(session ids.SessionToken, command ids.CommandIdentity, query string, at time.Time, enabled bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.activities == nil {
		p.activities = make(map[ids.SessionToken]Activity)
	}
	a := Activity{Disabled: !enabled}
	if enabled {
		a = Activity{Command: command, Query: query, Start: at, Active: true}
	}
	p.activities[session] = a
}
func (p *Provider) Idle(session ids.SessionToken) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if a, ok := p.activities[session]; ok {
		a.Active = false
		p.activities[session] = a
	}
}
func (p *Provider) End(session ids.SessionToken) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.activities, session)
}

// PendingDead inspects only this writer's reportable, unflushed dead delta.
// Provider replacement invalidates them without moving the throttle frontier.
func (w *Writer) PendingDead(p *Provider, k Key) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, retired := p.retired[k]; retired {
		return 0
	}
	if w.generation != p.generation {
		return 0
	}
	return w.pending[k].Dead
}

// ReportAnalyze is the source-specific immediate shared reporting boundary.
// The caller reconciles sampled estimates with its unresolved/pending work.
func (p *Provider) ReportAnalyze(k Key, policy Policy, at time.Time, elapsed time.Duration, live, dead int64, resetMods bool) {
	if !policy.Counts {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, retired := p.retired[k]; retired {
		return
	}
	if p.facts == nil {
		p.facts = make(map[Key]Fact)
	}
	f := p.facts[k]
	f.Live, f.Dead = max(live, 0), max(dead, 0)
	if resetMods {
		f.Modifications = 0
	}
	f.AnalyzeCount++
	f.LastAnalyze = at
	f.AnalyzeTime += elapsed
	p.facts[k] = f
}

// ReportVacuum resets shared estimates/counters, never a writer's pending
// prefix. A later ordinary flush still contributes that prefix in full.
func (p *Provider) ReportVacuum(k Key, policy Policy, at time.Time, elapsed time.Duration, live, dead int64) {
	if !policy.Counts {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, retired := p.retired[k]; retired {
		return
	}
	if p.facts == nil {
		p.facts = make(map[Key]Fact)
	}
	f := p.facts[k]
	f.Live, f.Dead, f.InsertsSince = max(live, 0), max(dead, 0), 0
	f.VacuumCount++
	f.LastVacuum = at
	f.VacuumTime += elapsed
	p.facts[k] = f
}

// Retire removes one category-qualified identity at its catalog lifetime
// boundary. Tombstones reject late batches and usage completions without
// changing unrelated facts, readers, generations or writer throttles.
// Statistics-only resets retain tombstones; replacement ends the incarnation.
func (p *Provider) Retire(k Key) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.retired == nil {
		p.retired = make(map[Key]struct{})
	}
	p.retired[k] = struct{}{}
	delete(p.facts, k)
}

// Retire also discards the retiring owner's local pending entry. Other writers
// reject the shared tombstone when they next collect, complete or publish.
func (w *Writer) Retire(p *Provider, k Key) {
	p.Retire(k)
	delete(w.pending, k)
}
