package engine

import "maps"

// Only a target with checked nonunique indexes needs a mixed insertion lane.
// Unique/exclusion bindings retain their existing ordered fast path otherwise.
type indexInsertionBinding struct {
	index    *relationDescriptor
	unique   *uniqueBoundKey
	ordering indexOrdering
	hash     hashIndexBinding
}

func (r *indexWriteCandidate) materializeInsertion(bound *indexInsertionBinding, evaluation *EvalContext) error {
	if output, exists := r.indexes[bound.index.address]; exists {
		if !output.matches(bound.index) {
			return ErrCatalogReplaced
		}
		return nil
	}
	output, err := materializeIndexOutput(evaluation, bound.index, bound.ordering, bound.hash, r.values)
	if err != nil {
		return err
	}
	if r.indexes == nil {
		r.indexes = make(map[ObjectAddress]indexRowOutput)
	} else if r.sharedIndexes {
		r.indexes = maps.Clone(r.indexes)
	}
	r.sharedIndexes = false
	r.indexes[bound.index.address] = output
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
func (s *StatementScope) revalidateIndexRows(ref ObjectRef, old []rowVersion, batch *indexWriteBatch) error {
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
			return ErrCatalogReplaced
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
		s.existingIndexOutputs = make(map[ObjectAddress]map[indexRowIdentity]indexRowOutput)
	}
	outputs := s.existingIndexOutputs[index.address]
	if outputs == nil {
		outputs = make(map[indexRowIdentity]indexRowOutput)
		s.existingIndexOutputs[index.address] = outputs
	}
	outputs[indexRowIdentity{row: row.row, version: row.version}] = output
}
