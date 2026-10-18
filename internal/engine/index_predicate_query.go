package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// Binding runs under engine.mu. Candidate identity and definition are immutable;
// source capture revalidates them rather than reopening names or substituting
// prepared values. All candidates have RelationClassID and SubID zero.
func (b *expressionBinder) partialIndexCandidate(parent ids.ObjectRef, query expr.Expr) *relationDescriptor {
	if b.queryAuth == nil {
		return nil
	}
	var chosen *relationDescriptor
	for _, descriptor := range b.queryAuth.catalog.Descriptors() {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != parent.Address || index.SQLIndex.Predicate == nil {
			continue
		}
		if !partialPredicateImpliedBy(query, index.SQLIndex.Predicate.Definition) {
			continue
		}
		if chosen == nil || index.address.ObjectID < chosen.address.ObjectID {
			chosen = index
		}
	}
	return chosen
}

// Caller holds engine.mu, the same installation/capture mutex. Every fence is
// required: a storage pointer alone can retain newer vectors and end roots.
// Historical, private and rewritten views always use the ordinary scan.
func (e *Engine) capturePartialMaskLocked(scope *StatementScope, leaf queryLeaf, view *VisibilityView, mark queryRowMark) *logicalIndexRoot {
	candidate := leaf.partialCandidate
	if candidate == nil || view == nil || scope.owner.catalog != nil || stableIsolation(scope.owner.isolation) || scope.queryLocks != nil || mark.mode != 0 {
		return nil
	}
	data := view.read()
	if data == nil || data.catalog.state != e.catalog.state || data.catalog.owner != e.catalog.owner() || leaf.ref.Domain() != e.catalog.Domain || leaf.ref.Epoch != e.catalog.state.epoch {
		return nil
	}
	parent := leaf.ref.Address
	if len(data.own[parent]) != 0 || data.deleted[parent] != nil || len(data.rewrittenFrontiers) != 0 || data.frontierAt(parent) != e.commitSeq {
		return nil
	}
	store := e.storage[parent]
	if store == nil || data.generationAt(parent) != store || leaf.storage != store || len(data.committed[parent]) != len(store.versions) || data.ends[parent] != store.ends {
		return nil
	}
	prefix := data.committed[parent]
	if len(prefix) != 0 && &prefix[0] != &store.versions[0] {
		return nil
	}
	current, ok := e.catalog.state.Relation(parent)
	if !ok || !sameRelationRowShape(current, leaf.relation) {
		return nil
	}
	if e.catalog.state.Get(candidate.address) != candidate || candidate.SQLIndex.Parent != parent {
		return nil
	}
	root := e.indexRoots[candidate.address]
	if root == nil || root.membership == nil {
		return nil
	}
	return root
}

func partialMaskContains(root *logicalIndexRoot, row rowVersion) bool {
	version, ok := root.membership[row.row]
	return ok && version == row.version
}
