package index

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func RebindExclusionOperators(parent *catalog.Relation, definition *catalog.IndexDefinition) ([]sqltype.ExclusionOperator, error) {
	operators := make([]sqltype.ExclusionOperator, len(definition.Exclusion))
	for i, operator := range definition.Exclusion {
		usage, err := KeyUsage(parent, definition.Keys[i])
		if err != nil {
			return nil, err
		}
		operators[i], err = sqltype.BindExclusionOperator(usage, definition.Keys[i].Class, operator.Name)
		if err != nil {
			return nil, err
		}
	}
	return operators, nil
}

// Imported values and definitions must capture the destination type descriptors.
// The catalog replacement path invokes this after its existing expression remap.
func RebindImportedExclusions(next *catalog.State) error {
	for address, descriptor := range next.Descriptors() {
		parent, ok := descriptor.(*catalog.Relation)
		if !ok || len(parent.Constraints.Exclusion) == 0 {
			continue
		}
		var replacement *catalog.Relation
		for i, constraint := range parent.Constraints.Exclusion {
			index, ok := next.Relation(constraint.Index)
			if !ok || index.SQLIndex == nil {
				return fmt.Errorf("stuntgres: invalid imported exclusion attachment")
			}
			definition := index.SQLIndex
			changed := false
			for j, operator := range definition.Exclusion {
				usage, err := KeyUsage(parent, definition.Keys[j])
				if err != nil {
					return err
				}
				if !operator.MatchesUsage(usage) {
					changed = true
					break
				}
			}
			if !changed {
				continue
			}
			operators, err := RebindExclusionOperators(parent, definition)
			if err != nil {
				return err
			}
			if replacement == nil {
				copy := *parent
				copy.Constraints.Exclusion = slices.Clone(parent.Constraints.Exclusion)
				replacement = &copy
			}
			replacement.Constraints.Exclusion[i].Operators = operators
			indexCopy, definitionCopy := *index, *definition
			definitionCopy.Exclusion = operators
			indexCopy.SQLIndex = &definitionCopy
			next.SetDescriptor(index.Address(), &indexCopy)
		}
		if replacement != nil {
			next.SetDescriptor(address, replacement)
		}
	}
	return nil
}
