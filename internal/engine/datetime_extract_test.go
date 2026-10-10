package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestDatetimeExtractBindingPropertiesAndRemap(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	for _, source := range []string{"date", "time", "timetz", "timestamp", "timestamptz", "interval"} {
		value := "2000-01-01"
		switch source {
		case "time":
			value = "12:00"
		case "timetz":
			value = "12:00+05:45"
		case "interval":
			value = "1 year 2 months 3 days 4 hours"
		}
		for _, api := range []string{"extract", "date_part"} {
			raw := datetimeMakeRaw(api, datetimeMakeString("epoch"), ageFiniteTyped(source, value))
			node, err := b.bindDatetimeExtract(raw, true)
			if err != nil {
				t.Fatal(source, api, err)
			}
			e := node.(datetimeExtractExpression)
			result := sqltype.Numeric
			if api == "date_part" {
				result = sqltype.Float8
			}
			volatility := Immutable
			if source == "timestamptz" {
				volatility = Stable
			}
			if e.ResultUsage().Type.ID() != result || e.ResultUsage().Typmod != -1 || e.Properties().Volatility != volatility || plannedVolatility(e) != volatility || generationImmutable(e) != (volatility == Immutable) || !b.sameGroupExpression(e, e) {
				t.Fatal(source, api, e.Properties())
			}
			replacements := map[sqltype.ID]*sqltype.Descriptor{}
			for _, d := range sqltype.Builtins() {
				replacements[d.ID()] = d
			}
			next, changed, err := retagBoundTypes(New().CatalogRead(), e, func(d *sqltype.Descriptor) *sqltype.Descriptor { return replacements[d.ID()] })
			if err != nil || !changed {
				t.Fatal(next, changed, err)
			}
			rebound := next.(datetimeExtractExpression)
			if rebound.ResultUsage().Type != replacements[result] || rebound.ResultUsage().Type == e.ResultUsage().Type {
				t.Fatal("result authority retained")
			}
			for i, arg := range rebound.args {
				if arg.ResultUsage().Type == e.args[i].ResultUsage().Type {
					t.Fatal("child authority retained")
				}
			}
			zone, err := sqltype.ResolveZone("UTC")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := rebound.Eval(&EvalContext{Context: context.Background(), Input: sqltype.InputEnvironment{Zone: zone}}); err != nil {
				t.Fatal(source, api, err)
			}
		}
	}
}

func TestDatetimeExtractResolutionFailures(t *testing.T) {
	for _, tc := range []struct {
		api           string
		source        parser.Expr
		code, message string
	}{
		{"extract", datetimeMakeString("2000-01-01"), "42725", "function extract(unknown, unknown) is not unique"},
		{"date_part", parser.ParameterExpr{Number: 1}, "42725", "function date_part(unknown, unknown) is not unique"},
		{"extract", datetimeMakeInteger(1), "42883", "function extract(unknown, integer) does not exist"},
		{"date_part", ageFiniteTyped("text", "2000-01-01"), "42883", "function date_part(unknown, text) does not exist"},
	} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		_, err := b.bindDatetimeExtract(datetimeMakeRaw(tc.api, datetimeMakeString("year"), tc.source), true)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != tc.code || d.Message != tc.message || d.Position != "8" || d.Hint == "" {
			t.Fatal(tc, err)
		}
	}
}

// Real bound providers supplement the public volatile parser-boundary witness.
// No SQL parser admission is broadened and no fake evaluator supplies properties.
func TestDatetimeExtractWrapperContextRealChildProperties(t *testing.T) {
	engine := New()
	session := sessionStart(t, engine, nil)
	op := transactionOperation(t, session)
	scope, err := engine.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	defer op.Done()
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
	}()
	input := scope.input
	input.TransactionStart = time.Date(2000, 1, 1, 12, 0, 0, 123500000, time.UTC)
	for _, tc := range []struct {
		name       string
		condition  parser.Expr
		volatility Volatility
	}{
		{"stable", parser.CastExpr{Child: parser.CurrentDateTimeExpr{Kind: parser.CurrentDate}, TypeName: parser.TypeName{Name: "text"}}, Stable},
		{"volatile", datetimeMakeRaw("random"), Volatile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newTestExpressionBinder(t, context.Background(), engine, input, nil)
			field := parser.CaseExpr{Arms: []parser.CaseArm{{Condition: parser.PredicateExpr{Kind: parser.IsNotNull, Left: tc.condition}, Result: datetimeMakeString("timezone")}}, Else: datetimeMakeString("year")}
			node, err := b.bindDatetimeExtract(datetimeMakeRaw("date_part", field, ageFiniteTyped("date", "2000-01-01")), true)
			if err != nil {
				t.Fatal(err)
			}
			e := node.(datetimeExtractExpression)
			if e.dateWrapperContext || e.Properties().Volatility != tc.volatility || plannedVolatility(e) != tc.volatility || generationImmutable(e) {
				t.Fatal(e.Properties(), e.dateWrapperContext)
			}
			_, err = e.Eval(&EvalContext{Context: context.Background(), Input: input, statementScope: scope})
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "0A000" || d.Message != `unit "timezone" not supported for type timestamp without time zone` || d.Context != "" {
				t.Fatal(err)
			}
		})
	}
}
