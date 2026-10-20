package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

// Caller holds engine.mu, the same installation/capture mutex. Every fence is
// required: a storage pointer alone can retain newer vectors and end roots.
// Historical, private and rewritten views always use the ordinary scan.
func (e *Engine) capturePartialMaskLocked(scope *StatementScope, leaf exec.Leaf, view *visibility.View, mark plan.RowMark) *storage.IndexRoot {
	candidate := leaf.PartialCandidate
	if candidate == nil || view == nil || scope.owner.catalog != nil || stableIsolation(scope.owner.isolation) || scope.queryLocks != nil || mark.Mode != 0 {
		return nil
	}
	data := view.Data()
	if data == nil || data.Catalog.State() != e.catalog.state || data.Catalog.Owner() != e.catalog.owner() || leaf.Ref.Domain() != e.catalog.Domain || leaf.Ref.Epoch != e.catalog.state.Epoch() {
		return nil
	}
	parent := leaf.Ref.Address
	if len(data.Own[parent]) != 0 || data.Deleted[parent] != nil || len(data.RewrittenFrontiers) != 0 || data.FrontierAt(parent) != e.visibility.CommitSeq() {
		return nil
	}
	store := e.storage.Relations[parent]
	if store == nil || data.GenerationAt(parent) != store || leaf.Storage != store || len(data.Committed[parent]) != len(store.Versions) || data.Ends[parent] != store.Ends {
		return nil
	}
	prefix := data.Committed[parent]
	if len(prefix) != 0 && &prefix[0] != &store.Versions[0] {
		return nil
	}
	current, ok := e.catalog.state.Relation(parent)
	if !ok || !sameRelationRowShape(current, leaf.Relation) {
		return nil
	}
	if e.catalog.state.Get(candidate.Address()) != candidate || candidate.SQLIndex.Parent != parent {
		return nil
	}
	root := e.storage.IndexRoots[candidate.Address()]
	if root == nil || root.Membership == nil {
		return nil
	}
	return root
}
