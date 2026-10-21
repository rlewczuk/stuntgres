package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
)

// revalidateIndexRows rebinds after a stale unique binding, readmits the
// statement's programs and reprobes every row of batch.
func (s *StatementScope) revalidateIndexRows(ref ids.ObjectRef, old []storage.RowVersion, batch *index.WriteBatch) error {
	prior := s.UniqueBinding
	if prior != nil && prior.Ref != ref {
		prior = s.IndexBindings[ref.Address]
	}
	binding, err := s.bindUniqueWrite(ref)
	if err != nil {
		return err
	}
	purpose := index.ProbeRuntime
	if binding.Checked || prior != nil && prior.Checked {
		purpose = index.ProbeRevalidation
		if prior != nil && !index.SameRelationRowShape(prior.Parent, binding.Parent) {
			return ids.ErrCatalogReplaced
		}
	}
	if prior != nil {
		if err := s.writeHost().ReadmitRevalidation(prior, binding); err != nil {
			return err
		}
	}
	for i := range batch.Values {
		candidate := batch.Candidate(i)
		var excluded []storage.RowVersion
		if old != nil {
			excluded = old[:i+1]
		}
		conflict, err := s.probeUniqueRow(ref, &candidate, excluded, batch.Prefix(i), nil, purpose)
		if err != nil {
			return err
		}
		if conflict.index != nil {
			return conflict.diagnostic(s, ref)
		}
		batch.Set(i, candidate)
	}
	return nil
}

// Only actual blocking-grant continuations call this, never a row Eval or an
// uncontended key probe. TxRef/CommandID are phase coordinates, not a substitute
// for this fresh coherent authority capture. All checks after unlock are pure
// graph admission: no provider, CHECK truth, codec or output replay.
func (s *StatementScope) readmitProgramsAfterWait() error {
	if len(s.domains.preparedChecks) == 0 && len(s.routinePrograms) == 0 && len(s.IndexBindings) == 0 {
		return nil
	}
	e := s.engine()
	e.mu.Lock()
	err := s.validateLockedRelation(e)
	var auth AuthorizationView
	if err == nil {
		auth, err = s.writeAuthorizationLocked(e)
		if err == nil {
			auth.Recapture(e.privateCatalogLocked(s.owner.Catalog))
		}
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	return s.readmitPreparedPrograms(auth)
}
