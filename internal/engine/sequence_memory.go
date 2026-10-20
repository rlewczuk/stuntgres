package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// sessionSequences is a physical session's sequence memory: the per-sequence
// currval values and unused cache tails, the lastval target and the
// prepared-literal search-path cache. It is distinct from transactional
// sequence metadata and from the shared generator in storage. Engine.mu
// guards it; physical-session admission also serializes the path cache.
// Mutating methods carry the Locked suffix.
type sessionSequences struct {
	memories    map[ids.ObjectAddress]*sequenceMemory
	lastAddress ids.ObjectAddress
	path        sequencePathCache
}

// memory returns the session's memory of one sequence, or nil.
func (s *sessionSequences) memory(address ids.ObjectAddress) *sequenceMemory {
	return s.memories[address]
}

// demandLocked returns the session's memory of one sequence, creating it.
func (s *sessionSequences) demandLocked(address ids.ObjectAddress) *sequenceMemory {
	if s.memories == nil {
		s.memories = make(map[ids.ObjectAddress]*sequenceMemory)
	}
	m := s.memories[address]
	if m == nil {
		m = &sequenceMemory{}
		s.memories[address] = m
	}
	return m
}

// last is the sequence lastval reads; zero until a nextval in this session.
func (s *sessionSequences) last() ids.ObjectAddress {
	return s.lastAddress
}

func (s *sessionSequences) recordLastLocked(address ids.ObjectAddress) {
	s.lastAddress = address
}

// forgetLocked drops every memory and the lastval target when the session's
// TEMP objects end. The path cache is keyed by its inputs and survives.
func (s *sessionSequences) forgetLocked() {
	s.memories = nil
	s.lastAddress = ids.ObjectAddress{}
}

// discardLocked implements DISCARD ALL (all) and DISCARD SEQUENCES. DISCARD
// SEQUENCES forgets values and unused tails, not an unconsumed relation-source
// invalidation after rollback.
func (s *sessionSequences) discardLocked(all bool) {
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

// retainLocked keeps only the memories keep accepts; a dropped lastval target
// is forgotten with its memory.
func (s *sessionSequences) retainLocked(keep func(ids.ObjectAddress) bool) {
	for address := range s.memories {
		if !keep(address) {
			delete(s.memories, address)
			if s.lastAddress == address {
				s.lastAddress = ids.ObjectAddress{}
			}
		}
	}
}

// cachedPath returns the effective path cached for exactly this key.
func (s *sessionSequences) cachedPath(key sequencePathKey) (sequenceEnvironment, bool) {
	if s.path.valid && s.path.key == key {
		return s.path.environment, true
	}
	return sequenceEnvironment{}, false
}

func (s *sessionSequences) storePathLocked(key sequencePathKey, environment sequenceEnvironment) {
	s.path = sequencePathCache{valid: true, key: key, environment: environment}
}

// sequenceMemory is one session's view of one sequence: the currval value,
// the unused tail of its last cache reservation and the generator generation
// that tail belongs to.
type sequenceMemory struct {
	value      int64
	valid      bool
	next       int64
	remaining  uint64
	generation ids.DefinitionRevision
	// Local relation-source invalidation survives reverted reset without
	// borrowing generator/cache identity or changing currval/lastval.
	analysisStamp ids.DefinitionRevision
}

// observeLocked drops a tail reserved from an older generator generation.
func (m *sequenceMemory) observeLocked(generation ids.DefinitionRevision) {
	if m.generation != generation {
		m.remaining = 0
		m.generation = generation
	}
}

// current is the currval value, valid after a nextval or a called setval.
func (m *sequenceMemory) current() (int64, bool) {
	return m.value, m.valid
}

// nextLocked returns the next value, reserving a new cache tail from the
// generator when the current one is used up.
func (m *sequenceMemory) nextLocked(options catalog.SequenceOptions, g *storage.SequenceGenerator, name string) (int64, error) {
	if m.remaining == 0 {
		first, count, err := reserveSequence(options, g, name)
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

// setLocked records setval: the tail is discarded, and a called value
// becomes currval.
func (m *sequenceMemory) setLocked(value int64, called bool) {
	m.remaining = 0
	if called {
		m.value, m.valid = value, true
	}
}

// discardTailLocked drops the unused cache tail; currval is unchanged.
func (m *sequenceMemory) discardTailLocked() {
	m.remaining = 0
}

// discardStaleTailLocked drops the tail unless it belongs to generation.
func (m *sequenceMemory) discardStaleTailLocked(generation ids.DefinitionRevision) {
	if m.generation != generation {
		m.remaining = 0
	}
}

// markSourceEventLocked records a local reset (TRUNCATE RESTART IDENTITY):
// the old tail is discarded and stamp invalidates prepared literals.
func (m *sequenceMemory) markSourceEventLocked(stamp ids.DefinitionRevision) {
	m.remaining = 0
	m.analysisStamp = stamp
}

// sourceStamp is the latest local relation-source event, or zero.
func (m *sequenceMemory) sourceStamp() ids.DefinitionRevision {
	return m.analysisStamp
}

// forgetValuesLocked clears everything but the source stamp and reports
// whether the memory still carries one.
func (m *sequenceMemory) forgetValuesLocked() bool {
	if m.analysisStamp == 0 {
		return false
	}
	*m = sequenceMemory{analysisStamp: m.analysisStamp}
	return true
}
