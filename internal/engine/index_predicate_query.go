package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// Binding runs under engine.mu. Candidate identity and definition are immutable;
// source capture revalidates them rather than reopening names or substituting
// prepared values. All candidates have RelationClassID and SubID zero.
func (b *expressionBinder) partialIndexCandidate(parent ids.ObjectRef, query expr.Expr) *catalog.Relation {
	if b.queryAuth == nil {
		return nil
	}
	var chosen *catalog.Relation
	for _, descriptor := range b.queryAuth.Catalog.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != parent.Address || index.SQLIndex.Predicate == nil {
			continue
		}
		if !partialPredicateImpliedBy(query, index.SQLIndex.Predicate.Definition) {
			continue
		}
		if chosen == nil || index.Address().ObjectID < chosen.Address().ObjectID {
			chosen = index
		}
	}
	return chosen
}

// Caller holds engine.mu, the same installation/capture mutex. Every fence is
// required: a storage pointer alone can retain newer vectors and end roots.
// Historical, private and rewritten views always use the ordinary scan.
func (e *Engine) capturePartialMaskLocked(scope *StatementScope, leaf queryLeaf, view *VisibilityView, mark queryRowMark) *storage.IndexRoot {
	candidate := leaf.partialCandidate
	if candidate == nil || view == nil || scope.owner.catalog != nil || stableIsolation(scope.owner.isolation) || scope.queryLocks != nil || mark.mode != 0 {
		return nil
	}
	data := view.Data()
	if data == nil || data.Catalog.State() != e.catalog.state || data.Catalog.Owner() != e.catalog.owner() || leaf.ref.Domain() != e.catalog.Domain || leaf.ref.Epoch != e.catalog.state.Epoch() {
		return nil
	}
	parent := leaf.ref.Address
	if len(data.Own[parent]) != 0 || data.Deleted[parent] != nil || len(data.RewrittenFrontiers) != 0 || data.FrontierAt(parent) != e.visibility.CommitSeq() {
		return nil
	}
	store := e.storage.Relations[parent]
	if store == nil || data.GenerationAt(parent) != store || leaf.storage != store || len(data.Committed[parent]) != len(store.Versions) || data.Ends[parent] != store.Ends {
		return nil
	}
	prefix := data.Committed[parent]
	if len(prefix) != 0 && &prefix[0] != &store.Versions[0] {
		return nil
	}
	current, ok := e.catalog.state.Relation(parent)
	if !ok || !sameRelationRowShape(current, leaf.relation) {
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

func partialMaskContains(root *storage.IndexRoot, row storage.RowVersion) bool {
	version, ok := root.Membership[row.Row]
	return ok && version == row.Version
}
