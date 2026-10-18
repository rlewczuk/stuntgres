package engine

import (
	"iter"
	"slices"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

// Admission precedes dependency discovery. Repeat after grants so a descendant
// gaining a child while its AX grant was queued becomes part of the same drop.
func lockInheritanceDropClosure(scope *StatementScope, stage *CatalogStage, roots []ids.ObjectAddress) error {
	provider := stageDropProvider(nil, stage, scope, nil, nil)
	catalog, err := admitInheritanceDropClosure(scope, stage.base, roots, provider.removalDescriptors())
	stage.base = catalog
	return err
}

// admitInheritanceDropClosure reconciles actual captures without a stage. The
// incumbent execution wrapper alone installs its returned catalog as stage base.
func admitInheritanceDropClosure(scope *StatementScope, catalog CatalogRead, roots []ids.ObjectAddress, removals iter.Seq2[ids.ObjectAddress, Descriptor]) (CatalogRead, error) {
	if scope == nil {
		return catalog, nil
	}
	// Ordinary DROP owns its existing admission policy. In particular low-level
	// callers need not have acquired a root grant; do not introduce one unless
	// an inheritance edge actually requires fencing the closure.
	participates := false
	for _, root := range roots {
		if table, ok := catalog.Relation(root); ok && root.SubID == 0 && inheritanceParticipant(catalog, table) {
			participates = true
			break
		}
	}
	if !participates {
		return catalog, nil
	}
	locked := make(map[ids.ObjectAddress]bool)
	for {
		var tables []ids.ObjectAddress
		seen := make(map[ids.ObjectAddress]bool)
		var visit func(ids.ObjectAddress)
		visit = func(address ids.ObjectAddress) {
			if seen[address] {
				return
			}
			seen[address] = true
			table, ok := catalog.Relation(address)
			if !ok || table.relationKind != tableRelation || address.SubID != 0 {
				return
			}
			tables = append(tables, address)
			for _, child := range inheritanceChildren(catalog, address) {
				visit(child.address)
			}
		}
		for _, root := range roots {
			visit(root)
		}
		slices.SortFunc(tables, func(a, b ids.ObjectAddress) int {
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
			if err := scope.acquireSequence(catalog.owner.domain.Ref(catalog.Epoch(), address), lock.AccessExclusive, true); err != nil {
				return catalog, err
			}
			locked[address] = true
			grew = true
		}
		if !grew {
			return catalog, nil
		}
		// Private TEMP catalogs are owned by the same serialized session. Their
		// staged objects must not be replaced by the global generation.
		private := false
		for _, address := range tables {
			table := catalog.MustRelation(address)
			if catalog.MustNamespace(table.namespace).temporary != (ids.SessionToken{}) {
				private = true
				break
			}
		}
		if private {
			return catalog, nil
		}
		current := scope.op.session.Engine().CatalogRead()
		if current.Epoch() != catalog.Epoch() {
			return catalog, ids.ErrCatalogReplaced
		}
		for address := range locked {
			if current.Get(address) != catalog.Get(address) {
				return catalog, CatalogConflict{}
			}
		}
		for address, descriptor := range removals {
			if current.Get(address) != descriptor {
				return catalog, CatalogConflict{}
			}
		}
		catalog = current
	}
}
