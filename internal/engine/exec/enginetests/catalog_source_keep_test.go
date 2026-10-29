package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// TestCatalogSourceKeepFilterShapes checks which filters let a catalog
// producer skip rows, and that a skipped cell is one the filter's first
// conjunct makes FALSE.
func TestCatalogSourceKeepFilterShapes(t *testing.T) {
	read := engine.New().CatalogRead()
	lookup := func(id sqltype.ID) *sqltype.Descriptor {
		d, ok := read.LookupType(id)
		if !ok {
			t.Fatal("missing type", id)
		}
		return d
	}
	text, boolean, integer := lookup(sqltype.Text), lookup(sqltype.Bool), lookup(sqltype.Int4)
	usage := text.Usage()
	equal, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, text, text, boolean, usage.Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	domain := &ids.Domain{}
	ref := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 16202})
	other := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 16203})
	// Bound columns name their attribute in the origin's SubID.
	attribute := func(r ids.ObjectRef, number int32) ids.ObjectRef {
		return domain.Ref(r.Epoch, ids.ObjectAddress{ClassID: r.Address.ClassID, ObjectID: r.Address.ObjectID, SubID: number})
	}
	info := expr.Info{Usage: usage}
	truth := expr.Info{Usage: boolean.Usage()}
	name := plan.ColumnExpr{Info: info, Origin: attribute(ref, 1), Scope: 1, Frame: 1}
	literal := func(s string) expr.Expr { return plan.LiteralExpr{Info: info, Value: sqltype.String(text, s)} }
	eq := func(left, right expr.Expr) expr.Expr {
		return plan.ComparisonExpr{Info: truth, Left: left, Right: right, Predicate: equal, Operator: "="}
	}
	in := func(items ...expr.Expr) expr.Expr {
		return plan.ScalarArrayComparisonExpr{Info: truth, Left: name, Items: items, Predicate: equal, Operator: "="}
	}
	logical := func(op parser.LogicalOperator, left, right expr.Expr) expr.Expr {
		return plan.LogicalExpr{Info: truth, Op: op, Left: left, Right: right}
	}
	scan := func() *plan.SourceNode { return &plan.SourceNode{Ref: ref} }
	filtered := func(predicate expr.Expr) plan.Node { return &plan.FilterNode{Child: scan(), Predicate: predicate} }
	unrelated := eq(plan.ColumnExpr{Info: info, Origin: attribute(ref, 2), Scope: 1, Frame: 1, Slot: 1}, literal("on"))
	seven, err := sqltype.Integer(integer, 7)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		root   plan.Node
		keep   []string // nil: no test
		reject []string
	}{
		{"equality", filtered(eq(name, literal("work_mem"))), []string{"work_mem"}, []string{"Work_mem", "work_mem ", "role"}},
		{"reversed equality", filtered(eq(literal("role"), name)), []string{"role"}, []string{"work_mem"}},
		{"list", filtered(in(literal("role"), literal("work_mem"))), []string{"role", "work_mem"}, []string{"search_path"}},
		{"first conjunct", filtered(logical(parser.LogicalAnd, logical(parser.LogicalAnd, eq(name, literal("role")), unrelated), unrelated)), []string{"role"}, []string{"work_mem"}},
		{"later conjunct", filtered(logical(parser.LogicalAnd, unrelated, eq(name, literal("role")))), nil, nil},
		{"disjunction", filtered(logical(parser.LogicalOr, eq(name, literal("role")), unrelated)), nil, nil},
		{"NULL probe", filtered(eq(name, plan.LiteralExpr{Info: info, Value: sqltype.Null(text)})), nil, nil},
		{"NULL list item", filtered(in(literal("role"), plan.LiteralExpr{Info: info, Value: sqltype.Null(text)})), nil, nil},
		{"other column", filtered(unrelated), nil, nil},
		{"other occurrence", filtered(eq(plan.ColumnExpr{Info: info, Origin: attribute(ref, 1), Scope: 1, Frame: 1, QueryOccurrence: 2}, literal("role"))), nil, nil},
		{"outer column", filtered(eq(plan.ColumnExpr{Info: info, Origin: attribute(ref, 1), Scope: 2, Frame: 1}, literal("role"))), nil, nil},
		{"other relation", filtered(eq(plan.ColumnExpr{Info: info, Origin: attribute(other, 1), Scope: 1, Frame: 1}, literal("role"))), nil, nil},
		{"non-text probe", filtered(eq(name, plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: seven})), nil, nil},
		{"not equality", filtered(plan.ComparisonExpr{Info: truth, Left: name, Right: literal("role"), Predicate: equal, Operator: "<"}), nil, nil},
		{"filter above projection", &plan.FilterNode{Child: &plan.ProjectNode{Child: scan()}, Predicate: eq(name, literal("role"))}, nil, nil},
		{"two scans", &plan.JoinNode{Left: filtered(eq(name, literal("role"))), Right: scan()}, nil, nil},
		{"unfiltered", scan(), nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			keep := exec.CatalogSourceKeep(context.Background(), nil, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, tc.root, 0, ref, 0)
			if tc.keep == nil {
				if keep != nil {
					t.Fatal("unexpected row test")
				}
				return
			}
			if keep == nil {
				t.Fatal("missing row test")
			}
			for _, s := range tc.keep {
				if !keep(sqltype.String(text, s)) {
					t.Fatal("rejected a matching cell", s)
				}
			}
			for _, s := range tc.reject {
				if keep(sqltype.String(text, s)) {
					t.Fatal("kept a cell the filter rejects", s)
				}
			}
			if !keep(sqltype.Null(text)) {
				t.Fatal("rejected a NULL cell, for which the conjunct is not FALSE")
			}
		})
	}
}

