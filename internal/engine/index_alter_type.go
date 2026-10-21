package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/sqltype"
)

// A TYPE action rebuilds each affected constraint/backing relation as one
// removal-and-create candidate, retaining the parent and stable attribute keys.
func (e *Engine) rebuildAlterUniquesLocked(stage *CatalogStage, parent *catalog.Relation, transformations []index.ColumnTransformation) (*catalog.Relation, error) {
	if len(transformations) == 0 {
		return parent, nil
	}
	var next *catalog.Relation
	for i, old := range parent.Constraints.Unique {
		idx, ok := stage.Base.Relation(old.Index)
		if !ok {
			// A TYPE-reminted standalone index was already rebuilt before its
			// USING INDEX attachment. Do not allocate another backing identity.
			if current, exists := stage.Get(old.Index).(*catalog.Relation); exists && current.SQLIndex != nil {
				continue
			}
			return nil, fmt.Errorf("stuntgres: invalid TYPE unique backing index")
		}
		if idx.SQLIndex == nil {
			return nil, fmt.Errorf("stuntgres: invalid TYPE unique backing index")
		}
		if current, exists := stage.Get(old.Index).(*catalog.Relation); exists && current.SQLIndex != nil {
			idx = current
		}
		if !index.UniqueIndexTypeAffected(idx.SQLIndex, transformations) {
			continue
		}
		if next == nil {
			clone := *parent
			clone.Constraints.Unique = slices.Clone(parent.Constraints.Unique)
			next = &clone
		}
		c := &next.Constraints.Unique[i]
		ref := e.catalog.Domain.Ref(stage.Base.Epoch(), old.Index)
		if err := stage.removeObjectLocked(ref, false); err != nil {
			return nil, err
		}
		var err error
		c.OID, err = e.catalog.allocateOIDLocked()
		if err != nil {
			return nil, err
		}
		id, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return nil, err
		}
		c.Index = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}
		clone := *idx
		clone.ObjectAddress = c.Index
		clone.ObjectRevision, err = e.catalog.revisionLocked()
		if err != nil {
			return nil, err
		}
		definition := *idx.SQLIndex
		definition.Keys = slices.Clone(idx.SQLIndex.Keys)
		definition.Constraint = index.UniqueBacking(c)
		clone.SQLIndex = &definition
		clone.Columns = slices.Clone(idx.Columns)
		for j, key := range definition.Keys {
			attribute, expression, err := index.CatalogKey(parent, key)
			if err != nil || expression != nil {
				return nil, fmt.Errorf("stuntgres: invalid TYPE unique key")
			}
			slot := slices.Index(parent.Attributes, attribute)
			clone.Columns[j].Name = parent.Columns[slot].Name
			clone.Columns[j].Usage = parent.Columns[slot].Usage
			class, ok := sqltype.DefaultIndexClass(clone.Columns[j].Usage.Type)
			if !ok {
				return nil, index.DefaultClassError(clone.Columns[j].Usage.Type.DiagnosticName(), catalog.BtreeMethod)
			}
			definition.Keys[j].Class = class
			clone.Columns[j].OriginTable = uint32(id)
		}
		for j, attribute := range definition.Include {
			slot := slices.Index(parent.Attributes, attribute)
			column := &clone.Columns[len(definition.Keys)+j]
			column.Name, column.Usage, column.OriginTable = parent.Columns[slot].Name, parent.Columns[slot].Usage, uint32(id)
		}
		for j, column := range clone.Columns {
			if column.Name != idx.Columns[j].Name {
				clone.Index = make(map[string]int, len(clone.Columns))
				for slot, renamed := range clone.Columns {
					clone.Index[renamed.Name] = slot
				}
				break
			}
		}
		if err := stage.Create(&clone); err != nil {
			return nil, err
		}
	}
	if next == nil {
		return e.rebuildAlterNotNullsLocked(stage, parent, transformations)
	}
	if err := stage.Replace(next); err != nil {
		return nil, err
	}
	return e.rebuildAlterNotNullsLocked(stage, next, transformations)
}
