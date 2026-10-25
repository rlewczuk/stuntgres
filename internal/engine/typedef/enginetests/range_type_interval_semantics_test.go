package enginetests_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestRangeTypeCapturedIntervalSemantics(t *testing.T) {
	ctx := context.Background()
	view, r, m := rangeTypeUnitFamily(t, sqltype.Regclass, false)
	subtype, _ := r.RangeDefinition()
	text, _ := view.LookupType(sqltype.Text)
	array, _ := view.LookupArray(r)
	for _, id := range []ids.OID{30000, 30010, 30020, 30030, 30040, 30050} {
		d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, ObjectRevision: 1, ObjectName: fmt.Sprint("endpoint", id), RelationKind: catalog.TableRelation}
		view.State().SetDescriptor(d.Address(), d)
	}
	procedures := make(map[catalog.RangeConstructorForm]*catalog.Procedure)
	for i, form := range []catalog.RangeConstructorForm{catalog.RangeConstructorTwo, catalog.RangeConstructorThree, catalog.MultirangeConstructorZero, catalog.MultirangeConstructorOne, catalog.MultirangeConstructorMany} {
		typ := r
		if form >= catalog.MultirangeConstructorZero {
			typ = m
		}
		provider := &catalog.RangeConstructor{Type: typ, Form: form}
		signature, err := provider.Signature(view)
		if err != nil {
			t.Fatal(err)
		}
		owner := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}).Owner
		p := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(940100 + i)}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 2, ObjectName: typ.Name(), Signature: signature, RoutineKind: catalog.FunctionRoutine, Result: ids.OID(typ.ID()), Owner: owner, Volatility: expr.Immutable, Strict: form >= catalog.MultirangeConstructorZero, RangeConstructor: provider, ACL: authorization.DefaultBaseline(owner, authorization.DefaultRoutines, false)}
		view.State().SetDescriptor(p.Address(), p)
		if err := catalog.ValidateRangeConstructor(view, p); err != nil {
			t.Fatal(err)
		}
		procedures[form] = p
	}
	literal := func(value sqltype.Value) expr.Expr {
		return plan.LiteralExpr{Info: expr.Info{Usage: value.Type().Usage()}, Value: value}
	}
	call := func(form catalog.RangeConstructorForm, variadic bool, args ...expr.Expr) expr.Expr {
		p := procedures[form]
		n := plan.RangeConstructorExpr{Info: expr.Info{Usage: p.RangeConstructor.Type.Usage(), RoutineReference: p}, Procedure: p, Ref: view.Owner().Domain.Ref(view.Epoch(), p.Address()), Args: args, Variadic: variadic}
		if err := n.ValidateStoredProvider(); err != nil {
			t.Fatal(err)
		}
		return n
	}
	bound := func(id int) expr.Expr {
		if id < 0 {
			return literal(sqltype.Null(subtype.Subtype.Type))
		}
		return literal(sqltype.String(subtype.Subtype.Type, fmt.Sprint(id)))
	}
	span := func(lo, hi int, flags string) expr.Expr {
		return call(catalog.RangeConstructorThree, false, bound(lo), bound(hi), literal(sqltype.String(text, flags)))
	}
	many := func(variadic bool, args ...expr.Expr) expr.Expr {
		if variadic {
			return call(catalog.MultirangeConstructorMany, true, plan.ArrayExpr{Info: expr.Info{Usage: array.Usage()}, Elements: args})
		}
		return call(catalog.MultirangeConstructorMany, false, args...)
	}
	capture := func(node expr.Expr) []catalog.AliasOrderConstraint {
		state := catalog.CloneState(view.State())
		a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 940110}
		state.SetDescriptor(a, &catalog.Relation{ObjectAddress: a, ObjectRevision: 1, ObjectName: "definition_only", RelationKind: catalog.TableRelation, ColumnMeta: catalog.ColumnMetadata{Defaults: map[int32]catalog.ColumnDefault{1: {Expression: node}}}})
		edges, err := stored.RangeIdentityConstraints(ctx, state, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, edge := range edges {
			if edge.Lower.Object.ClassID != ids.RelationClassID || edge.Upper.Object.ClassID != ids.RelationClassID {
				t.Fatal("source object provenance lost", edge)
			}
		}
		return edges
	}
	reaches := func(edges []catalog.AliasOrderConstraint, lo, hi ids.OID) bool {
		seen := map[ids.OID]bool{lo: true}
		for changed := true; changed; {
			changed = false
			for _, edge := range edges {
				if seen[edge.Lower.OID] && !seen[edge.Upper.OID] {
					seen[edge.Upper.OID] = true
					changed = true
				}
			}
		}
		return seen[hi]
	}
	assertOrder := func(edges []catalog.AliasOrderConstraint, points ...ids.OID) {
		for i := 1; i < len(points); i++ {
			if !reaches(edges, points[i-1], points[i]) {
				t.Fatal("missing actual endpoint preorder", points[i-1], points[i], edges)
			}
		}
	}
	a, b, c := span(30000, 30010, "[]"), span(30020, 30030, "[]"), span(30040, 30050, "[]")
	for _, variadic := range []bool{false, true} {
		for _, members := range [][]expr.Expr{{b, a}, {b, a, c}, {c, a, b}} {
			edges := capture(many(variadic, members...))
			assertOrder(edges, 30000, 30010, 30020, 30030)
			if len(members) == 3 {
				assertOrder(edges, 30030, 30040, 30050)
			}
		}
		// Hidden source overlap boundaries still influence future recipe evaluation.
		assertOrder(capture(many(variadic, span(30010, 30020, "[]"), span(30000, 30030, "[]"))), 30000, 30010, 30020, 30030)
		assertOrder(capture(many(variadic, span(-1, 30010, "[]"), span(30020, -1, "[]"))), 30010, 30020)
		for _, flags := range []string{"[]", "[)", "(]", "()"} {
			edges := capture(many(variadic, span(30010, 30010, flags), span(30020, 30020, "[]")))
			if flags == "[]" {
				assertOrder(edges, 30010, 30020)
			} else if len(edges) != 0 {
				t.Fatal("empty interval invented finite endpoints", flags, edges)
			}
		}
		// Both excluded sides leave a real gap; any included touching side merges.
		for _, rightFlags := range []string{"(]", "[]"} {
			assertOrder(capture(many(variadic, span(30000, 30010, "[)"), span(30010, 30020, rightFlags))), 30000, 30010, 30020)
		}
		if edges := capture(many(variadic, span(30020, 30010, "[]"))); len(edges) != 0 {
			t.Fatal("reversed invalid source range was repaired", edges)
		}
	}
	for _, node := range []expr.Expr{
		call(catalog.MultirangeConstructorZero, false),
		call(catalog.MultirangeConstructorOne, false, literal(sqltype.Null(r))),
		call(catalog.MultirangeConstructorMany, true, literal(sqltype.Null(array))),
		many(true), many(false, literal(sqltype.Null(r)), span(30010, 30010, "[]"), span(30020, 30020, "[]")),
		many(true, literal(sqltype.Null(r)), span(30010, 30010, "[]"), span(30020, 30020, "[]")),
	} {
		if edges := capture(node); len(edges) != 0 {
			t.Fatal("NULL/empty/error constructor acquired fictitious member edges", edges)
		}
	}
	literalLeft, literalRight := rangeTypeUnitInput(t, r, "[30000,30010]"), rangeTypeUnitInput(t, r, "[30020,30030]")
	for _, variadic := range []bool{false, true} {
		assertOrder(capture(many(variadic, literal(literalRight), literal(literalLeft))), 30000, 30010, 30020, 30030)
	}
	literalMany := rangeTypeUnitInput(t, m, "{[30020,30030],[30000,30010]}")
	assertOrder(capture(literal(literalMany)), 30000, 30010, 30020, 30030)
	nullElements, err := sqltype.ArrayValue(array, []sqltype.ArrayDimension{{Lower: 1, Length: 3}}, []sqltype.Value{sqltype.Null(r), rangeTypeUnitInput(t, r, "[30010,30010]"), rangeTypeUnitInput(t, r, "[30020,30020]")})
	if err != nil {
		t.Fatal(err)
	}
	if edges := capture(call(catalog.MultirangeConstructorMany, true, literal(nullElements))); len(edges) != 0 {
		t.Fatal("NULL declared-array element became strict NULL or invented topology", edges)
	}
	independent := plan.ArrayExpr{Info: expr.Info{Usage: array.Usage()}, Elements: []expr.Expr{a, b}}
	edges := capture(independent)
	assertOrder(edges, 30000, 30010)
	assertOrder(edges, 30020, 30030)
	if reaches(edges, 30010, 30020) {
		t.Fatal("independent carrier elements became one datum", edges)
	}
	// Public codec output independently establishes source touching semantics;
	// capture itself never invokes these genuine providers or their normalization.
	for _, tc := range []struct {
		left, right string
		count       int
	}{{"[30000,30010)", "(30010,30020]", 2}, {"[30000,30010)", "[30010,30020]", 1}} {
		left, right := rangeTypeUnitInput(t, r, tc.left), rangeTypeUnitInput(t, r, tc.right)
		value, err := sqltype.MultirangeFromValues(ctx, m, []sqltype.Value{left, right})
		if err != nil || value.MultirangeLen() != tc.count {
			t.Fatal("source topology witness", tc, err)
		}
	}
}
