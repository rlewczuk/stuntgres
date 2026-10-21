package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
)

// AttributeRankCandidate returns ref's table with the declared array rank of
// column set to rank; nil when it already has it. Only an array column may
// take a nonzero rank.
func AttributeRankCandidate(view catalog.Read, ref ids.ObjectRef, column string, rank int16) (*catalog.Relation, error) {
	old, ok := view.Relation(ref.Address)
	if !ok || old.RelationKind != catalog.TableRelation {
		return nil, sqlError("0A000", table.RestoreAttributeRankBoundary)
	}
	slot, exists := old.Index[column]
	if !exists {
		return nil, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, column, old.Name()))
	}
	if rank != 0 && !old.Columns[slot].Usage.Type.IsArray() {
		return nil, sqlError("0A000", table.RestoreAttributeRankBoundary)
	}
	if old.Columns[slot].DeclaredDimensions == rank {
		return nil, nil
	}
	if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return nil, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *old
	next.ObjectRevision++
	next.Columns = slices.Clone(old.Columns)
	next.Columns[slot].DeclaredDimensions = rank
	return &next, nil
}
