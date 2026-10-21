package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func (e *Engine) rebuildAlterExclusionsLocked(stage *CatalogStage, parent *catalog.Relation, transformations []columnTransformation) (*catalog.Relation, error) {
	if len(parent.Constraints.Exclusion) == 0 || len(transformations) == 0 {
		return parent, nil
	}
	var next *catalog.Relation
	for i, old := range parent.Constraints.Exclusion {
		index, ok := stage.Base.Relation(old.Index)
		if !ok {
			if current, exists := stage.Get(old.Index).(*catalog.Relation); exists && current.SQLIndex != nil {
				continue // New definitions already bind the completed TYPE shape.
			}
		}
		if !ok || index.SQLIndex == nil {
			return nil, fmt.Errorf("stuntgres: invalid TYPE exclusion index")
		}
		if !UniqueIndexTypeAffected(index.SQLIndex, transformations) {
			continue
		}
		if replacement, ok := stage.Get(old.Index).(*catalog.Relation); ok {
			index = replacement
		}
		oldParent := stage.Base.MustRelation(parent.Address())
		evolved, err := EvolveIndex(index, oldParent, parent, transformations)
		if err != nil {
			return nil, err
		}
		if evolved == nil {
			return nil, fmt.Errorf("stuntgres: lost TYPE exclusion attachment")
		}
		if next == nil {
			copy := *parent
			copy.Constraints.Exclusion = slices.Clone(parent.Constraints.Exclusion)
			next = &copy
		}
		c := &next.Constraints.Exclusion[i]
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
		definition.Exclusion, err = RebindExclusionOperators(parent, &definition)
		if err != nil {
			return nil, err
		}
		c.Operators = definition.Exclusion
		definition.Constraint = ExclusionBacking(c)
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
