package engine

import (
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Counters remain mutable and nontransactional within one generator binding.
// These observations fence the binding/descriptor, never a snapshot of counters.
type truncateSequence struct {
	ref       ids.ObjectRef
	relation  *relationDescriptor
	generator *sequenceGenerator
	committed *sequenceGenerator
}

func truncateOwnsSequence(d *relationDescriptor, selected map[ids.ObjectAddress]int) bool {
	if d.relationKind != sequenceRelation || d.sequence.owned == (ids.ObjectAddress{}) {
		return false
	}
	a := d.sequence.owned
	a.SubID = 0
	return selected[a] != 0
}

// Table closure and its privilege checks finish before any owned-sequence AX.
// Capture under metadata authority, wait without it, then revalidate exactly.
func (s *StatementScope) acquireTruncateSequences(stmt *parser.Truncate) error {
	e := s.op.session.Engine()
	e.mu.Lock()
	auth, err := e.statementAuthorizationLocked(s.op, stmt)
	if err == nil {
		for a, descriptor := range auth.catalog.state.descriptors {
			d, ok := descriptor.(*relationDescriptor)
			if !ok || !truncateOwnsSequence(d, s.truncateSelected) {
				continue
			}
			s.truncateSequences = append(s.truncateSequences, truncateSequence{
				ref:      e.catalog.domain.Ref(s.txRef.Epoch(), a),
				relation: d, generator: e.sequenceGeneratorLocked(s, a), committed: e.sequences[a],
			})
		}
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	slices.SortFunc(s.truncateSequences, func(a, b truncateSequence) int { return compareTruncateAddress(a.ref.Address, b.ref.Address) })
	for _, observed := range s.truncateSequences {
		if err = s.acquireSequence(observed.ref, accessExclusive, true); err != nil {
			return err
		}
		e.mu.Lock()
		auth, err = e.statementAuthorizationLocked(s.op, stmt)
		if err == nil {
			if auth.catalog.state.descriptors[observed.ref.Address] != observed.relation || e.sequenceGeneratorLocked(s, observed.ref.Address) != observed.generator || observed.generator == nil || e.sequences[observed.ref.Address] != observed.committed {
				err = ErrCatalogReplaced
			} else {
				err = auth.Check(AccessRequirement{Object: observed.ref, OwnerOnly: true})
			}
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

// Caller holds Engine.mu. Stage fresh bindings with the same catalog candidate
// as table/index roots. Rollback discards these bindings, not counter mutations
// made on the original generator before reset.
func (s *StatementScope) stageTruncateSequences(auth AuthorizationView, stage *CatalogStage) (map[ids.ObjectAddress]*sequenceGenerator, error) {
	e := s.op.session.Engine()
	var observed map[ids.ObjectAddress]*sequenceGenerator
	if len(s.truncateSequences) != 0 {
		observed = make(map[ids.ObjectAddress]*sequenceGenerator, len(s.truncateSequences))
	}
	for _, sequence := range s.truncateSequences {
		a := sequence.ref.Address
		if auth.catalog.state.descriptors[a] != sequence.relation || e.sequenceGeneratorLocked(s, a) != sequence.generator || e.sequences[a] != sequence.committed {
			return nil, ErrCatalogReplaced
		}
		if err := auth.Check(AccessRequirement{Object: sequence.ref, OwnerOnly: true}); err != nil {
			return nil, err
		}
		observed[a] = sequence.committed
	}
	// Host publication need not honor SQL locks. Do not silently omit an owned
	// sequence added between closure capture and staging, even for an empty set.
	for a, descriptor := range auth.catalog.state.descriptors {
		if d, ok := descriptor.(*relationDescriptor); ok && truncateOwnsSequence(d, s.truncateSelected) {
			if _, captured := observed[a]; !captured {
				return nil, CatalogConflict{}
			}
		}
	}
	if len(s.truncateSequences) != 0 {
		stage.generators = make(map[ids.ObjectAddress]*sequenceGenerator, len(s.truncateSequences))
	}
	for _, sequence := range s.truncateSequences {
		rev, err := e.catalog.revisionLocked()
		if err != nil {
			return nil, err
		}
		d := *sequence.relation
		d.revision, d.sequencePlanStamp = rev, rev
		if err = stage.Replace(&d); err != nil {
			return nil, err
		}
		stage.generators[d.address] = &sequenceGenerator{last: d.sequence.start, generation: rev}
	}
	session := s.op.session
	if len(s.truncateSequences) != 0 && session.sequences == nil {
		session.sequences = make(map[ids.ObjectAddress]*sequenceMemory, len(s.truncateSequences))
	}
	for _, sequence := range s.truncateSequences {
		a := sequence.ref.Address
		m := session.sequences[a]
		if m == nil {
			m = &sequenceMemory{}
			session.sequences[a] = m
		}
		// Reset itself discards the initiating backend's old unused tail,
		// even if it rolls back without another function demand. Its local
		// source event also survives rollback, without changing observation
		// validity/value, the last-demanded sequence, or a peer's analysis.
		m.remaining = 0
		m.analysisStamp = stage.descriptors[a].(*relationDescriptor).sequencePlanStamp
	}
	s.catalogAuthority = auth.catalogAuthorityLocked(ids.ObjectRef{}, nil)
	return observed, nil
}
