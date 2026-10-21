package engine

import (
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

// Table closure and its privilege checks finish before any owned-sequence AX.
// Capture under metadata authority, wait without it, then revalidate exactly.
func (s *StatementScope) acquireTruncateSequences(stmt *parser.Truncate) error {
	e := s.engine()
	e.mu.Lock()
	auth, err := e.statementAuthorizationLocked(s.op, stmt)
	if err == nil {
		for a, descriptor := range auth.Catalog.Descriptors() {
			d, ok := descriptor.(*catalog.Relation)
			if !ok || !dml.TruncateOwnsSequence(d, s.truncateOrZero().Selected) {
				continue
			}
			s.truncate().Sequences = append(s.truncateOrZero().Sequences, dml.TruncateSequence{
				Ref:      e.catalog.Domain.Ref(s.txRef.Epoch(), a),
				Relation: d, Generator: e.sequenceGeneratorLocked(s, a), Committed: e.storage.Sequences[a],
			})
		}
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	slices.SortFunc(s.truncateOrZero().Sequences, func(a, b dml.TruncateSequence) int { return dml.CompareTruncateAddress(a.Ref.Address, b.Ref.Address) })
	for _, observed := range s.truncateOrZero().Sequences {
		if err = s.acquireSequence(observed.Ref, lock.AccessExclusive, true); err != nil {
			return err
		}
		e.mu.Lock()
		auth, err = e.statementAuthorizationLocked(s.op, stmt)
		if err == nil {
			if auth.Catalog.Get(observed.Ref.Address) != observed.Relation || e.sequenceGeneratorLocked(s, observed.Ref.Address) != observed.Generator || observed.Generator == nil || e.storage.Sequences[observed.Ref.Address] != observed.Committed {
				err = ids.ErrCatalogReplaced
			} else {
				err = auth.Check(access.Requirement{Object: observed.Ref, OwnerOnly: true})
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
func (s *StatementScope) stageTruncateSequences(auth AuthorizationView, stage *CatalogStage) (map[ids.ObjectAddress]*storage.SequenceGenerator, error) {
	e := s.engine()
	var observed map[ids.ObjectAddress]*storage.SequenceGenerator
	if len(s.truncateOrZero().Sequences) != 0 {
		observed = make(map[ids.ObjectAddress]*storage.SequenceGenerator, len(s.truncateOrZero().Sequences))
	}
	for _, sequence := range s.truncateOrZero().Sequences {
		a := sequence.Ref.Address
		if auth.Catalog.Get(a) != sequence.Relation || e.sequenceGeneratorLocked(s, a) != sequence.Generator || e.storage.Sequences[a] != sequence.Committed {
			return nil, ids.ErrCatalogReplaced
		}
		if err := auth.Check(access.Requirement{Object: sequence.Ref, OwnerOnly: true}); err != nil {
			return nil, err
		}
		observed[a] = sequence.Committed
	}
	// Host publication need not honor SQL locks. Do not silently omit an owned
	// sequence added between closure capture and staging, even for an empty set.
	for a, descriptor := range auth.Catalog.Descriptors() {
		if d, ok := descriptor.(*catalog.Relation); ok && dml.TruncateOwnsSequence(d, s.truncateOrZero().Selected) {
			if _, captured := observed[a]; !captured {
				return nil, catalog.Conflict{}
			}
		}
	}
	if len(s.truncateOrZero().Sequences) != 0 {
		stage.sequenceState().ResetGenerators(len(s.truncateOrZero().Sequences))
	}
	for _, sequence := range s.truncateOrZero().Sequences {
		rev, err := e.catalog.revisionLocked()
		if err != nil {
			return nil, err
		}
		d := *sequence.Relation
		d.ObjectRevision, d.SequencePlanStamp = rev, rev
		if err = stage.Replace(&d); err != nil {
			return nil, err
		}
		stage.sequence.SetGenerator(d.Address(), &storage.SequenceGenerator{Last: d.Sequence.Start, Generation: rev})
	}
	session := s.op.Session
	for _, sequence := range s.truncateOrZero().Sequences {
		a := sequence.Ref.Address
		// Reset itself discards the initiating backend's old unused tail,
		// even if it rolls back without another function demand. Its local
		// source event also survives rollback, without changing observation
		// validity/value, the last-demanded sequence, or a peer's analysis.
		payloadOf(session).sequences.DemandLocked(a).MarkSourceEventLocked(stage.Get(a).(*catalog.Relation).SequencePlanStamp)
	}
	s.catalogAuthority = auth.catalogAuthorityLocked(ids.ObjectRef{}, nil)
	return observed, nil
}
