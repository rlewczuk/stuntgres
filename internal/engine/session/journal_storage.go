package session

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// RecordRowShapes records a catalog stage's heap replacements in c.Storage.
// A first change keeps the shape's observed predecessor; a later one keeps
// the journal's first predecessor and replaces only the latest private store.
// A new replacement clears truncation and materialized provenance, and a
// rewrite records its frontier and drops the snapshot. Each change carries
// the staged relation, else the base one. It takes no lock.
func (c *Journal) RecordRowShapes(shapes map[ids.ObjectAddress]storage.RowShape, staged *catalog.Candidate) {
	for a, rowShape := range shapes {
		shape, exists := c.Storage[a]
		if !exists {
			shape = StorageChange{Original: rowShape.Original, Root: rowShape.Root, Ends: rowShape.ObservedEnds, Generation: rowShape.Generation}
		}
		if shape.Replacement != rowShape.Replacement {
			shape.Truncated = false
			shape.MaterializedConcurrent, shape.MaterializedContinuous = false, false
		}
		shape.Replacement = rowShape.Replacement
		if rowShape.Rewritten {
			shape.Rewritten, shape.Frontier = true, rowShape.Frontier
			shape.Snapshot, shape.HasSnapshot = nil, false
		}
		shape.Relation, _ = staged.Get(a).(*catalog.Relation)
		if shape.Relation == nil {
			// An index-expression-only domain generation changes executable
			// index authority without changing its parent's column descriptor.
			shape.Relation = staged.Base.MustRelation(a)
		}
		c.Storage[a] = shape
	}
}

// MaterializedRefresh is a materialized view refresh a statement stages.
type MaterializedRefresh struct {
	Address              ids.ObjectAddress
	Predecessor, Storage *storage.Relation
	Ends                 *storage.DeletionRoot
	Generation           uint64
	Descriptor           *catalog.Relation
	Concurrent           bool
	// Committed reports whether Predecessor is the committed store.
	Committed bool
	// Frontier is the commit sequence the refreshed rows are rewritten at.
	Frontier ids.CommitSeq
}

// RecordMaterializedRefresh records a refresh in c.Storage. It keeps the
// first committed predecessor (or private CREATE absence) and replaces only
// the latest private root. Continuity holds while every refresh since the
// committed store is concurrent. It takes no lock.
func (c *Journal) RecordMaterializedRefresh(r MaterializedRefresh) {
	shape, exists := c.Storage[r.Address]
	if !exists {
		shape = StorageChange{Original: r.Predecessor, Root: r.Predecessor.Versions, Ends: r.Ends, Generation: r.Generation}
		shape.MaterializedContinuous = r.Concurrent && r.Committed
	} else {
		shape.MaterializedContinuous = shape.MaterializedContinuous && r.Concurrent
	}
	shape.MaterializedConcurrent = r.Concurrent
	shape.Replacement, shape.Relation = r.Storage, r.Descriptor
	shape.Truncated = false
	shape.Rewritten, shape.Frontier = true, r.Frontier
	shape.Snapshot, shape.HasSnapshot = nil, false
	c.Storage[r.Address] = shape
}