// TestCatalogSourceKeepOIDProbes checks oid key probes: an oid literal or
// parameter, a bound regclass or regprocedure literal (the binder stores its
// OID) under a cast to oid, or an integer literal cast to oid. A text value
// cast to regclass reads the input environment's resolver, so it yields no
// test.
func TestCatalogSourceKeepOIDProbes(t *testing.T) {
	read := engine.New().CatalogRead()
	lookup := func(id sqltype.ID) *sqltype.Descriptor {
		d, ok := read.LookupType(id)
		if !ok {
			t.Fatal("missing type", id)
		}
		return d
	}
	oid, regclass, text, boolean := lookup(sqltype.Oid), lookup(sqltype.Regclass), lookup(sqltype.Text), lookup(sqltype.Bool)
	regprocedure, int4 := lookup(sqltype.Regprocedure), lookup(sqltype.Int4)
	equal, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, oid, oid, boolean, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	toOID, err := sqltype.PlanCoercion(regclass.Usage(), oid.Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	toRegclass, err := sqltype.PlanCoercion(text.Usage(), regclass.Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	routineToOID, err := sqltype.PlanCoercion(regprocedure.Usage(), oid.Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	integerToOID, err := sqltype.PlanCoercion(int4.Usage(), oid.Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	domain := &ids.Domain{}
	ref := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1249})
	column := plan.ColumnExpr{Info: expr.Info{Usage: oid.Usage()}, Origin: domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1249, SubID: 1}), Scope: 1, Frame: 1}
	cell := func(n uint32) sqltype.Value {
		v, err := sqltype.OIDValue(oid, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	filtered := func(probe expr.Expr) plan.Node {
		return &plan.FilterNode{Child: &plan.SourceNode{Ref: ref}, Predicate: plan.ComparisonExpr{Info: expr.Info{Usage: boolean.Usage()}, Left: column, Right: probe, Predicate: equal, Operator: "="}}
	}
	oidLiteral := plan.LiteralExpr{Info: expr.Info{Usage: oid.Usage()}, Value: cell(16384)}
	boundRegclass := plan.CoercionExpr{Info: expr.Info{Usage: oid.Usage()}, Child: plan.LiteralExpr{Info: expr.Info{Usage: regclass.Usage()}, Value: sqltype.String(regclass, "16384")}, Plan: toOID}
	parameter := plan.ParameterExpr{Info: expr.Info{Usage: oid.Usage()}, Number: 1}
	routine, err := sqltype.RegprocValue(regprocedure, 16384)
	if err != nil {
		t.Fatal(err)
	}
	boundRegprocedure := plan.CoercionExpr{Info: expr.Info{Usage: oid.Usage()}, Child: plan.LiteralExpr{Info: expr.Info{Usage: regprocedure.Usage()}, Value: routine}, Plan: routineToOID}
	number, err := sqltype.Integer(int4, 16384)
	if err != nil {
		t.Fatal(err)
	}
	integerLiteral := plan.CoercionExpr{Info: expr.Info{Usage: oid.Usage()}, Child: plan.LiteralExpr{Info: expr.Info{Usage: int4.Usage()}, Value: number}, Plan: integerToOID}
	integerParameter := plan.CoercionExpr{Info: expr.Info{Usage: oid.Usage()}, Child: plan.ParameterExpr{Info: expr.Info{Usage: int4.Usage()}, Number: 1}, Plan: integerToOID}
	textRegclass := plan.CoercionExpr{Info: expr.Info{Usage: oid.Usage()}, Plan: toOID,
		Child: plan.CoercionExpr{Info: expr.Info{Usage: regclass.Usage()}, Child: plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "t")}, Plan: toRegclass}}
	for _, tc := range []struct {
		name  string
		probe expr.Expr
		keeps bool
	}{{"oid literal", oidLiteral, true}, {"bound regclass literal", boundRegclass, true}, {"bound regprocedure literal", boundRegprocedure, true}, {"integer literal", integerLiteral, true}, {"oid parameter", parameter, true}, {"integer parameter", integerParameter, false}, {"text cast to regclass", textRegclass, false}} {
		t.Run(tc.name, func(t *testing.T) {
			keep := exec.CatalogSourceKeep(context.Background(), []sqltype.Value{cell(16384)}, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, filtered(tc.probe), 0, ref, 0)
			if !tc.keeps {
				if keep != nil {
					t.Fatal("unexpected row test")
				}
				return
			}
			if keep == nil {
				t.Fatal("missing row test")
			}
			if !keep(cell(16384)) || keep(cell(16385)) || keep(cell(1259)) || !keep(sqltype.Null(oid)) {
				t.Fatal("row test does not match the oid conjunct")
			}
		})
	}
}
