package enginetests_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

func intarraySortDependencyOwnership(t *testing.T) {
	e := engine.New()
	s := intarraySortOwnershipSetup(t, e)
	view := e.CatalogRead()
	relation := view.MustRelation(view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ia_owner"}))
	column := func(index int) ids.ObjectAddress {
		address := relation.Address()
		address.SubID = relation.Attributes[index]
		return address
	}
	for _, tc := range []struct {
		call    string
		columns []ids.ObjectAddress
	}{
		{"public.sort(a)", []ids.ObjectAddress{column(0)}},
		{"public.sort(a,d)", []ids.ObjectAddress{column(0), column(3)}},
		{"public.sort_asc(a)", []ids.ObjectAddress{column(0)}},
		{"public.sort_desc(a)", []ids.ObjectAddress{column(0)}},
		{"public.uniq(a)", []ids.ObjectAddress{column(0)}},
		{"public.sort(a,COALESCE(d,d))", []ids.ObjectAddress{column(0), column(3)}},
	} {
		binding := selectedDependencyBinding(t, e, s, "SELECT "+tc.call+","+tc.call+" FROM ia_owner")
		project := binding.Query.Root.(*plan.ProjectNode)
		selectedDependencyManifest(t, binding, selectedDependencyProcedure(t, project.Expressions[0]))
		before := append([]catalog.ExpressionDependency(nil), binding.Dependencies...)
		first, second := intarraySortOwnershipCall(t, project.Expressions[0]), intarraySortOwnershipCall(t, project.Expressions[1])
		// Occurrence is an expansion ordinal within Source, not a global ID.
		// Written repeats keep distinct source/ordinal pairs and lexical spans.
		sameOccurrence := func(a, b expr.Info) bool {
			return a.Source == b.Source && a.Occurrence == b.Occurrence
		}
		if first.Occurrence == 0 || second.Occurrence == 0 || sameOccurrence(first.Info, second.Info) || first.SourcePosition() == second.SourcePosition() {
			t.Fatalf("repeated written calls collapsed provenance: first=%#v/%d second=%#v/%d", first.Source, first.Occurrence, second.Source, second.Occurrence)
		}
		for i := range first.Operation.Arity() {
			a, b := expr.InfoOf(first.Args[i]), expr.InfoOf(second.Args[i])
			if a.Occurrence == 0 || b.Occurrence == 0 || sameOccurrence(a, b) || a.Pos == b.Pos {
				t.Fatalf("repeated operand %d collapsed provenance: first=%#v/%d second=%#v/%d", i, a.Source, a.Occurrence, b.Source, b.Occurrence)
			}
		}
		if tc.call == "public.sort(a,COALESCE(d,d))" {
			// This harness retains the analyzed graph, before execution
			// simplification. ConditionalExpr owns Args explicitly; it is not
			// an expr.Composite and must be inspected through its real owner.
			for _, call := range []functions.IntarraySort{first, second} {
				direction, ok := call.Args[1].(plan.ConditionalExpr)
				if !ok || direction.Kind != parser.Coalesce || len(direction.Args) != 2 {
					t.Fatalf("analyzed repeated direction lost conditional operands: %T", call.Args[1])
				}
				left, leftOK := direction.Args[0].(plan.ColumnExpr)
				right, rightOK := direction.Args[1].(plan.ColumnExpr)
				if !leftOK || !rightOK || left.Origin.Address != column(3) || right.Origin != left.Origin || left.Slot != right.Slot || left.ResultUsage() != right.ResultUsage() {
					t.Fatal("repeated direction leaves lost their genuine selected column")
				}
				a, b := expr.InfoOf(left), expr.InfoOf(right)
				if a.Occurrence == 0 || b.Occurrence == 0 || sameOccurrence(a, b) || a.Pos.Character >= b.Pos.Character {
					t.Fatal("repeated direction leaves collapsed or reordered lexical provenance")
				}
			}
		}
		for _, node := range project.Expressions {
			call := intarraySortOwnershipCall(t, node)
			capture, ok, err := call.Capture(expr.CaptureFunction)
			if err != nil || !ok || capture.Count != call.Operation.Arity() || !reflect.DeepEqual(capture.Args, call.Children()) {
				t.Fatal("capture lost active operand order", err)
			}
			got, err := stored.ExpressionDependRefs(t.Context(), view, node)
			selectedDependencySet(t, got, err, append(append([]ids.ObjectAddress(nil), tc.columns...), call.Ref.Address)...)
		}
		got, err := stored.QueryDependRefs(t.Context(), view, nil, binding.Query)
		selectedDependencySet(t, got, err, append(append([]ids.ObjectAddress(nil), tc.columns...), first.Ref.Address)...)
		if !reflect.DeepEqual(before, binding.Dependencies) {
			t.Fatal("dependency traversal mutated executable manifest")
		}
	}
}
