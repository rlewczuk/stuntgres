package table

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/stored"
)

// Called under Engine.mu. Stored views expose their named source's scan mode,
// not a flattened set of leaves or the mode of an unrelated SQL occurrence.
func InheritanceQueryLeaves(view catalog.Read, roots []exec.Leaf, token ids.SessionToken, visit func(exec.Leaf) error) error {
	var active []ids.ObjectAddress
	var walk func(exec.Leaf) error
	walk = func(leaf exec.Leaf) error {
		d, ok := view.Relation(leaf.Ref.Address)
		if !ok || leaf.Ref.Epoch != view.Epoch() {
			return ids.ErrCatalogReplaced
		}
		leaf.Relation = d
		if d.RelationKind == catalog.ViewRelation {
			for _, address := range active {
				if address == d.Address() {
					return nil
				}
			}
			active = append(active, d.Address())
			defer func() { active = active[:len(active)-1] }()
			for i, child := range stored.ViewOf(d).Sources() {
				if child.AnalysisOnly {
					continue
				}
				ref, err := stored.ViewUseReference(view, d, child.Ref)
				if err != nil {
					return err
				}
				if err := walk(exec.Leaf{Ref: ref, Only: child.Only, Mark: stored.ViewLeafRowMark(stored.ViewOf(d), i, leaf.Mark)}); err != nil {
					return err
				}
			}
			return nil
		}
		if d.RelationKind != catalog.TableRelation || leaf.Only {
			return visit(leaf)
		}
		return InheritanceQueryClosure(view, d, token, func(physical *catalog.Relation) error {
			child := leaf
			child.Ref.Address, child.Relation = physical.Address(), physical
			return visit(child)
		})
	}
	for _, leaf := range roots {
		if !leaf.AnalysisOnly {
			if err := walk(leaf); err != nil {
				return err
			}
		}
	}
	return nil
}

// InheritancePendingGrants returns the physical table leaves of roots that
// still need a grant, in visit order and without storage, and records each
// one's row-mark mode in grants, which it allocates on first need. A zero
// entry stands for AccessShare, a nonzero one for RowShare. Called under
// Engine.mu; it never waits.
func InheritancePendingGrants(view catalog.Read, roots []exec.Leaf, token ids.SessionToken, grants map[ids.ObjectRef]lock.Mode) ([]exec.Leaf, map[ids.ObjectRef]lock.Mode, error) {
	var pending []exec.Leaf
	err := InheritanceQueryLeaves(view, roots, token, func(leaf exec.Leaf) error {
		if leaf.Relation.RelationKind != catalog.TableRelation {
			return nil
		}
		mode := lock.AccessShare
		if leaf.Mark.Mode != 0 {
			mode = lock.RowShare
		}
		// Named query roots already have exact-identity grants. Leave the
		// ordinary single-table path allocation- and reacquisition-free.
		for _, root := range roots {
			if root.Ref == leaf.Ref && root.Relation != nil && root.Relation.RelationKind == catalog.TableRelation && (root.Mark.Mode != 0 || mode == lock.AccessShare) {
				return nil
			}
		}
		// Stored-base admission uses query rowmark modes: a present zero
		// entry already holds AccessShare, any nonzero entry holds RowShare.
		prior, granted := grants[leaf.Ref]
		if granted && (prior != 0 || mode == lock.AccessShare) {
			return nil
		}
		if granted {
			for i := range pending {
				if pending[i].Ref == leaf.Ref {
					pending[i].Mark = leaf.Mark
					grants[leaf.Ref] = leaf.Mark.Mode
					return nil
				}
			}
		}
		pending = append(pending, leaf)
		if grants == nil {
			grants = make(map[ids.ObjectRef]lock.Mode)
		}
		grants[leaf.Ref] = leaf.Mark.Mode
		return nil
	})
	return pending, grants, err
}
