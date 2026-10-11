package engine

// Engine.mu is held. Reuse replacement's copy-on-change type/expression/value
// reownership without changing OIDs, object references, or old catalog roots.
func (e *Engine) reownStagedTypeGenerationsLocked(stage *CatalogStage) error {
	view := e.stageRead(stage)
	next := view.state
	if _, err := reownDomainTypes(next, nil); err != nil {
		return err
	}
	if err := reownCatalogScalarTypes(next, nil, nil); err != nil {
		return err
	}
	if err := e.reownCatalogExpressions(next, nil, true); err != nil {
		return err
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
		if relation, ok := descriptor.(*relationDescriptor); ok && relation.relationKind == tableRelation {
			old, _ := previous.(*relationDescriptor)
			columnsChanged := false
			for i, column := range relation.columns {
				columnsChanged = columnsChanged || old.columns[i].Usage.Type != column.Usage.Type
			}
			if columnsChanged {
				store := e.storage[address]
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
	if stage.rowShapes == nil {
		stage.rowShapes = make(map[ObjectAddress]catalogRowShape)
	}
	for address, replacement := range stores {
		if shape, ok := stage.rowShapes[address]; ok {
			shape.replacement = replacement
			stage.rowShapes[address] = shape
			continue
		}
		original := e.storage[address]
		stage.rowShapes[address] = catalogRowShape{original: original, replacement: replacement, observedLength: len(original.versions), observedEnds: original.ends, generation: original.generation}
	}
	return nil
}
