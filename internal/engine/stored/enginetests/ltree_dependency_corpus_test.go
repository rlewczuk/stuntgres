package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

// Reuse the existing real binding inventories without multiplying ownership,
// remapping and authority matrices already covered by those inventories.
func TestLtreeSQLDependencyCorpus(t *testing.T) {
	t.Run("arrays", func(t *testing.T) {
		e := engine.New()
		s := ltreeArrayOwnershipSetup(t, e)
		view := e.CatalogRead()
		for _, direct := range []bool{false, true} {
			project := ltreeArrayOwnershipBinding(t, e, s, direct)
			for _, node := range project.Expressions {
				call := ltreeArrayOwnershipCall(t, node)
				identity := call.Ref.Address
				if selected, ok := node.(plan.SelectedOperatorExpr); ok {
					identity = selected.Ref.Address
				}
				want := []ids.ObjectAddress{identity, call.Args[0].(plan.ColumnExpr).Origin.Address, call.Args[1].(plan.ColumnExpr).Origin.Address}
				got, err := stored.ExpressionDependRefs(context.Background(), view, node)
				selectedDependencySet(t, got, err, want...)
			}
		}
	})
	t.Run("paths", func(t *testing.T) {
		e := engine.New()
		s := ltreePathOwnershipSetup(t, e)
		view := e.CatalogRead()
		relation := view.MustRelation(view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "lh_owner"}))
		columns := [][]int{{1}, {1}, {1}, {1, 2}, {1, 2}, {1}, {1}, {3}, {4}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 2}, {1, 3}, {1, 3}}
		for _, direct := range []bool{false, true} {
			project := ltreePathOwnershipBinding(t, e, s, direct)
			if len(project.Expressions) != len(columns) {
				t.Fatal("path inventory and dependency expectations diverged")
			}
			for i, node := range project.Expressions {
				identity := selectedDependencyProcedure(t, ltreePathOwnershipCall(t, node)).Address()
				if selected, ok := node.(plan.SelectedOperatorExpr); ok {
					identity = selected.Ref.Address
				}
				want := []ids.ObjectAddress{identity}
				for _, slot := range columns[i] {
					address := relation.Address()
					address.SubID = relation.Attributes[slot-1]
					want = append(want, address)
				}
				got, err := stored.ExpressionDependRefs(context.Background(), view, node)
				selectedDependencySet(t, got, err, want...)
			}
		}
	})
	t.Run("queries", func(t *testing.T) {
		e := engine.New()
		s := ltreeLqueryOwnershipSetup(t, e)
		view := e.CatalogRead()
		relation := view.MustRelation(view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "lq_owner"}))
		for _, direct := range []bool{false, true} {
			project := ltreeLqueryOwnershipBinding(t, e, s, direct)
			for _, node := range project.Expressions {
				identity := selectedDependencyProcedure(t, ltreeLqueryOwnershipCall(t, node)).Address()
				if selected, ok := node.(plan.SelectedOperatorExpr); ok {
					identity = selected.Ref.Address
				}
				want := []ids.ObjectAddress{identity}
				for _, attribute := range relation.Attributes {
					address := relation.Address()
					address.SubID = attribute
					want = append(want, address)
				}
				got, err := stored.ExpressionDependRefs(context.Background(), view, node)
				selectedDependencySet(t, got, err, want...)
			}
		}
	})
	t.Run("ltxtqueries", func(t *testing.T) {
		e := engine.New()
		s := ltreeLtxtqueryOwnershipSetup(t, e)
		view := e.CatalogRead()
		for _, direct := range []bool{false, true} {
			project := ltreeLtxtqueryOwnershipBinding(t, e, s, direct)
			for _, node := range project.Expressions {
				call := ltreeLtxtqueryOwnershipCall(t, node)
				identity := call.Ref.Address
				if selected, ok := node.(plan.SelectedOperatorExpr); ok {
					identity = selected.Ref.Address
				}
				want := []ids.ObjectAddress{identity, call.Args[0].(plan.ColumnExpr).Origin.Address, call.Args[1].(plan.ColumnExpr).Origin.Address}
				got, err := stored.ExpressionDependRefs(context.Background(), view, node)
				selectedDependencySet(t, got, err, want...)
			}
		}
	})
}
