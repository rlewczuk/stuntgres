package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func rebindExclusionOperators(parent *relationDescriptor, definition *indexDefinition) ([]sqltype.ExclusionOperator, error) {
	operators := make([]sqltype.ExclusionOperator, len(definition.exclusion))
	for i, operator := range definition.exclusion {
		usage, err := indexKeyUsage(parent, definition.keys[i])
		if err != nil {
			return nil, err
		}
		operators[i], err = sqltype.BindExclusionOperator(usage, definition.keys[i].class, operator.Name)
		if err != nil {
			return nil, err
		}
	}
	return operators, nil
}
func (e *Engine) rebuildAlterExclusionsLocked(stage *CatalogStage, parent *relationDescriptor, transformations []columnTransformation) (*relationDescriptor, error) {
	if len(parent.exclusionConstraints) == 0 || len(transformations) == 0 {
		return parent, nil
	}
	var next *relationDescriptor
	for i, old := range parent.exclusionConstraints {
		index, ok := stage.base.state.descriptors[old.Index].(*relationDescriptor)
		if !ok {
			if current, exists := stage.descriptors[old.Index].(*relationDescriptor); exists && current.sqlIndex != nil {
				continue // New definitions already bind the completed TYPE shape.
			}
		}
		if !ok || index.sqlIndex == nil {
			return nil, fmt.Errorf("stuntgres: invalid TYPE exclusion index")
		}
		if !uniqueIndexTypeAffected(index.sqlIndex, transformations) {
			continue
		}
		if replacement, ok := stage.descriptors[old.Index].(*relationDescriptor); ok {
			index = replacement
		}
		oldParent := stage.base.state.descriptors[parent.address].(*relationDescriptor)
		evolved, err := evolveIndex(index, oldParent, parent, transformations)
		if err != nil {
			return nil, err
		}
		if evolved == nil {
			return nil, fmt.Errorf("stuntgres: lost TYPE exclusion attachment")
		}
		if next == nil {
			copy := *parent
			copy.exclusionConstraints = slices.Clone(parent.exclusionConstraints)
			next = &copy
		}
		c := &next.exclusionConstraints[i]
		if err := stage.removeObjectLocked(e.catalog.domain.Ref(stage.base.Epoch(), old.Index), false); err != nil {
			return nil, err
		}
		c.OID, err = e.catalog.allocateOIDLocked()
		if err != nil {
			return nil, err
		}
		indexOID, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return nil, err
		}
		c.Index = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: indexOID}
		copy := *evolved
		copy.address = c.Index
		copy.revision, err = e.catalog.revisionLocked()
		if err != nil {
			return nil, err
		}
		definition := *evolved.sqlIndex
		definition.exclusion, err = rebindExclusionOperators(parent, &definition)
		if err != nil {
			return nil, err
		}
		c.Operators = definition.exclusion
		definition.constraint = exclusionBacking(c)
		definition.ready, definition.valid, definition.live = true, true, true
		copy.sqlIndex = &definition
		copy.columns = slices.Clone(evolved.columns)
		for j := range copy.columns {
			copy.columns[j].OriginTable = uint32(indexOID)
		}
		if err := stage.Create(&copy); err != nil {
			return nil, err
		}
	}
	if next == nil {
		return parent, nil
	}
	if err := stage.Replace(next); err != nil {
		return nil, err
	}
	return next, nil
}

// Imported values and definitions must capture the destination type descriptors.
// The catalog replacement path invokes this after its existing expression remap.
func rebindImportedExclusions(next *catalogState) error {
	for address, descriptor := range next.descriptors {
		parent, ok := descriptor.(*relationDescriptor)
		if !ok || len(parent.exclusionConstraints) == 0 {
			continue
		}
		var replacement *relationDescriptor
		for i, constraint := range parent.exclusionConstraints {
			index, ok := next.descriptors[constraint.Index].(*relationDescriptor)
			if !ok || index.sqlIndex == nil {
				return fmt.Errorf("stuntgres: invalid imported exclusion attachment")
			}
			definition := index.sqlIndex
			changed := false
			for j, operator := range definition.exclusion {
				usage, err := indexKeyUsage(parent, definition.keys[j])
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
			operators, err := rebindExclusionOperators(parent, definition)
			if err != nil {
				return err
			}
			if replacement == nil {
				copy := *parent
				copy.exclusionConstraints = slices.Clone(parent.exclusionConstraints)
				replacement = &copy
			}
			replacement.exclusionConstraints[i].Operators = operators
			indexCopy, definitionCopy := *index, *definition
			definitionCopy.exclusion = operators
			indexCopy.sqlIndex = &definitionCopy
			next.descriptors[index.address] = &indexCopy
		}
		if replacement != nil {
			next.descriptors[address] = replacement
		}
	}
	return nil
}
