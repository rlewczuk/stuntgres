package core

import (
	"maps"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
)

// Engine.mu is held. Reuse replacement's copy-on-change type/expression/value
// reownership without changing OIDs, object references, or old catalog roots.
func (e *Engine) ReownStagedTypeGenerationsLocked(stage *CatalogStage) error {
	view := e.StageRead(stage)
	next := view.State()
	if _, err := typedef.ReownDomainTypes(next, nil, false, routine.RangeTypeRebinder(next, nil)); err != nil {
		return err
	}
	// Installed text codecs reject ordinary same-OID generation Retag.
	// Authorize only the exact published source pointer of a changed catalog-owned role,
	// using the existing transient scalar-import seam for nested values/recipes.
	imports := next.ScalarImports()
	authorized := false
	for address, descriptor := range next.Descriptors() {
		typ, ok := descriptor.(*catalog.Type)
		if !ok || !typ.SQLType.IsHstore() && !typ.SQLType.IsLquery() && !typ.SQLType.IsLtree() && !typ.SQLType.IsLtxtquery() && !typ.SQLType.IsCitext() && !typ.SQLType.IsEan13() && !typ.SQLType.IsIsbn() && !typ.SQLType.IsIsbn13() && !typ.SQLType.IsIsmn() && !typ.SQLType.IsIsmn13() && !typ.SQLType.IsIssn() && !typ.SQLType.IsIssn13() && !typ.SQLType.IsUpc() {
			continue
		}
		old, exists := stage.Base.Type(address)
		if !exists || !(old.SQLType.IsHstore() && typ.SQLType.IsHstore() || old.SQLType.IsLquery() && typ.SQLType.IsLquery() || old.SQLType.IsLtree() && typ.SQLType.IsLtree() || old.SQLType.IsLtxtquery() && typ.SQLType.IsLtxtquery() || old.SQLType.IsCitext() && typ.SQLType.IsCitext() || old.SQLType.IsEan13() && typ.SQLType.IsEan13() || old.SQLType.IsIsbn() && typ.SQLType.IsIsbn() || old.SQLType.IsIsbn13() && typ.SQLType.IsIsbn13() || old.SQLType.IsIsmn() && typ.SQLType.IsIsmn() || old.SQLType.IsIsmn13() && typ.SQLType.IsIsmn13() || old.SQLType.IsIssn() && typ.SQLType.IsIssn() || old.SQLType.IsIssn13() && typ.SQLType.IsIssn13() || old.SQLType.IsUpc() && typ.SQLType.IsUpc()) || old.SQLType == typ.SQLType || old.Extension != typ.Extension || old.Array != typ.Array {
			continue
		}
		if !authorized {
			next.SetScalarImports(maps.Clone(imports))
			authorized = true
		}
		next.SetScalarImport(old.SQLType, typ.SQLType)
	}
	defer next.SetScalarImports(imports)
	if err := stored.ReownScalarTypes(next, nil, nil); err != nil {
		return err
	}
	if err := stored.ReownExpressions(e.Catalog.Read(next), next, nil, true, nil); err != nil {
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
			if columnsChanged && !stage.deferredAlterRows[address] {
				store := e.StageStorageLocked(stage, address)
				if shape, ok := stage.RowShapes[address]; ok {
					store = shape.Replacement
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
					store := e.StageStorageLocked(stage, parent)
					if shape, ok := stage.RowShapes[parent]; ok {
						store = shape.Replacement
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
		} else if err := stage.ReplaceDescriptor(descriptor, false, false); err != nil {
			return err
		}
	}
	if len(stores) == 0 {
		return nil
	}
	if err := stored.ReownScalarTypes(next, stores, nil); err != nil {
		return err
	}
	if stage.domainAlter.AlterContext() != nil {
		if err := e.stageDomainIndexGenerationsLocked(stage, next, stores); err != nil {
			return err
		}
	}
	if stage.RowShapes == nil {
		stage.RowShapes = make(map[ids.ObjectAddress]storage.RowShape)
	}
	for address, replacement := range stores {
		if shape, ok := stage.RowShapes[address]; ok {
			shape.Replacement = replacement
			stage.RowShapes[address] = shape
			continue
		}
		original := e.StageStorageLocked(stage, address)
		stage.RowShapes[address] = storage.RowShape{Original: original, Root: original.Versions, Replacement: replacement, ObservedLength: len(original.Versions), ObservedEnds: original.Ends, Generation: original.Generation}
	}
	return nil
}
