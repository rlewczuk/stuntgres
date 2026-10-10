package engine

import "slices"

// Admission precedes dependency discovery. Repeat after grants so a descendant
// gaining a child while its AX grant was queued becomes part of the same drop.
func lockInheritanceDropClosure(scope *StatementScope, stage *CatalogStage, roots []ObjectAddress) error {
	if scope == nil {
		return nil
	}
	// Ordinary DROP owns its existing admission policy. In particular low-level
	// callers need not have acquired a root grant; do not introduce one unless
	// an inheritance edge actually requires fencing the closure.
	participates := false
	for _, root := range roots {
		if table, ok := stage.base.state.descriptors[root].(*relationDescriptor); ok && root.SubID == 0 && inheritanceParticipant(stage.base, table) {
			participates = true
			break
		}
	}
	if !participates {
		return nil
	}
	locked := make(map[ObjectAddress]bool)
	for {
		var tables []ObjectAddress
		seen := make(map[ObjectAddress]bool)
		var visit func(ObjectAddress)
		visit = func(address ObjectAddress) {
			if seen[address] {
				return
			}
			seen[address] = true
			table, ok := stage.base.state.descriptors[address].(*relationDescriptor)
			if !ok || table.relationKind != tableRelation || address.SubID != 0 {
				return
			}
			tables = append(tables, address)
			for _, child := range inheritanceChildren(stage.base, address) {
				visit(child.address)
			}
		}
		for _, root := range roots {
			visit(root)
		}
		slices.SortFunc(tables, func(a, b ObjectAddress) int {
			if a.ObjectID < b.ObjectID {
				return -1
			}
			if a.ObjectID > b.ObjectID {
				return 1
			}
			return 0
		})
		grew := false
		for _, address := range tables {
			if locked[address] {
				continue
			}
			if err := scope.acquireSequence(ObjectRef{owner: stage.owner.owner, Epoch: stage.base.Epoch(), Address: address}, accessExclusive, true); err != nil {
				return err
			}
			locked[address] = true
			grew = true
		}
		if !grew {
			return nil
		}
		// Private TEMP catalogs are owned by the same serialized session. Their
		// staged objects must not be replaced by the global generation.
		private := false
		for _, address := range tables {
			table := stage.base.state.descriptors[address].(*relationDescriptor)
			if stage.base.state.descriptors[table.namespace].(*namespaceDescriptor).temporary != (SessionToken{}) {
				private = true
				break
			}
		}
		if private {
			return nil
		}
		current := stage.owner.CatalogRead()
		if current.Epoch() != stage.base.Epoch() {
			return ErrCatalogReplaced
		}
		for address := range locked {
			if current.state.descriptors[address] != stage.base.state.descriptors[address] {
				return CatalogConflict{}
			}
		}
		for address, removal := range stage.removals {
			if current.state.descriptors[address] != removal.descriptor {
				return CatalogConflict{}
			}
		}
		stage.base = current
	}
}
