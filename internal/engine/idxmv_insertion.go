package engine

import (
	"maps"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// Only a target with checked nonunique indexes needs a mixed insertion lane.
// Unique/exclusion bindings retain their existing ordered fast path otherwise.
type indexInsertionBinding struct {
	index       *catalog.Relation
	parent      *catalog.Relation
	preparation indexExecutionPreparation
	unique      *uniqueBoundKey
	ordering    indexOrdering
	hash        storage.HashBinding
}

func (r *indexWriteCandidate) materializeInsertion(bound *indexInsertionBinding, evaluation *exec.Context) error {
	if output, exists := r.indexes[bound.index.Address()]; exists {
		if !indexOutputMatches(output, bound.index) {
			return ids.ErrCatalogReplaced
		}
		return nil
	}
	output, err := materializeIndexOutput(evaluation, &bound.preparation, bound.parent, bound.index, &bound.ordering, &bound.hash, r.values)
	if err != nil {
		return err
	}
	if r.indexes == nil {
		r.indexes = make(map[ids.ObjectAddress]storage.IndexOutput)
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

// Existing command-private insertion records own the consumed graph. Reuse it
// before discarding/rebinding a catalog-drifted binding; cooked values alone
// cannot certify the original call after a wait.
func (b *uniqueWriteBinding) readmitPrograms(auth exec.RoutineAccess) error {
	for i := range b.keys {
		if err := b.keys[i].preparation.admission.Readmit(auth); err != nil {
			return err
		}
	}
	for i := range b.insertion {
		if b.insertion[i].unique != nil {
			continue
		}
		if err := b.insertion[i].preparation.admission.Readmit(auth); err != nil {
			return err
		}
	}
	return nil
}
