package engine

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

func (e *Engine) firstQueryRowModeLocked(scope *StatementScope, root plan.Node, leaves []exec.Leaf) (lock.Mode, error) {
	view := e.privateCatalogLocked(scope.owner.Catalog)
	if _, err := stored.ValidateViewReadGraph(view, leaves); err != nil {
		return 0, err
	}
	if mode := analyze.QueryRootRowMode(root); mode != 0 {
		return mode, nil
	}
	var visit func(*catalog.Relation, plan.RowMark, lock.Mode) (lock.Mode, error)
	visit = func(d *catalog.Relation, inherited plan.RowMark, diagnostic lock.Mode) (lock.Mode, error) {
		if d.RelationKind != catalog.ViewRelation {
			if inherited.Mode == 0 {
				return 0, nil
			}
			if diagnostic != 0 {
				return diagnostic, nil
			}
			return inherited.Mode, nil
		}
		if inherited.Mode != 0 {
			return analyze.RowLockDiagnostic(inherited), nil
		}
		if diagnostic == 0 {
			diagnostic = analyze.QueryRootRowMode(stored.ViewOf(d).Query().Root)
		}
		for i, leaf := range stored.ViewOf(d).Sources() {
			child, ok := view.Relation(leaf.Ref.Address)
			if !ok || !stored.ViewReferenceValid(view, d, leaf.Ref) {
				return 0, ids.ErrCatalogReplaced
			}
			first, err := visit(child, viewLeafRowMark(stored.ViewOf(d), i, inherited), diagnostic)
			if err != nil || first != 0 {
				return first, err
			}
		}
		return 0, nil
	}
	for _, leaf := range leaves {
		first, err := visit(leaf.Relation, leaf.Mark, analyze.RowLockDiagnostic(leaf.Mark))
		if err != nil || first != 0 {
			return first, err
		}
	}
	return 0, nil
}
func viewHasStoredRowMarks(d *catalog.Relation, view catalog.Read) bool {
	seen := make(map[ids.ObjectAddress]bool)
	var visit func(*catalog.Relation) bool
	visit = func(d *catalog.Relation) bool {
		if d.RelationKind != catalog.ViewRelation || seen[d.Address()] {
			return false
		}
		seen[d.Address()] = true
		if analyze.QueryRootRowMode(stored.ViewOf(d).Query().Root) != 0 {
			return true
		}
		for _, leaf := range stored.ViewOf(d).Sources() {
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
func viewRowLockLegality(d *viewDefinition, mode lock.Mode) error {
	if analyze.QueryHasSetOutput(d.Query().Root) {
		return nil // A stored set view is an opaque physical-lineage boundary.
	}
	return analyze.QueryRowLockLegality(d.Query().Root, mode)
}

// Called before any source expression is opened. Inherited rowmark legality,
// kind and authorization checks remain Execute-only.
func (e *Engine) initializeQueryLocksLocked(scope *StatementScope, root plan.Node, leaves []exec.Leaf) error {
	marked := analyze.QueryRootRowMode(root) != 0
	view := e.privateCatalogLocked(scope.owner.Catalog)
	visibilityDemand, err := stored.ValidateViewReadGraph(view, leaves)
	if err != nil {
		return err
	}
	scope.inquiry.visibilityDemand = scope.inquiry.visibilityDemand || visibilityDemand
	var visit func(*catalog.Relation, plan.RowMark, bool) error
	visit = func(d *catalog.Relation, inherited plan.RowMark, referenceMarked bool) error {
		if d.RelationKind == catalog.ViewRelation {
			if analyze.QueryHasSetOutput(stored.ViewOf(d).Query().Root) {
				referenceMarked = false
			}
			referenceMarked = referenceMarked || analyze.QueryRootRowMode(stored.ViewOf(d).Query().Root) != 0
			marked = marked || analyze.QueryRootRowMode(stored.ViewOf(d).Query().Root) != 0
			if err := viewRowLockLegality(stored.ViewOf(d), analyze.RowLockDiagnostic(inherited)); err != nil {
				return err
			}
			for i, leaf := range stored.ViewOf(d).Sources() {
				child, ok := view.Relation(leaf.Ref.Address)
				if !ok || !stored.ViewReferenceValid(view, d, leaf.Ref) {
					return ids.ErrCatalogReplaced
				}
				if err := visit(child, viewLeafRowMark(stored.ViewOf(d), i, inherited), referenceMarked); err != nil {
					return err
				}
			}
			return nil
		}
		if exec.IsPGSequencesRelation(d) {
			return nil // Expanded catalog view: no represented physical tuple input.
		}
		if (referenceMarked || inherited.Mode != 0) && d.RelationKind == catalog.MaterializedViewRelation {
			return sqlError("42809", fmt.Sprintf("cannot lock rows in materialized view %q", d.Name()))
		}
		// PostgreSQL validates reference rowmark kinds even for an unselected
		// sequence sibling. This does not confer UPDATE ACLs or tuple grants.
		if referenceMarked && d.RelationKind == catalog.SequenceRelation {
			return sqlError("42809", fmt.Sprintf("cannot lock rows in sequence %q", d.Name()))
		}
		if inherited.Mode == 0 {
			return nil
		}
		marked = true
		if d.RelationKind == catalog.CatalogRelation {
			return sqlError("0A000", "row locking is not supported for virtual catalog relations")
		}
		return nil
	}
	for _, leaf := range leaves {
		if err := visit(leaf.Relation, leaf.Mark, leaf.DeleteReference || leaf.Query != nil && len(leaf.Query.Locking) != 0); err != nil {
			return err
		}
	}
	if marked {
		scope.queryLocks = &exec.LockContext{}
	}
	return nil
}

func (v AuthorizationView) checkViewRowMarks(d *catalog.Relation, inherited plan.RowMark, invoker authorization.PrincipalRef) error {
	if _, err := stored.ValidateViewReadGraph(v.Catalog, []exec.Leaf{{Relation: d}}); err != nil {
		return err
	}
	return v.checkViewRowMarksExpanded(d, inherited, invoker)
}

func (v AuthorizationView) checkViewRowMarksExpanded(d *catalog.Relation, inherited plan.RowMark, invoker authorization.PrincipalRef) error {
	if d.RelationKind != catalog.ViewRelation {
		return nil
	}
	if err := viewRowLockLegality(stored.ViewOf(d), analyze.RowLockDiagnostic(inherited)); err != nil {
		return err
	}
	principal := d.Owner
	if stored.ViewOf(d).Invoker() {
		principal = invoker
	}
	v.State.CurrentUser = principal
	for i, leaf := range stored.ViewOf(d).Sources() {
		mark := viewLeafRowMark(stored.ViewOf(d), i, inherited)
		child, ok := v.Catalog.Relation(leaf.Ref.Address)
		if !ok || !stored.ViewReferenceValid(v.Catalog, d, leaf.Ref) {
			return ids.ErrCatalogReplaced
		}
		indices := make([]int, 0, len(leaf.Reads))
		for _, attribute := range leaf.Reads {
			found := false
			for slot, live := range child.Attributes {
				if attribute == live {
					indices = append(indices, slot)
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("stuntgres: missing view source attribute")
			}
		}
		use, err := stored.ViewUseReference(v.Catalog, d, leaf.Ref)
		if err != nil {
			return err
		}
		if err := v.CheckColumns(use, child, authorization.SelectPrivilege, indices); err != nil {
			return err
		}
		if exec.EffectiveQueryRowMark(child, mark).Mode != 0 {
			if err := v.CheckColumns(use, child, authorization.UpdatePrivilege, nil); err != nil {
				return err
			}
		}
		if err := v.checkViewRowMarksExpanded(child, mark, invoker); err != nil {
			return err
		}
	}
	return nil
}

func viewLeafRowMark(view *viewDefinition, occurrence int, inherited plan.RowMark) plan.RowMark {
	mark := view.Sources()[occurrence].Mark
	if inherited.Mode != 0 && analyze.QueryRowMarkReachesSource(view.Query().Root, occurrence) {
		mark = exec.MergeQueryRowMark(inherited, mark)
	}
	return mark
}
