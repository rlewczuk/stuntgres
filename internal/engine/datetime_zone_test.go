package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

type datetimeZoneSignature struct {
	name   string
	args   [3]sqltype.ID
	count  int
	result sqltype.ID
}

var datetimeZoneSignatures = [...]datetimeZoneSignature{
	{"timezone", [3]sqltype.ID{sqltype.Text, sqltype.Timestamp}, 2, sqltype.Timestamptz},
	{"timezone", [3]sqltype.ID{sqltype.Interval, sqltype.Timestamp}, 2, sqltype.Timestamptz},
	{"timezone", [3]sqltype.ID{sqltype.Text, sqltype.Timestamptz}, 2, sqltype.Timestamp},
	{"timezone", [3]sqltype.ID{sqltype.Interval, sqltype.Timestamptz}, 2, sqltype.Timestamp},
	{"timezone", [3]sqltype.ID{sqltype.Text, sqltype.Timetz}, 2, sqltype.Timetz},
	{"timezone", [3]sqltype.ID{sqltype.Interval, sqltype.Timetz}, 2, sqltype.Timetz},
	{"timezone", [3]sqltype.ID{sqltype.Timestamp}, 1, sqltype.Timestamptz},
	{"timezone", [3]sqltype.ID{sqltype.Timestamptz}, 1, sqltype.Timestamp},
	{"timezone", [3]sqltype.ID{sqltype.Timetz}, 1, sqltype.Timetz},
	{"date_add", [3]sqltype.ID{sqltype.Timestamptz, sqltype.Interval}, 2, sqltype.Timestamptz},
	{"date_add", [3]sqltype.ID{sqltype.Timestamptz, sqltype.Interval, sqltype.Text}, 3, sqltype.Timestamptz},
	{"date_subtract", [3]sqltype.ID{sqltype.Timestamptz, sqltype.Interval}, 2, sqltype.Timestamptz},
	{"date_subtract", [3]sqltype.ID{sqltype.Timestamptz, sqltype.Interval, sqltype.Text}, 3, sqltype.Timestamptz},
}

func TestDatetimeZoneAllSignaturesAndRetag(t *testing.T) {
	env := sqltype.InputEnvironment{}
	env.Zone, _ = sqltype.ResolveZone("UTC")
	env.TransactionStart = time.Date(2020, 7, 1, 12, 0, 0, 0, time.UTC)
	values := map[sqltype.ID]parser.Expr{
		sqltype.Text:        ageFiniteTyped("text", "UTC"),
		sqltype.Interval:    ageFiniteTyped("interval", "1 hour"),
		sqltype.Timestamp:   ageFiniteTyped("timestamp", "2004-01-01 12:00"),
		sqltype.Timestamptz: ageFiniteTyped("timestamptz", "2004-01-01 12:00+00"),
		sqltype.Timetz:      ageFiniteTyped("timetz", "12:00+00"),
	}
	for _, signature := range datetimeZoneSignatures {
		b := newTestExpressionBinder(t, context.Background(), New(), env, nil)
		args := make([]parser.Expr, signature.count)
		for i := range args {
			args[i] = values[signature.args[i]]
		}
		node, err := b.bindFunctionProvider(datetimeMakeRaw(signature.name, args...))
		if err != nil {
			t.Fatal(signature, err)
		}
		want := expr.Immutable
		if signature.name != "timezone" && signature.count == 2 || signature.name == "timezone" && (signature.count == 1 || signature.args[0] == sqltype.Text && signature.args[1] == sqltype.Timetz) {
			want = expr.Stable
		}
		if node.ResultUsage().Type.ID() != signature.result || node.ResultUsage().Typmod != -1 || plannedVolatility(node) != want || generationImmutable(node) != (want == expr.Immutable) {
			t.Fatal(signature, node.Properties())
		}
		if !b.sameGroupExpression(node, node) {
			t.Fatal("structural identity", signature)
		}
		replacements := map[sqltype.ID]*sqltype.Descriptor{}
		for _, descriptor := range sqltype.Builtins() {
			replacements[descriptor.ID()] = descriptor
		}
		rebound, changed, err := retagBoundTypes(New().CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacements[d.ID()] })
		if err != nil || !changed || rebound.ResultUsage().Type != replacements[signature.result] {
			t.Fatal("remap", signature, rebound, changed, err)
		}
		value, err := rebound.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background(), Input: env}})
		if err != nil || value.Type() != rebound.ResultUsage().Type {
			t.Fatal("remapped provider", signature, value, err)
		}
		seen := 0
		boundExpressionChildren(rebound, func(expr.Expr) { seen++ })
		if seen != signature.count {
			t.Fatal("closed children", signature, seen)
		}
	}
}

