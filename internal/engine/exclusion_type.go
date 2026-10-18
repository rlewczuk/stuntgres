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
	if len(parent.ExclusionConstraints) == 0 || len(transformations) == 0 {
		return parent, nil
	}
	var next *relationDescriptor
	for i, old := range parent.ExclusionConstraints {
		index, ok := stage.base.Relation(old.Index)
		if !ok {
			if current, exists := stage.descriptors[old.Index].(*relationDescriptor); exists && current.SQLIndex != nil {
				continue // New definitions already bind the completed TYPE shape.
			}
		}
		if !ok || index.SQLIndex == nil {
			return nil, fmt.Errorf("stuntgres: invalid TYPE exclusion index")
		}
		if !uniqueIndexTypeAffected(index.SQLIndex, transformations) {
			continue
		}
		if replacement, ok := stage.descriptors[old.Index].(*relationDescriptor); ok {
			index = replacement
		}
		oldParent := stage.base.MustRelation(parent.address)
		evolved, err := evolveIndex(index, oldParent, parent, transformations)
		if err != nil {
			return nil, err
		}
		if evolved == nil {
			return nil, fmt.Errorf("stuntgres: lost TYPE exclusion attachment")
		}
		if next == nil {
			copy := *parent
			copy.ExclusionConstraints = slices.Clone(parent.ExclusionConstraints)
			next = &copy
		}
		c := &next.ExclusionConstraints[i]
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
		definition := *evolved.SQLIndex
		definition.exclusion, err = rebindExclusionOperators(parent, &definition)
		if err != nil {
			return nil, err
		}
		c.Operators = definition.exclusion
		definition.constraint = exclusionBacking(c)
		definition.ready, definition.valid, definition.live = true, true, true
		copy.SQLIndex = &definition
		copy.Columns = slices.Clone(evolved.Columns)
		for j := range copy.Columns {
			copy.Columns[j].OriginTable = uint32(indexOID)
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
	for address, descriptor := range next.Descriptors() {
		parent, ok := descriptor.(*relationDescriptor)
		if !ok || len(parent.ExclusionConstraints) == 0 {
			continue
		}
		var replacement *relationDescriptor
		for i, constraint := range parent.ExclusionConstraints {
			index, ok := next.Relation(constraint.Index)
			if !ok || index.SQLIndex == nil {
				return fmt.Errorf("stuntgres: invalid imported exclusion attachment")
			}
			definition := index.SQLIndex
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
				copy.ExclusionConstraints = slices.Clone(parent.ExclusionConstraints)
				replacement = &copy
			}
			replacement.ExclusionConstraints[i].Operators = operators
			indexCopy, definitionCopy := *index, *definition
			definitionCopy.exclusion = operators
			indexCopy.SQLIndex = &definitionCopy
			next.setDescriptor(index.address, &indexCopy)
		}
		if replacement != nil {
			next.setDescriptor(address, replacement)
		}
	}
	return nil
}
