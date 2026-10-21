package index

import (
	"maps"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// Only a target with checked nonunique indexes needs a mixed insertion lane.
// Unique/exclusion bindings retain their existing ordered fast path otherwise.
type InsertionBinding struct {
	Index       *catalog.Relation
	Parent      *catalog.Relation
	preparation ExecutionPreparation
	Unique      *UniqueBoundKey
	Ordering    Ordering
	Hash        storage.HashBinding
}

func (r *WriteCandidate) MaterializeInsertion(bound *InsertionBinding, evaluation *exec.Context) error {
	if output, exists := r.Indexes[bound.Index.Address()]; exists {
		if !OutputMatches(output, bound.Index) {
			return ids.ErrCatalogReplaced
		}
		return nil
	}
	output, err := MaterializeIndexOutput(evaluation, &bound.preparation, bound.Parent, bound.Index, &bound.Ordering, &bound.Hash, r.Values)
	if err != nil {
		return err
	}
	if r.Indexes == nil {
		r.Indexes = make(map[ids.ObjectAddress]storage.IndexOutput)
	} else if r.SharedIndexes {
		r.Indexes = maps.Clone(r.Indexes)
	}
	r.SharedIndexes = false
	r.Indexes[bound.Index.Address()] = output
	return nil
}

type ProbePurpose uint8

const (
	ProbeRuntime ProbePurpose = iota
	ProbeTracked
	ProbeRevalidation
)

// Existing command-private insertion records own the consumed graph. Reuse it
// before discarding/rebinding a catalog-drifted binding; cooked values alone
// cannot certify the original call after a wait.
func (b *UniqueWriteBinding) ReadmitPrograms(auth exec.RoutineAccess) error {
	for i := range b.Keys {
		if err := b.Keys[i].Preparation.admission.Readmit(auth); err != nil {
			return err
		}
	}
	for i := range b.Insertion {
		if b.Insertion[i].Unique != nil {
			continue
		}
		if err := b.Insertion[i].preparation.admission.Readmit(auth); err != nil {
			return err
		}
	}
	return nil
}
