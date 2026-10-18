package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func rebindExclusionOperators(parent *catalog.Relation, definition *catalog.IndexDefinition) ([]sqltype.ExclusionOperator, error) {
	operators := make([]sqltype.ExclusionOperator, len(definition.Exclusion))
	for i, operator := range definition.Exclusion {
		usage, err := indexKeyUsage(parent, definition.Keys[i])
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
func (e *Engine) rebuildAlterExclusionsLocked(stage *CatalogStage, parent *catalog.Relation, transformations []columnTransformation) (*catalog.Relation, error) {
	if len(parent.ExclusionConstraints) == 0 || len(transformations) == 0 {
		return parent, nil
	}
	var next *catalog.Relation
	for i, old := range parent.ExclusionConstraints {
		index, ok := stage.Base.Relation(old.Index)
		if !ok {
			if current, exists := stage.Descriptors[old.Index].(*catalog.Relation); exists && current.SQLIndex != nil {
				continue // New definitions already bind the completed TYPE shape.
			}
		}
		if !ok || index.SQLIndex == nil {
			return nil, fmt.Errorf("stuntgres: invalid TYPE exclusion index")
		}
		if !uniqueIndexTypeAffected(index.SQLIndex, transformations) {
			continue
		}
		if replacement, ok := stage.Descriptors[old.Index].(*catalog.Relation); ok {
			index = replacement
		}
		oldParent := stage.Base.MustRelation(parent.Address())
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
		if err := stage.removeObjectLocked(e.catalog.Domain.Ref(stage.Base.Epoch(), old.Index), false); err != nil {
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
		copy.ObjectAddress = c.Index
		copy.ObjectRevision, err = e.catalog.revisionLocked()
		if err != nil {
			return nil, err
		}
		definition := *evolved.SQLIndex
		definition.Exclusion, err = rebindExclusionOperators(parent, &definition)
		if err != nil {
			return nil, err
		}
		c.Operators = definition.Exclusion
		definition.Constraint = exclusionBacking(c)
		definition.Ready, definition.Valid, definition.Live = true, true, true
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
		parent, ok := descriptor.(*catalog.Relation)
		if !ok || len(parent.ExclusionConstraints) == 0 {
			continue
		}
		var replacement *catalog.Relation
		for i, constraint := range parent.ExclusionConstraints {
			index, ok := next.Relation(constraint.Index)
			if !ok || index.SQLIndex == nil {
				return fmt.Errorf("stuntgres: invalid imported exclusion attachment")
			}
			definition := index.SQLIndex
			changed := false
			for j, operator := range definition.Exclusion {
				usage, err := indexKeyUsage(parent, definition.Keys[j])
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
