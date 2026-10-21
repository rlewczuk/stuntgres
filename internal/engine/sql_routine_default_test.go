package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSQLRoutineStoredDefaultValidation(t *testing.T) {
	e := New()
	run(t, e, "CREATE FUNCTION routine_unit_stored(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 1/0$$")
	run(t, e, "CREATE TABLE routine_unit_stored_default(v integer DEFAULT routine_unit_stored(1))")
	ctx := context.Background()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	raw := statement(t, "SELECT routine_unit_stored(1)").(*parser.Select).Targets[0]
	bound, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	original, ok := bound.(plan.SQLRoutineExpr)
	if !ok {
		t.Fatalf("not a SQL routine: %T", bound)
	}
	read := e.CatalogRead()
	relation := read.MustRelation(roleTableRef(t, e, "routine_unit_stored_default").Address)
	if err := (relationProvider{}).Validate(read, relation); err != nil {
		t.Fatal("valid closed SQL default rejected or evaluated", err)
	}
	if _, err := (stored.ScalarRenderer{}).SQL(ctx, read, original); err != nil {
		t.Fatal("valid stored SQL call rejected or evaluated", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*plan.SQLRoutineExpr)
	}{
		{"understated-volatility", func(n *plan.SQLRoutineExpr) { n.Property.Volatility = expr.Immutable }},
		{"invented-row-dependency", func(n *plan.SQLRoutineExpr) { n.Property.RowDependent = true }},
		{"invented-parameter-dependency", func(n *plan.SQLRoutineExpr) { n.Property.ParameterDependent = true }},
		{"invented-stage-dependency", func(n *plan.SQLRoutineExpr) { n.Property.StageDependent = true }},
		{"invented-domain-dependency", func(n *plan.SQLRoutineExpr) { n.Property.DomainDependent = true }},
		{"missing-carrier", func(n *plan.SQLRoutineExpr) { n.RoutineReference = nil }},
		{"missing-argument", func(n *plan.SQLRoutineExpr) { n.Args = nil }},
		{"nil-argument", func(n *plan.SQLRoutineExpr) { n.Args = []expr.Expr{nil} }},
		{"wrong-result", func(n *plan.SQLRoutineExpr) { n.Usage = b.Type(sqltype.Int8).Usage() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			bad := original
			test.mutate(&bad)
			if err := bad.ValidateStoredProvider(); err == nil {
				t.Fatal("forged SQL provider accepted")
			}
			if _, err := (stored.ScalarRenderer{}).SQL(ctx, read, bad); err == nil {
				t.Fatal("forged stored SQL default rendered")
			}
			candidate := *relation
			definition := relation.ColumnMeta.Defaults[1]
			definition.Expression = bad
			candidate.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: definition}
			if err := (relationProvider{}).Validate(read, &candidate); err == nil {
				t.Fatal("forged catalog SQL default accepted")
			}
		})
	}
	foreign := original
	foreign.Ref = New().catalog.Domain.Ref(original.Ref.Epoch, original.Ref.Address)
	if _, err := (stored.ScalarRenderer{}).SQL(ctx, read, foreign); err == nil {
		t.Fatal("foreign SQL lifetime accepted")
	}
}

func TestSQLRoutineDefaultClosureBoundary(t *testing.T) {
	e := New()
	run(t, e, "CREATE SEQUENCE routine_closure_seq")
	run(t, e, "CREATE FUNCTION routine_closure(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$")
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	for _, test := range []struct {
		expression string
		want       bool
	}{
		{"nextval('routine_closure_seq')", false},
		{"nextval('routine_closure_seq') + 1", false},
		{"currval('routine_closure_seq')", false},
		{"nextval(NULL)", false},
		{"routine_closure(1)", true},
		{"routine_closure(1) + 1", true},
	} {
		t.Run(test.expression, func(t *testing.T) {
			raw := statement(t, "SELECT "+test.expression).(*parser.Select).Targets[0]
			node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			if got := analyze.ClosedSQLRoutineDefault(node); got != test.want {
				t.Fatalf("SQL default closure = %v, want %v", got, test.want)
			}
		})
	}
}
