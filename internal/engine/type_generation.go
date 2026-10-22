package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
)

// Engine.mu is held. Reuse replacement's copy-on-change type/expression/value
// reownership without changing OIDs, object references, or old catalog roots.
func (e *Engine) reownStagedTypeGenerationsLocked(stage *CatalogStage) error {
	view := e.stageRead(stage)
	next := view.State()
	if _, err := typedef.ReownDomainTypes(next, nil, false); err != nil {
		return err
	}
	if err := reownCatalogScalarTypes(next, nil, nil); err != nil {
		return err
	}
	if err := stored.ReownExpressions(e.catalog.read(next), next, nil, true); err != nil {
		return err
	}
	// ALTER changes executable domain recipes, unlike mere retained-value
	// reownership. Every changed same-ID consumer must invalidate held analysis.
	if stage.domainAlter.AlterContext() != nil {
		for address, descriptor := range next.Descriptors() {
			previous := stage.Get(address)
			if previous == nil {
				previous = stage.Base.Get(address)
			}
			if previous == nil || previous == descriptor || descriptor.Revision() != previous.Revision() {
				continue
			}
			revision, err := stage.cascadeMetadataRevision(previous.Revision())
			if err != nil {
				return err
			}
			switch descriptor := descriptor.(type) {
			case *catalog.Type:
				copy := *descriptor
				copy.ObjectRevision = revision
				next.SetDescriptor(address, &copy)
			case *catalog.Relation:
				copy := *descriptor
				copy.ObjectRevision = revision
				next.SetDescriptor(address, &copy)
			}
		}
	}
	var stores map[ids.ObjectAddress]*storage.Relation
	for address, descriptor := range next.Descriptors() {
		previous := stage.Get(address)
		if previous == nil {
			previous = stage.Base.Get(address)
		}
		if descriptor == previous {
			continue
		}
		if relation, ok := descriptor.(*catalog.Relation); ok && (relation.RelationKind == catalog.TableRelation || relation.RelationKind == catalog.MaterializedViewRelation) {
			old, _ := previous.(*catalog.Relation)
			columnsChanged := false
			for i, column := range relation.Columns {
				columnsChanged = columnsChanged || old.Columns[i].Usage.Type != column.Usage.Type
			}
			if columnsChanged {
				store := e.stageStorageLocked(stage, address)
				if shape, ok := stage.rowShapes[address]; ok {
					store = shape.replacement
				}
				if store != nil {
					if stores == nil {
						stores = make(map[ids.ObjectAddress]*storage.Relation)
					}
					stores[address] = store
				}
			}
		}
		if stage.domainAlter.AlterContext() != nil {
			if index, ok := descriptor.(*catalog.Relation); ok && index.SQLIndex != nil {
				parent := index.SQLIndex.Parent
				if stores[parent] == nil {
					store := e.stageStorageLocked(stage, parent)
					if shape, ok := stage.rowShapes[parent]; ok {
						store = shape.replacement
					}
					if store != nil {
						if stores == nil {
							stores = make(map[ids.ObjectAddress]*storage.Relation)
						}
						stores[parent] = store
					}
				}
			}
		}
		if stage.Base.Get(address) == nil {
			stage.UpdateDescriptor(descriptor)
		} else if err := stage.replaceDescriptor(descriptor, false, false); err != nil {
			return err
		}
	}
	if len(stores) == 0 {
		return nil
	}
	if err := reownCatalogScalarTypes(next, stores, nil); err != nil {
		return err
	}
	if stage.domainAlter.AlterContext() != nil {
		if err := e.stageDomainIndexGenerationsLocked(stage, next, stores); err != nil {
			return err
		}
	}
	if stage.rowShapes == nil {
		stage.rowShapes = make(map[ids.ObjectAddress]catalogRowShape)
	}
	for address, replacement := range stores {
		if shape, ok := stage.rowShapes[address]; ok {
			shape.replacement = replacement
			stage.rowShapes[address] = shape
			continue
		}
		original := e.stageStorageLocked(stage, address)
		stage.rowShapes[address] = catalogRowShape{original: original, root: original.Versions, replacement: replacement, observedLength: len(original.Versions), observedEnds: original.Ends, generation: original.Generation}
	}
	return nil
}
