package enginetests_test

import (
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"testing"
)

func intarraySetDependencyOwnership(t *testing.T) {
	e := engine.New()
	s := intarraySetOwnershipSetup(t, e)
	view := e.CatalogRead()
	relation := view.MustRelation(view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ia_owner"}))
	column := func(index int) ids.ObjectAddress {
		address := relation.Address()
		address.SubID = relation.Attributes[index]
		return address
	}
	for _, tc := range []struct {
		symbol, name, right string
		index               int
	}{
		{"&&", "_int_overlap", "b", 1}, {"@>", "_int_contains", "b", 1}, {"<@", "_int_contained", "b", 1},
		{"+", "intarray_push_elem", "n", 2}, {"+", "intarray_push_array", "b", 1},
		{"-", "intarray_del_elem", "n", 2}, {"-", "intset_subtract", "b", 1},
		{"|", "intset_union_elem", "n", 2}, {"|", "_int_union", "b", 1}, {"&", "_int_inter", "b", 1},
	} {
		binding := selectedDependencyBinding(t, e, s, "SELECT a OPERATOR(public."+tc.symbol+") "+tc.right+",public."+tc.name+"(a,"+tc.right+") FROM ia_owner")
		project := binding.Query.Root.(*plan.ProjectNode)
		selected := project.Expressions[0].(plan.SelectedOperatorExpr)
		backer := selectedDependencyProcedure(t, selected.Provider)
		direct := selectedDependencyProcedure(t, project.Expressions[1])
		if backer != direct || backer.Name() != tc.name || selected.Operator.Function != backer.Address().ObjectID {
			t.Fatal("operator and direct call lost selected backer")
		}
		selectedDependencyManifest(t, binding, backer)
		capturedOperator := false
		for _, dep := range binding.Dependencies {
			if dep.Ref == selected.Ref && dep.Revision == selected.Operator.Revision() {
				capturedOperator = true
			}
		}
		if !capturedOperator {
			t.Fatal("missing exact selected operator lifetime")
		}
		before := append([]catalog.ExpressionDependency(nil), binding.Dependencies...)
		got, err := stored.ExpressionDependRefs(t.Context(), view, selected)
		selectedDependencySet(t, got, err, column(0), column(tc.index), selected.Ref.Address)
		got, err = stored.ExpressionDependRefs(t.Context(), view, project.Expressions[1])
		selectedDependencySet(t, got, err, column(0), column(tc.index), backer.Address())
		got, err = stored.QueryDependRefs(t.Context(), view, nil, binding.Query)
		selectedDependencySet(t, got, err, column(0), column(tc.index), selected.Ref.Address, backer.Address())
		if !reflect.DeepEqual(before, binding.Dependencies) {
			t.Fatal("SQL edge traversal changed executable manifest")
		}
	}
}
