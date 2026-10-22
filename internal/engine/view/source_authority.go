package view

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

// AuthorizeSource checks a stored view occurrence: SELECT on each source
// leaf under the view owner or invoker, then its row marks. The source
// collector and execution fences use the same captured owner/invoker
// checks; neither substitutes a live role/catalog sample during recursion.
func AuthorizeSource(a access.View, env access.Environment, ref ids.ObjectRef, definition *stored.View, invoker authorization.PrincipalRef, mark plan.RowMark) error {
	d, ok := a.Catalog.Relation(ref.Address)
	if !ok || ref.Domain() != a.Owner.Domain || ref.Epoch != a.Catalog.Epoch() || stored.ViewOf(d) != definition {
		return ids.ErrCatalogReplaced
	}
	principal := d.Owner
	if definition.Invoker() {
		principal = invoker
	}
	a.State.CurrentUser = principal
	for _, leaf := range definition.Sources() {
		source, ok := a.Catalog.Relation(leaf.Ref.Address)
		if !ok || !stored.ViewReferenceValid(a.Catalog, d, leaf.Ref) {
			return ids.ErrCatalogReplaced
		}
		if leaf.AnalysisOnly {
			continue
		}
		indices := make([]int, 0, len(leaf.Reads))
		for _, attribute := range leaf.Reads {
			found := false
			for slot, live := range source.Attributes {
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
		use, err := stored.ViewUseReference(a.Catalog, d, leaf.Ref)
		if err != nil {
			return err
		}
		if err := a.CheckColumns(env, use, source, authorization.SelectPrivilege, indices); err != nil {
			return err
		}
	}
	if mark.Mode != 0 || stored.ViewHasRowMarks(d, a.Catalog) {
		return CheckRowMarks(a, env, d, mark, invoker)
	}
	return nil
}

// CheckRowMarks validates d's read graph, then checks the stored and
// inherited row marks of its sources under each view's owner or invoker.
func CheckRowMarks(a access.View, env access.Environment, d *catalog.Relation, inherited plan.RowMark, invoker authorization.PrincipalRef) error {
	if _, err := stored.ValidateViewReadGraph(a.Catalog, []exec.Leaf{{Relation: d}}); err != nil {
		return err
	}
	return checkRowMarksExpanded(a, env, d, inherited, invoker)
}

func checkRowMarksExpanded(a access.View, env access.Environment, d *catalog.Relation, inherited plan.RowMark, invoker authorization.PrincipalRef) error {
	if d.RelationKind != catalog.ViewRelation {
		return nil
	}
	if err := stored.ViewRowLockLegality(stored.ViewOf(d), analyze.RowLockDiagnostic(inherited)); err != nil {
		return err
	}
	principal := d.Owner
	if stored.ViewOf(d).Invoker() {
		principal = invoker
	}
	a.State.CurrentUser = principal
	for i, leaf := range stored.ViewOf(d).Sources() {
		mark := stored.ViewLeafRowMark(stored.ViewOf(d), i, inherited)
		child, ok := a.Catalog.Relation(leaf.Ref.Address)
		if !ok || !stored.ViewReferenceValid(a.Catalog, d, leaf.Ref) {
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
		use, err := stored.ViewUseReference(a.Catalog, d, leaf.Ref)
		if err != nil {
			return err
		}
		if err := a.CheckColumns(env, use, child, authorization.SelectPrivilege, indices); err != nil {
			return err
		}
		if exec.EffectiveQueryRowMark(child, mark).Mode != 0 {
			if err := a.CheckColumns(env, use, child, authorization.UpdatePrivilege, nil); err != nil {
				return err
			}
		}
		if err := checkRowMarksExpanded(a, env, child, mark, invoker); err != nil {
			return err
		}
	}
	return nil
}

// CheckRowSecuritySource applies the row_security setting guard to relation and,
// through stored views, to each source under the layer's principal.
// stored.ValidateViewReadGraph has already rejected active-path cycles. Each layer's
// invoker is the executing user, not the owner of an enclosing definer view.
func CheckRowSecuritySource(auth access.View, relation *catalog.Relation, principal, invoker authorization.PrincipalRef) error {
	if relation.RelationKind != catalog.ViewRelation {
		return access.RowSecuritySettingGuard(false, auth.Roles, relation, principal)
	}
	principal = relation.Owner
	if stored.ViewOf(relation).Invoker() {
		principal = invoker
	}
	for _, source := range stored.ViewOf(relation).Sources() {
		child, ok := auth.Catalog.Relation(source.Ref.Address)
		if !ok || !stored.ViewReferenceValid(auth.Catalog, relation, source.Ref) {
			return ids.ErrCatalogReplaced
		}
		if err := CheckRowSecuritySource(auth, child, principal, invoker); err != nil {
			return err
		}
	}
	return nil
}
