package engine

import (
	"iter"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

// Admission precedes dependency discovery. Repeat after grants so a descendant
// gaining a child while its AX grant was queued becomes part of the same drop.
func lockInheritanceDropClosure(scope *StatementScope, stage *CatalogStage, roots []ids.ObjectAddress) error {
	provider := stageDropProvider(nil, stage, scope, nil, nil)
	catalog, err := admitInheritanceDropClosure(scope, stage.Base, roots, provider.removalDescriptors())
	stage.Base = catalog
	return err
}

// admitInheritanceDropClosure reconciles actual captures without a stage. The
// incumbent execution wrapper alone installs its returned catalog as stage base.
func admitInheritanceDropClosure(scope *StatementScope, view CatalogRead, roots []ids.ObjectAddress, removals iter.Seq2[ids.ObjectAddress, Descriptor]) (CatalogRead, error) {
	if scope == nil {
		return view, nil
	}
	// Ordinary DROP owns its existing admission policy. In particular low-level
	// callers need not have acquired a root grant; do not introduce one unless
	// an inheritance edge actually requires fencing the closure.
	participates := false
	for _, root := range roots {
		if table, ok := view.Relation(root); ok && root.SubID == 0 && inheritanceParticipant(view, table) {
			participates = true
			break
		}
	}
	if !participates {
		return view, nil
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
			table, ok := view.Relation(address)
			if !ok || table.RelationKind != catalog.TableRelation || address.SubID != 0 {
				return
			}
			tables = append(tables, address)
			for _, child := range inheritanceChildren(view, address) {
				visit(child.Address())
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
			if err := scope.acquireSequence(view.Owner().Domain.Ref(view.Epoch(), address), lock.AccessExclusive, true); err != nil {
				return view, err
			}
			locked[address] = true
			grew = true
		}
		if !grew {
			return view, nil
		}
		// Private TEMP catalogs are owned by the same serialized session. Their
		// staged objects must not be replaced by the global generation.
		private := false
		for _, address := range tables {
			table := view.MustRelation(address)
			if view.MustNamespace(table.Namespace()).Temporary != (ids.SessionToken{}) {
				private = true
				break
			}
		}
		if private {
			return view, nil
		}
		current := scope.op.session.Engine().CatalogRead()
		if current.Epoch() != view.Epoch() {
			return view, ids.ErrCatalogReplaced
		}
		for address := range locked {
			if current.Get(address) != view.Get(address) {
				return view, CatalogConflict{}
			}
		}
		for address, descriptor := range removals {
			if current.Get(address) != descriptor {
				return view, CatalogConflict{}
			}
		}
		view = current
	}
}
