package stored

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
)

// ViewHasRowMarks reports whether a view, or any view it reads, carries a
// stored row mark in its query root or on a source occurrence.
func ViewHasRowMarks(d *catalog.Relation, view catalog.Read) bool {
	seen := make(map[ids.ObjectAddress]bool)
	var visit func(*catalog.Relation) bool
	visit = func(d *catalog.Relation) bool {
		if d.RelationKind != catalog.ViewRelation || seen[d.Address()] {
			return false
		}
		seen[d.Address()] = true
		if analyze.QueryRootRowMode(ViewOf(d).Query().Root) != 0 {
			return true
		}
		for _, leaf := range ViewOf(d).Sources() {
			if leaf.Mark.Mode != 0 {
				return true
			}
			if child, ok := view.Relation(leaf.Ref.Address); ok && visit(child) {
				return true
			}
		}
		return false
	}
	return visit(d)
}

// ViewRowLockLegality checks an inherited row-lock mode against a stored view
// query. A stored set view is an opaque physical-lineage boundary.
func ViewRowLockLegality(d *View, mode lock.Mode) error {
	if analyze.QueryHasSetOutput(d.Query().Root) {
		return nil // A stored set view is an opaque physical-lineage boundary.
	}
	return analyze.QueryRowLockLegality(d.Query().Root, mode)
}

// ViewLeafRowMark is the row mark of one stored source occurrence, merged with
// the inherited mark when that mark reaches the occurrence.
func ViewLeafRowMark(view *View, occurrence int, inherited plan.RowMark) plan.RowMark {
	mark := view.Sources()[occurrence].Mark
	if inherited.Mode != 0 && analyze.QueryRowMarkReachesSource(view.Query().Root, occurrence) {
		mark = exec.MergeQueryRowMark(inherited, mark)
	}
	return mark
}
