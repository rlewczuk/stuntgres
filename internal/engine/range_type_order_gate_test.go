package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestRangeTypeOrderingConstraintGate(t *testing.T) {
	view, r, m := rangeTypeUnitFamily(t, sqltype.Regclass, false)
	definition, _ := r.RangeDefinition()
	lower, upper := sqltype.String(definition.Subtype.Type, "10"), sqltype.String(definition.Subtype.Type, "20")
	makeRange := func(lo, hi sqltype.Value) sqltype.Value {
		value, err := sqltype.RangeFromValues(context.Background(), r, lo, hi, "[]")
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	one, two := makeRange(lower, lower), makeRange(upper, upper)
	finite := makeRange(lower, upper)
	multi, err := sqltype.MultirangeFromValues(context.Background(), m, []sqltype.Value{one, two})
	if err != nil {
		t.Fatal(err)
	}
	array, _ := view.LookupArray(r)
	separate, err := sqltype.ArrayValue(array, []sqltype.ArrayDimension{{Lower: 1, Length: 2}}, []sqltype.Value{one, two})
	if err != nil {
		t.Fatal(err)
	}
	relation := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 930000}
	for _, tc := range []struct {
		name   string
		values []sqltype.Value
		want   bool
	}{
		{"unused_family", nil, false},
		{"independent_singleton_rows", []sqltype.Value{one, two}, false},
		{"independent_array_singletons", []sqltype.Value{separate}, false},
		{"null", []sqltype.Value{sqltype.Null(r)}, false},
		{"unbounded", []sqltype.Value{makeRange(sqltype.Null(definition.Subtype.Type), sqltype.Null(definition.Subtype.Type))}, false},
		{"finite_bounds", []sqltype.Value{finite}, true},
		{"cross_member_order", []sqltype.Value{multi}, true},
	} {
		var rows []storage.RowVersion
		for _, value := range tc.values {
			rows = append(rows, storage.RowVersion{Values: []sqltype.Value{value}})
		}
		actual, err := stored.RangeIdentityConstraints(context.Background(), view.State(), map[ids.ObjectAddress]*storage.Relation{relation: {Versions: rows}})
		if err != nil || (len(actual) != 0) != tc.want {
			t.Fatal(tc.name, "actual ordering constraints", actual, tc.want, err)
		}
	}
	provider := &catalog.RangeConstructor{Type: r, Form: catalog.RangeConstructorTwo}
	signature, err := provider.Signature(view)
	if err != nil {
		t.Fatal(err)
	}
	owner := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.ID())}).Owner
	procedure := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 930001}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 2, ObjectName: r.Name(), Signature: signature, RoutineKind: catalog.FunctionRoutine, Result: ids.OID(r.ID()), Owner: owner, Volatility: expr.Immutable, RangeConstructor: provider, ACL: authorization.DefaultBaseline(owner, authorization.DefaultRoutines, false)}
	view.State().SetDescriptor(procedure.Address(), procedure)
	if err := catalog.ValidateRangeConstructor(view, procedure); err != nil {
		t.Fatal(err)
	}
	literal := func(value sqltype.Value) expr.Expr {
		return plan.LiteralExpr{Info: expr.Info{Usage: value.Type().Usage()}, Value: value}
	}
	for _, hi := range []sqltype.Value{lower, upper} {
		constructor := plan.RangeConstructorExpr{Info: expr.Info{Usage: r.Usage(), RoutineReference: procedure}, Procedure: procedure, Ref: view.Owner().Domain.Ref(view.Epoch(), procedure.Address()), Args: []expr.Expr{literal(lower), literal(hi)}}
		if err := constructor.ValidateStoredProvider(); err != nil {
			t.Fatal(err)
		}
		descriptor := &catalog.Relation{ObjectAddress: relation, ObjectRevision: 1, ObjectName: "captured", RelationKind: catalog.TableRelation, ColumnMeta: catalog.ColumnMetadata{Defaults: map[int32]catalog.ColumnDefault{1: {Expression: constructor}}}}
		state := catalog.CloneState(view.State())
		state.SetDescriptor(relation, descriptor)
		actual, err := stored.RangeIdentityConstraints(context.Background(), state, nil)
		if err != nil || (len(actual) != 0) != !sqltype.SameDatum(lower, hi) {
			t.Fatal("stored constructor ordering constraint", actual, err)
		}
	}
}

