package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/stored"
)

func physicalSameAttributes(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i, attribute := range a {
		if !slices.Contains(b, attribute) || slices.Contains(a[:i], attribute) {
			return false
		}
	}
	return true
}

func physicalPermutedPrimary(table *catalog.Relation) *catalog.UniqueConstraint {
	if len(table.Constraints.PhysicalNotNull) == 0 {
		return nil
	}
	for i := range table.Constraints.Unique {
		primary := &table.Constraints.Unique[i]
		if !primary.Primary {
			continue
		}
		for _, foreign := range table.Constraints.ForeignKeys {
			if !foreign.NotValid && foreign.Target == table.Address() && foreign.Index == primary.Index && physicalSameAttributes(foreign.Referenced, primary.Attributes) && !slices.Equal(foreign.Referenced, primary.Attributes) {
				return primary
			}
		}
	}
	return nil
}

// Reuse the existing eligible-index predicate. Only real providers competing
// with a captured permuted self-FK's primary are ordered after that FK.
func physicalCompetingIndexSource(view catalog.Read, index *catalog.Relation) (*catalog.Relation, bool, error) {
	if index == nil || index.SQLIndex == nil {
		return nil, false, nil
	}
	parent, ok := view.Relation(index.SQLIndex.Parent)
	if !ok {
		return nil, false, nil
	}
	primary := physicalPermutedPrimary(parent)
	if primary == nil || primary.Index == index.Address() {
		return nil, false, nil
	}
	eligible, err := foreignKeyIndexEligible(parent, index, primary.Attributes)
	if err != nil || !eligible {
		return nil, false, err
	}
	source, ok := view.Relation(primary.Index)
	if !ok {
		return nil, false, fmt.Errorf("stuntgres: missing physical self-FK primary provider")
	}
	return source, true, nil
}

func emitPhysicalCompetingUniques(view catalog.Read, table *catalog.Relation, owner string, separate bool, emit func(string) error, setOwner func(string) error) error {
	primary := physicalPermutedPrimary(table)
	if primary == nil {
		return nil
	}
	primaryIndex := view.MustRelation(primary.Index)
	if index.PartitionReplaySeparateConstraint(view, primaryIndex) != separate {
		return nil
	}
	qualified := catalog.QuoteIdentifier(view.Get(table.Namespace()).Name()) + "." + catalog.QuoteIdentifier(table.Name())
	for i := range table.Constraints.Unique {
		constraint := &table.Constraints.Unique[i]
		idx := view.MustRelation(constraint.Index)
		if index.PartitionReplaySeparateConstraint(view, idx) {
			continue
		}
		_, deferred, err := physicalCompetingIndexSource(view, idx)
		if err != nil {
			return err
		}
		if !deferred {
			continue
		}
		text, err := (stored.Deparser{View: view}).UniqueConstraintSQL(table, constraint)
		if err != nil {
			return err
		}
		if err := setOwner(owner); err != nil {
			return err
		}
		if err := emit("ALTER TABLE ONLY " + qualified + " ADD CONSTRAINT " + catalog.QuoteIdentifier(constraint.Name) + " " + text + ";\n"); err != nil {
			return err
		}
	}
	return nil
}