func TestDatetimeZonePreferredUnknownAndFailures(t *testing.T) {
	for _, args := range [][]parser.Expr{
		{parser.ParameterExpr{Number: 1}},
		{parser.ParameterExpr{Number: 1}, parser.ParameterExpr{Number: 2}},
	} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		node, err := b.bindFunctionProvider(datetimeMakeRaw("timezone", args...))
		if err != nil || node.ResultUsage().Type.ID() != sqltype.Timestamp {
			t.Fatal(node, err)
		}
		if len(args) == 1 {
			if b.params.parameters[1].Type.ID() != sqltype.Timestamptz {
				t.Fatal(b.params.parameters)
			}
		} else if b.params.parameters[1].Type.ID() != sqltype.Text || b.params.parameters[2].Type.ID() != sqltype.Timestamptz {
			t.Fatal(b.params.parameters)
		}
	}
	for _, name := range []string{"date_add", "date_subtract"} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		_, err := b.bindFunctionProvider(datetimeMakeRaw(name, parser.ParameterExpr{Number: 1}, parser.ParameterExpr{Number: 2}, parser.ParameterExpr{Number: 3}))
		if err != nil || b.params.parameters[1].Type.ID() != sqltype.Timestamptz || b.params.parameters[2].Type.ID() != sqltype.Interval || b.params.parameters[3].Type.ID() != sqltype.Text {
			t.Fatal(b.params.parameters, err)
		}
	}
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	_, err := b.bindFunctionProvider(datetimeMakeRaw("timezone", datetimeMakeInteger(1), ageFiniteTyped("timestamp", "2000-01-01")))
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42883" || diagnostic.Message != "function timezone(integer, timestamp without time zone) does not exist" || diagnostic.Position != "8" {
		t.Fatal(err)
	}
}

func TestDatetimeZoneStrictConstantNullAndPrecancellation(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	node, err := b.bindFunctionProvider(datetimeMakeRaw("date_add", ageFiniteTyped("timestamptz", "2004-01-01+00"), ageFiniteTyped("interval", "1 day"), ageFiniteTyped("text", "No/Such_Zone")))
	if err != nil {
		t.Fatal(err)
	}
	expression := node.(functions.DatetimeZoneCalendar)
	expression.Args[0] = literalExpression{expr.InfoOf(expression.Args[0]), sqltype.Null(expression.Args[0].ResultUsage().Type)}
	ctx := &EvalContext{evalStatement: evalStatement{Context: context.Background()}}
	simplified, changed, err := preflightCoercions(ctx, expression)
	if err != nil || !changed {
		t.Fatal(simplified, changed, err)
	}
}

func TestDatetimeZoneSelectedCastDiagnosticAndScopeRestoration(t *testing.T) {
	parameter := parser.ParameterExpr{Number: 1}
	stamp := parser.CastExpr{Child: parameter, TypeName: parser.TypeName{Name: "timestamptz"}}
	span := parser.CastExpr{Child: parameter, TypeName: parser.TypeName{Name: "interval"}, Pos: parser.Position{Character: 35}}
	for _, nested := range []bool{false, true} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		var source parser.Expr = stamp
		if nested {
			source = datetimeMakeRaw("date_add", stamp, ageFiniteTyped("interval", "0"))
		}
		_, err := b.bindFunctionProvider(datetimeMakeRaw("date_add", source, span))
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42846" || diagnostic.Message != "cannot cast type timestamp with time zone to interval" || diagnostic.Position != "35" || b.consumer.datetimeZoneOperands {
			t.Fatal(nested, err, b.consumer.datetimeZoneOperands)
		}
		// The same failed pair outside selected analysis retains the existing
		// adjacent unsupported-cast boundary; no new coercion was admitted.
		_, err = b.cast(span, expressionPolicy{context: projectionContext})
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || b.consumer.datetimeZoneOperands {
			t.Fatal("scope leaked", err, b.consumer.datetimeZoneOperands)
		}
	}
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	_, err := b.bindFunctionProvider(datetimeMakeRaw("date_add", ageFiniteTyped("timestamptz", "2004-01-01+00"), ageFiniteTyped("interval", "0")))
	if err != nil || b.consumer.datetimeZoneOperands {
		t.Fatal("successful analysis leaked scope", err, b.consumer.datetimeZoneOperands)
	}
}
