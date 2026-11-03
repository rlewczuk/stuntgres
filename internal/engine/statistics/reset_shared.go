package statistics

import (
	"math/bits"
	"stuntgres/internal/engine/ids"
	"time"
)

// SharedKind is a fixed statistics family, never a catalog object identity.
type SharedKind uint8

const (
	Archiver SharedKind = iota
	BGWriter
	Checkpointer
	SharedIO
	RecoveryPrefetch
	SLRU
	SharedWAL
	SharedKindCount
)

var sharedNames = [...]string{"archiver", "bgwriter", "checkpointer", "io", "recovery_prefetch", "slru", "wal"}

func SharedTarget(name string) (SharedKind, bool) {
	for k, n := range sharedNames {
		if n == name {
			return SharedKind(k), true
		}
	}
	return 0, false
}
func (k SharedKind) Name() string {
	if k >= SharedKindCount {
		return ""
	}
	return sharedNames[k]
}

type SLRUKind uint8

const (
	CommitTimestamp SLRUKind = iota
	MultixactMember
	MultixactOffset
	Notify
	Serializable
	Subtransaction
	Transaction
	OtherSLRU
	SLRUCount
)

var slruNames = [...]string{"commit_timestamp", "multixact_member", "multixact_offset", "notify", "serializable", "subtransaction", "transaction", "other"}

func SLRUTarget(name string) SLRUKind {
	for k, n := range slruNames {
		if n == name {
			return SLRUKind(k)
		}
	}
	return OtherSLRU
}
func (k SLRUKind) Name() string {
	if k >= SLRUCount {
		return ""
	}
	return slruNames[k]
}

// SharedFact contains logical cumulative events. IO/WAL operations remain solely
// in keyed Fact records. Recovery gauges are current state, not reset counters.
type SharedFact struct {
	Counters                            [6]int64
	TotalTime                           time.Duration
	LastSuccess, LastFailure            string
	LastSuccessAt, LastFailureAt        time.Time
	WALDistance, BlockDistance, IODepth int64
	ResetAt                             time.Time
}

// SLRUFact keeps the seven SLRU counters in PostgreSQL column order:
// zeroed, hit, read, written, exists, flushes, truncates.
type SLRUFact struct {
	Counters [7]int64
	ResetAt  time.Time
}

func mergeSLRU(a, b SLRUFact) SLRUFact {
	for i := range a.Counters {
		a.Counters[i] += b.Counters[i]
	}
	return a
}
type BackendFact struct {
	IO, WAL Fact
	ResetAt time.Time
}

