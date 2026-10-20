package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestDatetimeTruncBindingOwnership(t *testing.T) {
	utc, _ := sqltype.ResolveZone("UTC")
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{Zone: utc}, nil)
	for _, tc := range []struct {
		typ, source string
		result      sqltype.ID
		volatility  expr.Volatility
	}{
		{"timestamp", "2001-02-16 12:34:56", sqltype.Timestamp, expr.Immutable},
		{"timestamptz", "2001-02-16 12:34:56+00", sqltype.Timestamptz, expr.Stable},
		{"interval", "1 day 12:34:56", sqltype.Interval, expr.Immutable},
		{"date", "2001-02-16", sqltype.Timestamptz, expr.Stable},
		{"time", "24:00", sqltype.Interval, expr.Immutable},
	} {
		raw := datetimeMakeRaw("date_trunc", datetimeMakeString("day"), ageFiniteTyped(tc.typ, tc.source))
		node, err := b.bindFunctionProvider(raw)
		if err != nil {
			t.Fatal(tc, err)
		}
		e := node.(functions.DatetimeTrunc)
		if e.ResultUsage().Type.ID() != tc.result || e.ResultUsage().Typmod != -1 || e.Properties().Volatility != tc.volatility || plannedVolatility(e) != tc.volatility || generationImmutable(e) != (tc.volatility == expr.Immutable) {
			t.Fatal(tc, e.Properties(), e.ResultUsage())
		}
		if tc.typ == "time" && e.Args[1].ResultUsage().Type.ID() != sqltype.Time {
			t.Fatal("selected TIME conversion lost")
		}
		var children []expr.Expr
		boundExpressionChildren(e, func(child expr.Expr) { children = append(children, child) })
		if len(children) != 2 {
			t.Fatal(children)
		}
		if !b.sameGroupExpression(e, e) {
			t.Fatal("structural equality")
		}
		replacements := map[sqltype.ID]*sqltype.Descriptor{}
		for _, d := range sqltype.Builtins() {
			replacements[d.ID()] = d
		}
		next, changed, err := retagBoundTypes(New().CatalogRead(), e, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacements[d.ID()] })
		if err != nil || !changed {
			t.Fatal(next, changed, err)
		}
		rebound := next.(functions.DatetimeTrunc)
		if rebound.ResultUsage().Type != replacements[tc.result] {
			t.Fatal("retained result authority")
		}
		v, err := rebound.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background(), Input: sqltype.InputEnvironment{Zone: utc}}})
		if err != nil || v.Type() != replacements[tc.result] {
			t.Fatal(v, err)
		}
	}
}

func TestDatetimeTruncInferenceStrictChildren(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	raw := datetimeMakeRaw("date_trunc", parser.ParameterExpr{Number: 1}, parser.ParameterExpr{Number: 2}, parser.ParameterExpr{Number: 3})
	node, err := b.bindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	if b.params.parameters[1].Type.ID() != sqltype.Text || b.params.parameters[2].Type.ID() != sqltype.Timestamptz || b.params.parameters[3].Type.ID() != sqltype.Text || !node.Properties().ParameterDependent || plannedVolatility(node) != expr.Immutable {
		t.Fatal(node.Properties(), b.params.parameters)
	}
	e := node.(functions.DatetimeTrunc)
	for _, i := range []int{0, 1, 2} {
		n := e
		n.Args[i] = literalExpression{Info: b.info(n.Args[i].ResultUsage(), parser.Position{}, expr.Properties{Volatility: expr.Immutable}), value: sqltype.Null(n.Args[i].ResultUsage().Type)}
		simplified, changed, err := simplifyConditional(&EvalContext{evalStatement: evalStatement{Context: context.Background()}}, n, false)
		if err != nil || !changed || plannedVolatility(simplified) != expr.Immutable || !generationImmutable(simplified) {
			t.Fatal(simplified, changed, err)
		}
	}
	for _, tc := range []struct {
		source parser.Expr
		zone   bool
		code   string
	}{
		{datetimeMakeString("2001-01-01"), false, "42725"},
		{ageFiniteTyped("text", "2001-01-01"), false, "42883"},
		{ageFiniteTyped("timestamp", "2001-01-01"), true, "0A000"},
		{ageFiniteTyped("date", "2001-01-01"), true, "0A000"},
		{ageFiniteTyped("interval", "1 day"), true, "42883"},
	} {
		args := []parser.Expr{datetimeMakeString("day"), tc.source}
		if tc.zone {
			args = append(args, datetimeMakeString("UTC"))
		}
		_, err := b.bindFunctionProvider(datetimeMakeRaw("date_trunc", args...))
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
			t.Fatal(tc, err)
		}
	}
}
