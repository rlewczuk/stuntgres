package engine

// Engine.mu is held. Reuse replacement's copy-on-change type/expression/value
// reownership without changing OIDs, object references, or old catalog roots.
func (e *Engine) reownStagedTypeGenerationsLocked(stage *CatalogStage) error {
	view := e.stageRead(stage)
	next := view.state
	if _, err := reownDomainTypes(next, nil, false); err != nil {
		return err
	}
	if err := reownCatalogScalarTypes(next, nil, nil); err != nil {
		return err
	}
	if err := e.reownCatalogExpressions(next, nil, true); err != nil {
		return err
	}
	// ALTER changes executable domain recipes, unlike mere retained-value
	// reownership. Every changed same-ID consumer must invalidate held analysis.
	if stage.domainAlter != nil {
		for address, descriptor := range next.descriptors {
			previous := stage.descriptors[address]
			if previous == nil {
				previous = stage.base.state.descriptors[address]
			}
			if previous == nil || previous == descriptor || descriptor.Revision() != previous.Revision() {
				continue
			}
			revision, err := stage.cascadeMetadataRevision(previous.Revision())
			if err != nil {
				return err
			}
			switch descriptor := descriptor.(type) {
			case *typeDescriptor:
				copy := *descriptor
				copy.revision = revision
				next.descriptors[address] = &copy
			case *relationDescriptor:
				copy := *descriptor
				copy.revision = revision
				next.descriptors[address] = &copy
			}
		}
	}
	var stores map[ObjectAddress]*relationStorage
	for address, descriptor := range next.descriptors {
		previous := stage.descriptors[address]
		if previous == nil {
			previous = stage.base.state.descriptors[address]
		}
		if descriptor == previous {
			continue
		}
		if relation, ok := descriptor.(*relationDescriptor); ok && (relation.relationKind == tableRelation || relation.relationKind == materializedViewRelation) {
			old, _ := previous.(*relationDescriptor)
			columnsChanged := false
			for i, column := range relation.columns {
				columnsChanged = columnsChanged || old.columns[i].Usage.Type != column.Usage.Type
			}
			if columnsChanged {
				store := e.stageStorageLocked(stage, address)
				if shape, ok := stage.rowShapes[address]; ok {
					store = shape.replacement
				}
				if store != nil {
					if stores == nil {
						stores = make(map[ObjectAddress]*relationStorage)
					}
					stores[address] = store
				}
			}
		}
		if stage.domainAlter != nil {
			if index, ok := descriptor.(*relationDescriptor); ok && index.sqlIndex != nil {
				parent := index.sqlIndex.parent
				if stores[parent] == nil {
					store := e.stageStorageLocked(stage, parent)
					if shape, ok := stage.rowShapes[parent]; ok {
						store = shape.replacement
					}
					if store != nil {
						if stores == nil {
							stores = make(map[ObjectAddress]*relationStorage)
						}
						stores[parent] = store
					}
				}
			}
		}
		if stage.base.state.descriptors[address] == nil {
			stage.descriptors[address] = descriptor
		} else if err := stage.replaceDescriptor(descriptor, false); err != nil {
			return err
		}
	}
	if len(stores) == 0 {
		return nil
	}
	if err := reownCatalogScalarTypes(next, stores, nil); err != nil {
		return err
	}
	if stage.domainAlter != nil {
		if err := e.stageDomainIndexGenerationsLocked(stage, next, stores); err != nil {
			return err
		}
	}
	if stage.rowShapes == nil {
		stage.rowShapes = make(map[ObjectAddress]catalogRowShape)
	}
	for address, replacement := range stores {
		if shape, ok := stage.rowShapes[address]; ok {
			shape.replacement = replacement
			stage.rowShapes[address] = shape
			continue
		}
		original := e.stageStorageLocked(stage, address)
		stage.rowShapes[address] = catalogRowShape{original: original, root: original.versions, replacement: replacement, observedLength: len(original.versions), observedEnds: original.ends, generation: original.generation}
	}
	return nil
}
