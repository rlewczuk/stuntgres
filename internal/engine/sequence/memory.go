package sequence

import (
	"iter"
	"maps"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// SessionMemory is a physical session's sequence memory: the per-sequence
// currval values and unused cache tails, the lastval target and the
// prepared-literal search-path cache. It is distinct from transactional
// sequence metadata and from the shared generator in storage. Engine.mu
// guards it; physical-session admission also serializes the path cache.
// Mutating methods carry the Locked suffix.
type SessionMemory struct {
	memories    map[ids.ObjectAddress]*Memory
	lastAddress ids.ObjectAddress
	path        pathCache
}

// Memory returns the session's memory of one sequence, or nil.
func (s *SessionMemory) Memory(address ids.ObjectAddress) *Memory {
	return s.memories[address]
}

// Len is the number of sequences the session remembers.
func (s *SessionMemory) Len() int {
	return len(s.memories)
}

// All yields every remembered sequence and its memory, for inspection.
func (s *SessionMemory) All() iter.Seq2[ids.ObjectAddress, *Memory] {
	return maps.All(s.memories)
}

// DemandLocked returns the session's memory of one sequence, creating it.
func (s *SessionMemory) DemandLocked(address ids.ObjectAddress) *Memory {
	if s.memories == nil {
		s.memories = make(map[ids.ObjectAddress]*Memory)
	}
	m := s.memories[address]
	if m == nil {
		m = &Memory{}
		s.memories[address] = m
	}
	return m
}

// Last is the sequence lastval reads; zero until a nextval in this session.
func (s *SessionMemory) Last() ids.ObjectAddress {
	return s.lastAddress
}

// RecordLastLocked makes address the lastval target.
func (s *SessionMemory) RecordLastLocked(address ids.ObjectAddress) {
	s.lastAddress = address
}

// ForgetLocked drops every memory and the lastval target when the session's
// TEMP objects end. The path cache is keyed by its inputs and survives.
func (s *SessionMemory) ForgetLocked() {
	s.memories = nil
	s.lastAddress = ids.ObjectAddress{}
}

// DiscardLocked implements DISCARD ALL (all) and DISCARD SEQUENCES. DISCARD
// SEQUENCES forgets values and unused tails, not an unconsumed relation-source
// invalidation after rollback.
func (s *SessionMemory) DiscardLocked(all bool) {
	if all {
		clear(s.memories) // The full plan-generation event survives.
	} else {
		for address, memory := range s.memories {
			if !memory.forgetValuesLocked() {
				delete(s.memories, address)
			}
		}
	}
	s.lastAddress = ids.ObjectAddress{}
}

// RetainLocked keeps only the memories keep accepts; a dropped lastval target
// is forgotten with its memory.
func (s *SessionMemory) RetainLocked(keep func(ids.ObjectAddress) bool) {
	for address := range s.memories {
		if !keep(address) {
			delete(s.memories, address)
			if s.lastAddress == address {
				s.lastAddress = ids.ObjectAddress{}
			}
		}
	}
}

// CachedPath returns the effective path cached for exactly this key.
func (s *SessionMemory) CachedPath(key PathKey) (Environment, bool) {
	if s.path.valid && s.path.key == key {
		return s.path.environment, true
	}
	return Environment{}, false
}

// CachedPathKey returns the key of the cached effective path, if any.
func (s *SessionMemory) CachedPathKey() (PathKey, bool) {
	return s.path.key, s.path.valid
}

// StorePathLocked caches the effective path computed for key.
func (s *SessionMemory) StorePathLocked(key PathKey, environment Environment) {
	s.path = pathCache{valid: true, key: key, environment: environment}
}

// Memory is one session's view of one sequence: the currval value,
// the unused tail of its last cache reservation and the generator generation
// that tail belongs to.
type Memory struct {
	value      int64
	valid      bool
	next       int64
	remaining  uint64
	generation ids.DefinitionRevision
	// Local relation-source invalidation survives reverted reset without
	// borrowing generator/cache identity or changing currval/lastval.
	analysisStamp ids.DefinitionRevision
}

// ObserveLocked drops a tail reserved from an older generator generation.
func (m *Memory) ObserveLocked(generation ids.DefinitionRevision) {
	if m.generation != generation {
		m.remaining = 0
		m.generation = generation
	}
}

// Current is the currval value, valid after a nextval or a called setval.
func (m *Memory) Current() (int64, bool) {
	return m.value, m.valid
}

// CachedTail is the number of reserved values not yet returned.
func (m *Memory) CachedTail() uint64 {
	return m.remaining
}

// Generation is the generator generation the cached tail belongs to.
func (m *Memory) Generation() ids.DefinitionRevision {
	return m.generation
}

// NextLocked returns the next value, reserving a new cache tail from the
// generator when the current one is used up.
func (m *Memory) NextLocked(options catalog.SequenceOptions, g *storage.SequenceGenerator, name string) (int64, error) {
	if m.remaining == 0 {
		first, count, err := reserve(options, g, name)
		if err != nil {
			return 0, err
		}
		m.next, m.remaining = first, count
	}
	result := m.next
	m.remaining--
	if m.remaining != 0 {
		m.next += options.Increment
	}
	m.value, m.valid = result, true
	return result, nil
}

// SetLocked records setval: the tail is discarded, and a called value
// becomes currval.
func (m *Memory) SetLocked(value int64, called bool) {
	m.remaining = 0
	if called {
		m.value, m.valid = value, true
	}
}

// DiscardTailLocked drops the unused cache tail; currval is unchanged.
func (m *Memory) DiscardTailLocked() {
	m.remaining = 0
}

// DiscardStaleTailLocked drops the tail unless it belongs to generation.
func (m *Memory) DiscardStaleTailLocked(generation ids.DefinitionRevision) {
	if m.generation != generation {
		m.remaining = 0
	}
}

// MarkSourceEventLocked records a local reset (TRUNCATE RESTART IDENTITY):
// the old tail is discarded and stamp invalidates prepared literals.
func (m *Memory) MarkSourceEventLocked(stamp ids.DefinitionRevision) {
	m.remaining = 0
	m.analysisStamp = stamp
}

// SourceStamp is the latest local relation-source event, or zero.
func (m *Memory) SourceStamp() ids.DefinitionRevision {
	return m.analysisStamp
}

// forgetValuesLocked clears everything but the source stamp and reports
// whether the memory still carries one.
func (m *Memory) forgetValuesLocked() bool {
	if m.analysisStamp == 0 {
		return false
	}
	*m = Memory{analysisStamp: m.analysisStamp}
	return true
}
