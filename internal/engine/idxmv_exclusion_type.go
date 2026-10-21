package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

// RebuildAlterExclusions rebuilds each EXCLUDE constraint and backing index
// an ALTER COLUMN TYPE affects as one removal and create, with new
// identities, evolved keys and rebound operators.
func RebuildAlterExclusions(h index.Host, parent *catalog.Relation, transformations []index.ColumnTransformation) (*catalog.Relation, error) {
	if len(parent.Constraints.Exclusion) == 0 || len(transformations) == 0 {
		return parent, nil
	}
	var next *catalog.Relation
	for i, old := range parent.Constraints.Exclusion {
		idx, ok := h.StageBase().Relation(old.Index)
		if !ok {
			if current, exists := h.Stage().Get(old.Index).(*catalog.Relation); exists && current.SQLIndex != nil {
				continue // New definitions already bind the completed TYPE shape.
			}
		}
		if !ok || idx.SQLIndex == nil {
			return nil, fmt.Errorf("stuntgres: invalid TYPE exclusion index")
		}
		if !index.UniqueIndexTypeAffected(idx.SQLIndex, transformations) {
			continue
		}
		if replacement, ok := h.Stage().Get(old.Index).(*catalog.Relation); ok {
			idx = replacement
		}
		oldParent := h.StageBase().MustRelation(parent.Address())
		evolved, err := index.EvolveIndex(idx, oldParent, parent, transformations)
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
		if err := h.RemoveObject(stageRef(h, old.Index), false); err != nil {
			return nil, err
		}
		c.OID, err = h.AllocateOID()
		if err != nil {
			return nil, err
		}
		indexOID, err := h.AllocateOID()
		if err != nil {
			return nil, err
		}
		c.Index = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: indexOID}
		copy := *evolved
		copy.ObjectAddress = c.Index
		copy.ObjectRevision, err = h.Revision()
		if err != nil {
			return nil, err
		}
		definition := *evolved.SQLIndex
		definition.Exclusion, err = index.RebindExclusionOperators(parent, &definition)
		if err != nil {
			return nil, err
		}
		c.Operators = definition.Exclusion
		definition.Constraint = index.ExclusionBacking(c)
		definition.Ready, definition.Valid, definition.Live = true, true, true
		copy.SQLIndex = &definition
		copy.Columns = slices.Clone(evolved.Columns)
		for j := range copy.Columns {
			copy.Columns[j].OriginTable = uint32(indexOID)
		}
		if err := h.Stage().Create(&copy); err != nil {
			return nil, err
		}
	}
	if next == nil {
		return parent, nil
	}
	if err := h.Stage().Replace(next); err != nil {
		return nil, err
	}
	return next, nil
}
