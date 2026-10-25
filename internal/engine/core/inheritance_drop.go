package core

import (
	"iter"
	"stuntgres/internal/engine/table"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

// admitInheritanceDropClosure reconciles actual captures without a stage. The
// incumbent execution wrapper alone installs its returned catalog as stage base.
func admitInheritanceDropClosure(scope *StatementScope, view catalog.Read, roots []ids.ObjectAddress, removals iter.Seq2[ids.ObjectAddress, catalog.Descriptor]) (catalog.Read, error) {
	if scope == nil {
		return view, nil
	}
	// Ordinary DROP owns its existing admission policy. In particular low-level
	// callers need not have acquired a root grant; do not introduce one unless
	// an inheritance edge actually requires fencing the closure.
	participates := false
	for _, root := range roots {
		if relation, ok := view.Relation(root); ok && root.SubID == 0 && catalog.InheritanceParticipant(view, relation) {
			participates = true
			break
		}
	}
	if !participates {
		return view, nil
	}
	locked := make(map[ids.ObjectAddress]bool)
	for {
		tables := table.InheritanceDropClosure(view, roots)
		grew := false
		for _, address := range tables {
			if locked[address] {
				continue
			}
			if err := scope.AcquireSequence(view.Owner().Domain.Ref(view.Epoch(), address), lock.AccessExclusive, true); err != nil {
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
			relation := view.MustRelation(address)
			if view.MustNamespace(relation.Namespace()).Temporary != (ids.SessionToken{}) {
				private = true
				break
			}
		}
		if private {
			return view, nil
		}
		current := scope.Engine().CatalogRead()
		if current.Epoch() != view.Epoch() {
			return view, ids.ErrCatalogReplaced
		}
		for address := range locked {
			if current.Get(address) != view.Get(address) {
				return view, catalog.Conflict{}
			}
		}
		for address, descriptor := range removals {
			if current.Get(address) != descriptor {
				return view, catalog.Conflict{}
			}
		}
		view = current
	}
}
