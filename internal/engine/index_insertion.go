package engine

import (
	"maps"

	"stuntgres/internal/engine/ids"
)

// Only a target with checked nonunique indexes needs a mixed insertion lane.
// Unique/exclusion bindings retain their existing ordered fast path otherwise.
type indexInsertionBinding struct {
	index       *relationDescriptor
	parent      *relationDescriptor
	preparation indexExecutionPreparation
	unique      *uniqueBoundKey
	ordering    indexOrdering
	hash        hashIndexBinding
}

func (r *indexWriteCandidate) materializeInsertion(bound *indexInsertionBinding, evaluation *EvalContext) error {
	if output, exists := r.indexes[bound.index.Address()]; exists {
		if !output.matches(bound.index) {
			return ids.ErrCatalogReplaced
		}
		return nil
	}
	output, err := materializeIndexOutput(evaluation, &bound.preparation, bound.parent, bound.index, &bound.ordering, &bound.hash, r.values)
	if err != nil {
		return err
	}
	if r.indexes == nil {
		r.indexes = make(map[ids.ObjectAddress]indexRowOutput)
	} else if r.sharedIndexes {
		r.indexes = maps.Clone(r.indexes)
	}
	r.sharedIndexes = false
	r.indexes[bound.index.Address()] = output
	return nil
}

type indexProbePurpose uint8

const (
	indexProbeRuntime indexProbePurpose = iota
	indexProbeTracked
	indexProbeRevalidation
)

// Catalog-drift staging can refresh grants and immutable providers, but cannot
// manufacture a new checked insertion occurrence after the producer finished.
func (s *StatementScope) revalidateIndexRows(ref ids.ObjectRef, old []rowVersion, batch *indexWriteBatch) error {
	prior := s.uniqueBinding
	if prior != nil && prior.ref != ref {
		prior = s.indexBindings[ref.Address]
	}
	binding, err := s.bindUniqueWrite(ref)
	if err != nil {
		return err
	}
	purpose := indexProbeRuntime
	if binding.checked || prior != nil && prior.checked {
		purpose = indexProbeRevalidation
		if prior != nil && !sameRelationRowShape(prior.parent, binding.parent) {
			return ids.ErrCatalogReplaced
		}
	}
	if prior != nil {
		e := s.op.session.Engine()
		e.mu.Lock()
		auth, captureErr := s.writeAuthorizationLocked(e)
		if captureErr == nil && s.foreignActionAuth == nil {
			auth = s.routineAuth // Rule relation ownership is not routine authority.
		}
		if captureErr == nil {
			auth.recaptureCatalog(e.catalog.read(binding.catalog))
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
		if s.indexBindings[prior.ref.Address] != prior {
			if err := prior.readmitPrograms(auth); err != nil {
				return err
			}
		}
	}
	for i := range batch.values {
		candidate := batch.candidate(i)
		var excluded []rowVersion
		if old != nil {
			excluded = old[:i+1]
		}
		conflict, err := s.probeUniqueRow(ref, &candidate, excluded, batch.prefix(i), nil, purpose)
		if err != nil {
			return err
		}
		if conflict.index != nil {
			return conflict.diagnostic(s, ref)
		}
		batch.set(i, candidate)
	}
	return nil
}

// The mixed lane may need ordinary prior-own keys again after catalog drift.
// Freeze actual probe data by immutable row/version, not CHECK truth or values.
func (s *StatementScope) retainExistingIndexOutput(index *relationDescriptor, row rowVersion, output indexRowOutput) {
	if s.existingIndexOutputs == nil {
		s.existingIndexOutputs = make(map[ids.ObjectAddress]map[indexRowIdentity]indexRowOutput)
	}
	outputs := s.existingIndexOutputs[index.Address()]
	if outputs == nil {
		outputs = make(map[indexRowIdentity]indexRowOutput)
		s.existingIndexOutputs[index.Address()] = outputs
	}
	outputs[indexRowIdentity{row: row.row, version: row.version}] = output
}

// Existing command-private insertion records own the consumed graph. Reuse it
// before discarding/rebinding a catalog-drifted binding; cooked values alone
// cannot certify the original call after a wait.
func (b *uniqueWriteBinding) readmitPrograms(auth AuthorizationView) error {
	for i := range b.keys {
		if err := b.keys[i].preparation.admission.readmit(auth); err != nil {
			return err
		}
	}
	for i := range b.insertion {
		if b.insertion[i].unique != nil {
			continue
		}
		if err := b.insertion[i].preparation.admission.readmit(auth); err != nil {
			return err
		}
	}
	return nil
}

// Only actual blocking-grant continuations call this, never a row Eval or an
// uncontended key probe. TxRef/CommandID are phase coordinates, not a substitute
// for this fresh coherent authority capture. All checks after unlock are pure
// graph admission: no provider, CHECK truth, codec or output replay.
func (s *StatementScope) readmitProgramsAfterWait() error {
	if len(s.domainPreparedChecks) == 0 && len(s.routinePrograms) == 0 && len(s.indexBindings) == 0 {
		return nil
	}
	e := s.op.session.Engine()
	e.mu.Lock()
	err := s.validateLockedRelation(e)
	var auth AuthorizationView
	if err == nil {
		auth, err = s.writeAuthorizationLocked(e)
		if err == nil {
			auth.recaptureCatalog(e.privateCatalogLocked(s.owner.catalog))
		}
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	return s.readmitPreparedPrograms(auth)
}
