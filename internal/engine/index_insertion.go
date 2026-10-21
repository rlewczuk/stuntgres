package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
)

// Catalog-drift staging can refresh grants and immutable providers, but cannot
// manufacture a new checked insertion occurrence after the producer finished.
func (s *StatementScope) revalidateIndexRows(ref ids.ObjectRef, old []storage.RowVersion, batch *index.WriteBatch) error {
	prior := s.uniqueBinding
	if prior != nil && prior.Ref != ref {
		prior = s.indexBindings[ref.Address]
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
		e := s.engine()
		e.mu.Lock()
		auth, captureErr := s.writeAuthorizationLocked(e)
		if captureErr == nil && s.foreignActionAuth == nil {
			auth = s.routineAuth // Rule relation ownership is not routine authority.
		}
		if captureErr == nil {
			auth.Recapture(e.catalog.read(binding.Catalog))
		}
		e.mu.Unlock()
		if captureErr != nil {
			return captureErr
		}
		// Reconciliation checks actual surviving and folded calls, even when
		// a false predicate never initialized the executable key graph.
		if err := s.readmitPreparedPrograms(auth); err != nil {
			return err
		}
		if s.indexBindings[prior.Ref.Address] != prior {
			if err := prior.ReadmitPrograms(routineAccessOf(auth)); err != nil {
				return err
			}
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

// The mixed lane may need ordinary prior-own keys again after catalog drift.
// Freeze actual probe data by immutable row/version, not CHECK truth or values.
func (s *StatementScope) retainExistingIndexOutput(idx *catalog.Relation, row storage.RowVersion, output storage.IndexOutput) {
	if s.indexesOrZero().ExistingOutputs == nil {
		s.indexes().ExistingOutputs = make(map[ids.ObjectAddress]map[index.RowIdentity]storage.IndexOutput)
	}
	outputs := s.indexesOrZero().ExistingOutputs[idx.Address()]
	if outputs == nil {
		outputs = make(map[index.RowIdentity]storage.IndexOutput)
		s.indexes().ExistingOutputs[idx.Address()] = outputs
	}
	outputs[index.RowIdentity{Row: row.Row, Version: row.Version}] = output
}

// Only actual blocking-grant continuations call this, never a row Eval or an
// uncontended key probe. TxRef/CommandID are phase coordinates, not a substitute
// for this fresh coherent authority capture. All checks after unlock are pure
// graph admission: no provider, CHECK truth, codec or output replay.
func (s *StatementScope) readmitProgramsAfterWait() error {
	if len(s.domains.preparedChecks) == 0 && len(s.routinePrograms) == 0 && len(s.indexBindings) == 0 {
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
