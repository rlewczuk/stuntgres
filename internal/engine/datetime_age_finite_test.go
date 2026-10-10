package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func ageFiniteTyped(typ, value string) parser.Expr {
	return parser.CastExpr{Child: datetimeMakeString(value), TypeName: parser.TypeName{Name: typ}}
}

func TestDatetimeAgeFiniteBindingPropertiesAndRemap(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	for _, tc := range []struct {
		name       string
		args       []parser.Expr
		result     sqltype.ID
		volatility Volatility
	}{
		{"age", []parser.Expr{ageFiniteTyped("timestamp", "2000-01-01")}, sqltype.Interval, Stable},
		{"age", []parser.Expr{ageFiniteTyped("timestamp", "2004-06-01"), ageFiniteTyped("date", "2004-04-30")}, sqltype.Interval, Immutable},
		{"isfinite", []parser.Expr{ageFiniteTyped("date", "infinity")}, sqltype.Bool, Immutable},
		{"isfinite", []parser.Expr{ageFiniteTyped("timestamp", "infinity")}, sqltype.Bool, Immutable},
		{"isfinite", []parser.Expr{ageFiniteTyped("interval", "infinity")}, sqltype.Bool, Immutable},
	} {
		node, err := b.bindDatetimeAgeFinite(datetimeMakeRaw(tc.name, tc.args...), true)
		if err != nil {
			t.Fatal(tc, err)
		}
		e := node.(datetimeAgeFiniteExpression)
		if e.ResultUsage().Type.ID() != tc.result || e.ResultUsage().Typmod != -1 || e.Properties().Volatility != tc.volatility || plannedVolatility(e) != tc.volatility || generationImmutable(e) != (tc.volatility == Immutable) {
			t.Fatal(tc, e.Properties())
		}
		if !b.sameGroupExpression(e, e) {
			t.Fatal("lost structural identity")
		}
		replacements := map[sqltype.ID]*sqltype.Descriptor{}
		for _, d := range sqltype.Builtins() {
			replacements[d.ID()] = d
		}
		next, changed, err := retagBoundTypes(e, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacements[d.ID()] })
		if err != nil || !changed {
			t.Fatal(next, changed, err)
		}
		rebound := next.(datetimeAgeFiniteExpression)
		if rebound.ResultUsage().Type != replacements[tc.result] || rebound.ResultUsage().Type == e.ResultUsage().Type {
			t.Fatal("retained result authority")
		}
		for i, arg := range rebound.children() {
			if arg.ResultUsage().Type == e.args[i].ResultUsage().Type {
				t.Fatal("retained child authority")
			}
		}
		zone, err := sqltype.ResolveZone("UTC")
		if err != nil {
			t.Fatal(err)
		}
		_, err = rebound.Eval(&EvalContext{Context: context.Background(), Input: sqltype.InputEnvironment{Zone: zone, TransactionStart: time.Date(2000, 1, 1, 12, 0, 0, 0, time.UTC)}})
		if err != nil {
			t.Fatal("retagged evaluation", err)
		}
	}
	parameter := parser.ParameterExpr{Number: 1}
	node, err := b.bindDatetimeAgeFinite(datetimeMakeRaw("age", ageFiniteTyped("timestamp", "2004-06-01"), parameter), true)
	if err != nil {
		t.Fatal(err)
	}
	if b.parameters[1].Type.ID() != sqltype.Timestamp || !node.Properties().ParameterDependent {
		t.Fatal(b.parameters, node.Properties())
	}
}

func TestDatetimeAgeFiniteResolutionFailures(t *testing.T) {
	for _, tc := range []struct {
		raw                 parser.FunctionExpr
		code, message, hint string
	}{
		{datetimeMakeRaw("age", datetimeMakeString("2000-01-01")), "42725", "function age(unknown) is not unique", "Could not choose a best candidate function. You might need to add explicit type casts."},
		{datetimeMakeRaw("isfinite", parser.ParameterExpr{Number: 1}), "42725", "function isfinite(unknown) is not unique", "Could not choose a best candidate function. You might need to add explicit type casts."},
		{datetimeMakeRaw("age", datetimeMakeInteger(1)), "42883", "function age(integer) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts."},
		{datetimeMakeRaw("isfinite", ageFiniteTyped("text", "infinity")), "42883", "function isfinite(text) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts."},
		{datetimeMakeRaw("age", ageFiniteTyped("date", "2000-01-01")), "0A000", "age with time zone is unsupported", ""},
		{datetimeMakeRaw("age", datetimeMakeString("2004-06-01"), datetimeMakeString("2004-04-30")), "0A000", "age with time zone is unsupported", ""},
		{datetimeMakeRaw("isfinite", ageFiniteTyped("timestamptz", "infinity")), "0A000", "isfinite with time zone is unsupported", ""},
		{datetimeMakeRaw("isfinite", ageFiniteTyped("time", "12:00")), "0A000", "time to interval conversion is unsupported", ""},
	} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		_, err := b.bindDatetimeAgeFinite(tc.raw, true)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != tc.code || d.Message != tc.message || d.Hint != tc.hint || d.Position != "8" {
			t.Fatal(tc, err)
		}
	}
}

func TestDatetimeAgeUnaryRetagsHiddenDateAuthority(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	node, err := b.bindDatetimeAgeFinite(datetimeMakeRaw("age", ageFiniteTyped("timestamp", "2000-01-01")), true)
	if err != nil {
		t.Fatal(err)
	}
	var date *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Date {
			date = d
		}
	}
	next, changed, err := retagBoundTypes(node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d.ID() == sqltype.Date {
			return date
		}
		return d
	})
	if err != nil || !changed || next.ResultUsage() != node.ResultUsage() {
		t.Fatal("hidden current-date authority was not remapped", next, changed, err)
	}
	zone, err := sqltype.ResolveZone("UTC")
	if err != nil {
		t.Fatal(err)
	}
	value, err := next.Eval(&EvalContext{Context: context.Background(), Input: sqltype.InputEnvironment{Zone: zone, TransactionStart: time.Date(2000, 1, 1, 12, 0, 0, 0, time.UTC)}})
	if err != nil {
		t.Fatal(err)
	}
	months, days, micros, err := value.Interval()
	if err != nil || months != 0 || days != 0 || micros != 0 {
		t.Fatal(value, err)
	}
}