func mergeShared(a, b SharedFact) SharedFact {
	for i := range a.Counters {
		a.Counters[i] += b.Counters[i]
	}
	a.TotalTime += b.TotalTime
	if !b.LastSuccessAt.IsZero() {
		a.LastSuccess, a.LastSuccessAt = b.LastSuccess, b.LastSuccessAt
	}
	if !b.LastFailureAt.IsZero() {
		a.LastFailure, a.LastFailureAt = b.LastFailure, b.LastFailureAt
	}
	return a
}
func (p *Provider) ReadShared(k SharedKind) SharedFact {
	p.mu.Lock()
	defer p.mu.Unlock()
	if k >= SharedKindCount {
		return SharedFact{}
	}
	return p.shared[k]
}
func (p *Provider) ReadSLRU(k SLRUKind) SLRUFact {
	if k >= SLRUCount {
		return SLRUFact{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.slru[k]
}

// ReadSLRUs captures the complete fixed family under one leaf lock.
func (p *Provider) ReadSLRUs() [SLRUCount]SLRUFact {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.slru
}
func (p *Provider) ReadBackend(token ids.SessionToken) (BackendFact, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f, ok := p.backends[token]
	return f, ok
}

// ReportPrefetchGauges sets actual instantaneous measurements without inventing
// cumulative work or entering the writer's publication throttle.
func (p *Provider) ReportPrefetchGauges(wal, block, depth int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f := &p.shared[RecoveryPrefetch]
	f.WALDistance, f.BlockDistance, f.IODepth = wal, block, depth
}
func (p *Provider) resetShared(k SharedKind, at time.Time) {
	old := p.shared[k]
	p.shared[k] = SharedFact{ResetAt: at}
	if k == RecoveryPrefetch {
		p.shared[k].WALDistance, p.shared[k].BlockDistance, p.shared[k].IODepth = old.WALDistance, old.BlockDistance, old.IODepth
	}
	if k == SLRU {
		for i := range p.slru {
			p.slru[i] = SLRUFact{ResetAt: at}
		}
	}
	category := IO
	if k == SharedWAL {
		category = WAL
	}
	if k == SharedIO || k == SharedWAL {
		for key, f := range p.facts {
			if key.Category == category {
				f.Operations, f.TotalTime = 0, 0
				p.facts[key] = f
			}
		}
	}
}

// Resets only published state. Pending batches, generation, readers, positive
// backend registrations and catalog-retirement tombstones remain untouched.
func (p *Provider) ResetShared(k SharedKind, at time.Time) {
	if k >= SharedKindCount {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resetShared(k, at)
}
func (p *Provider) ResetAllShared(at time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k := SharedKind(0); k < SharedKindCount; k++ {
		p.resetShared(k, at)
	}
}
func (p *Provider) ResetSLRU(k SLRUKind, at time.Time) {
	if k >= SLRUCount {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.slru[k] = SLRUFact{ResetAt: at}
}
func (p *Provider) ResetBackend(token ids.SessionToken, at time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.backends[token]; !ok {
		return false
	}
	p.backends[token] = BackendFact{IO: Fact{ResetAt: at}, WAL: Fact{ResetAt: at}, ResetAt: at}
	return true
}
func validBackendToken(token ids.SessionToken) bool {
	return token.Domain() != nil && token.ID() != 0 && !token.IsDetached() && token.Private() == nil
}

// BeginBackend grants positive reporting authority only at successful actual
// public-session publication. A report cannot register its own stale identity.
// Core serializes registration, inheritance and end under the cluster mutex.
func (p *Provider) BeginBackend(token ids.SessionToken) bool {
	if !validBackendToken(token) {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.liveBackends == nil {
		p.liveBackends = make(map[ids.SessionToken]struct{})
	}
	p.liveBackends[token] = struct{}{}
	return true
}

// InheritBackendRegistrations installs only current positive lifetime authority
// into a new target at its no-failure publication boundary. The cluster mutex
// excludes concurrent lifetime changes; provider locks are acquired separately.
// No facts, pending work, reset history or session-owner pointers are copied.
func (p *Provider) InheritBackendRegistrations(source *Provider) {
	if p == source {
		return
	}
	source.mu.Lock()
	var live map[ids.SessionToken]struct{}
	if len(source.liveBackends) != 0 {
		live = make(map[ids.SessionToken]struct{}, len(source.liveBackends))
		for token := range source.liveBackends {
			live[token] = struct{}{}
		}
	}
	source.mu.Unlock()
	p.mu.Lock()
	p.liveBackends = live
	p.mu.Unlock()
}

func (p *Provider) endBackend(token ids.SessionToken) {
	delete(p.backends, token)
	delete(p.liveBackends, token)
	if len(p.backends) == 0 {
		p.backends = nil
	}
	if len(p.liveBackends) == 0 {
		p.liveBackends = nil
	}
}

// Replacement discards metrics, not positive authority of still-attached
// public sessions. The existing provider generation rejects old pending work.
func (p *Provider) clearShared(at time.Time) {
	p.shared = [SharedKindCount]SharedFact{}
	p.slru = [SLRUCount]SLRUFact{}
	p.backends = nil
	for k := range p.shared {
		p.shared[k].ResetAt = at
	}
	for k := range p.slru {
		p.slru[k].ResetAt = at
	}
}
func (p *Provider) factReset(category Category) time.Time {
	if category == IO {
		return p.shared[SharedIO].ResetAt
	}
	if category == WAL {
		return p.shared[SharedWAL].ResetAt
	}
	return p.reset
}

func (w *Writer) clearPending() {
	w.pending = nil
	if w.sharedDirty != 0 {
		w.sharedPending = [SharedKindCount]SharedFact{}
		w.sharedDirty = 0
	}
	if w.slruDirty != 0 {
		w.slruPending = [SLRUCount]SLRUFact{}
		w.slruDirty = 0
	}
	w.backendPending = nil
}
func (w *Writer) initialize(p *Provider) {
	if !w.initialized || w.generation != p.generation {
		w.clearPending()
		w.generation, w.initialized = p.generation, true
	}
}
func (w *Writer) Shared(p *Provider, k SharedKind, delta SharedFact) {
	if k >= SharedKindCount || k == SharedIO || k == SharedWAL || k == SLRU || delta == (SharedFact{}) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	w.initialize(p)
	w.sharedPending[k] = mergeShared(w.sharedPending[k], delta)
	w.sharedDirty |= 1 << k
}
func (w *Writer) SLRU(p *Provider, k SLRUKind, delta SLRUFact) {
	if k >= SLRUCount || delta == (SLRUFact{}) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	w.initialize(p)
	w.slruPending[k] = mergeSLRU(w.slruPending[k], delta)
	w.slruDirty |= 1 << k
}
func (w *Writer) Backend(p *Provider, token ids.SessionToken, io, wal Fact) {
	if !validBackendToken(token) || io == (Fact{}) && wal == (Fact{}) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, live := p.liveBackends[token]; !live {
		return
	}
	w.initialize(p)
	if w.backendPending == nil {
		w.backendPending = make(map[ids.SessionToken]BackendFact)
	}
	f := w.backendPending[token]
	f.IO = merge(f.IO, io)
	f.WAL = merge(f.WAL, wal)
	w.backendPending[token] = f
}

// Called only by the ordinary Writer.Flush, under the provider leaf mutex.
func (w *Writer) flushShared(p *Provider) {
	for dirty := w.sharedDirty; dirty != 0; dirty &= dirty - 1 {
		k := bits.TrailingZeros8(dirty)
		p.shared[k] = mergeShared(p.shared[k], w.sharedPending[k])
	}
	for dirty := w.slruDirty; dirty != 0; dirty &= dirty - 1 {
		k := bits.TrailingZeros8(dirty)
		p.slru[k] = mergeSLRU(p.slru[k], w.slruPending[k])
	}
	for token, delta := range w.backendPending {
		if _, live := p.liveBackends[token]; !live {
			continue
		}
		if p.backends == nil {
			p.backends = make(map[ids.SessionToken]BackendFact)
		}
		f := p.backends[token]
		f.IO = merge(f.IO, delta.IO)
		f.WAL = merge(f.WAL, delta.WAL)
		p.backends[token] = f
	}
}