func TestRangeTypeAllAliasConstraintComponents(t *testing.T) {
	view, r, m := rangeTypeUnitFamily(t, sqltype.Regclass, false)
	state := catalog.CloneState(view.State())
	for _, id := range []ids.OID{95001, 95011, 95021, 95031} {
		relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, ObjectRevision: 1, ObjectName: "endpoint", RelationKind: catalog.TableRelation}
		state.SetDescriptor(relation.Address(), relation)
	}
	first := rangeTypeUnitInput(t, r, "[95001,95011]")
	second := rangeTypeUnitInput(t, r, "[95021,95031]")
	multi, err := sqltype.MultirangeFromValues(context.Background(), m, []sqltype.Value{first, second})
	if err != nil {
		t.Fatal(err)
	}
	array, _ := view.LookupArray(r)
	separate, err := sqltype.ArrayValue(array, []sqltype.ArrayDimension{{Lower: 1, Length: 2}}, []sqltype.Value{first, second})
	if err != nil {
		t.Fatal(err)
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 95050}
	rows := map[ids.ObjectAddress]*storage.Relation{address: {Versions: []storage.RowVersion{{Values: []sqltype.Value{first}}, {Values: []sqltype.Value{second, separate}}}}}
	edges, err := stored.RangeIdentityConstraints(context.Background(), state, rows)
	if err != nil || len(edges) != 2 {
		t.Fatal("all independent row/carrier components, no cross-element edge", edges, err)
	}
	rows[address].Versions = append(rows[address].Versions, storage.RowVersion{Values: []sqltype.Value{multi}})
	edges, err = stored.RangeIdentityConstraints(context.Background(), state, rows)
	if err != nil || len(edges) != 3 {
		t.Fatal("multirange adds its actual cross-member edge", edges, err)
	}
	for _, edge := range edges {
		if edge.Lower.Object.ClassID != ids.RelationClassID || edge.Upper.Object.ClassID != ids.RelationClassID {
			t.Fatal("lost actual object provenance", edge)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := stored.RangeIdentityConstraints(ctx, state, rows); err != context.Canceled {
		t.Fatal("constraint capture lost cancellation", err)
	}
	rangeTypeUnitOutput(t, first, "[95001,95011]")
	rangeTypeUnitOutput(t, second, "[95021,95031]")
}

func TestRangeTypeCapturedCompanionConstraints(t *testing.T) {
	view, r, m := rangeTypeUnitFamily(t, sqltype.Regclass, false)
	subtype, _ := r.RangeDefinition()
	lo, hi := sqltype.String(subtype.Subtype.Type, "10"), sqltype.String(subtype.Subtype.Type, "20")
	literal := func(v sqltype.Value) expr.Expr {
		return plan.LiteralExpr{Info: expr.Info{Usage: v.Type().Usage()}, Value: v}
	}
	procedure := func(form catalog.RangeConstructorForm, typ *sqltype.Descriptor, id ids.OID) *catalog.Procedure {
		provider := &catalog.RangeConstructor{Type: typ, Form: form}
		signature, err := provider.Signature(view)
		if err != nil {
			t.Fatal(err)
		}
		owner := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}).Owner
		p := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 2, ObjectName: typ.Name(), Signature: signature, RoutineKind: catalog.FunctionRoutine, Result: ids.OID(typ.ID()), Owner: owner, Volatility: expr.Immutable, Strict: form >= catalog.MultirangeConstructorZero, RangeConstructor: provider, ACL: authorization.DefaultBaseline(owner, authorization.DefaultRoutines, false)}
		view.State().SetDescriptor(p.Address(), p)
		if err := catalog.ValidateRangeConstructor(view, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	rp := procedure(catalog.RangeConstructorTwo, r, 930101)
	mp := procedure(catalog.MultirangeConstructorMany, m, 930102)
	closed := procedure(catalog.RangeConstructorThree, r, 930104)
	text, _ := view.LookupType(sqltype.Text)
	call := func(p *catalog.Procedure, args ...expr.Expr) plan.RangeConstructorExpr {
		n := plan.RangeConstructorExpr{Info: expr.Info{Usage: p.RangeConstructor.Type.Usage(), RoutineReference: p}, Procedure: p, Ref: view.Owner().Domain.Ref(view.Epoch(), p.Address()), Args: args, Variadic: p == mp}
		if err := n.ValidateStoredProvider(); err != nil {
			t.Fatal(err)
		}
		return n
	}
	array, _ := view.LookupArray(r)
	for _, mode := range []string{"finite", "empty_member", "null_array"} {
		var second expr.Expr = call(closed, literal(hi), literal(hi), literal(sqltype.String(text, "[]")))
		if mode == "empty_member" {
			second = call(rp, literal(hi), literal(hi))
		}
		var arg expr.Expr = plan.ArrayExpr{Info: expr.Info{Usage: array.Usage()}, Elements: []expr.Expr{call(rp, literal(sqltype.Null(lo.Type())), literal(lo)), second}}
		if mode == "null_array" {
			arg = literal(sqltype.Null(array))
		}
		root := call(mp, arg)
		address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 930103}
		holder := &catalog.Relation{ObjectAddress: address, ObjectRevision: 1, ObjectName: "captured_many", RelationKind: catalog.TableRelation, ColumnMeta: catalog.ColumnMetadata{Defaults: map[int32]catalog.ColumnDefault{1: {Expression: root}}}}
		state := catalog.CloneState(view.State())
		state.SetDescriptor(address, holder)
		edges, err := stored.RangeIdentityConstraints(context.Background(), state, nil)
		if err != nil || (mode == "finite" && len(edges) != 1) || (mode != "finite" && len(edges) != 0) {
			t.Fatal("only actual cross-member finite bounds in structural array captures", mode, edges, err)
		}
	}
}
