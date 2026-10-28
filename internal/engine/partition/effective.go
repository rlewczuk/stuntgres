package partition

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// EffectiveChildren separates canonical dependency ownership from executable
// topology. Only a currently pending edge can consult the retained snapshot;
// fully removed edges and replacement identities are never resurrected.
// The normal attached path returns the original immutable slice without copying.
func EffectiveChildren(view catalog.Read, parent *catalog.Relation, snapshot *catalog.State) []ids.ObjectAddress {
	if parent.Partition == nil {
		return nil
	}
	included := func(a ids.ObjectAddress) bool {
		child, ok := view.Relation(a)
		if !ok || child.Membership == nil || !child.Membership.DetachPending {
			return true
		}
		if snapshot == nil || snapshot.Epoch() != view.Epoch() {
			return false
		}
		old, ok := snapshot.Get(a).(*catalog.Relation)
		return ok && old.Membership != nil && !old.Membership.DetachPending && old.Membership.Parent == parent.Address()
	}
	children := parent.Partition.Children
	for i, a := range children {
		if included(a) {
			continue
		}
		kept := make([]ids.ObjectAddress, 0, len(children)-1)
		kept = append(kept, children[:i]...)
		for _, later := range children[i+1:] {
			if included(later) {
				kept = append(kept, later)
			}
		}
		return kept
	}
	return children
}
