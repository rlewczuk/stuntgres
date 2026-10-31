package types_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDeparseConstraintsReviewRawTemporalCapture(t *testing.T) {
	for _, declaration := range []string{"TIME", "TIMETZ", "TIMESTAMP", "TIMESTAMPTZ"} {
		t.Run(declaration, func(t *testing.T) {
			text := "12:34:56.1234"
			if strings.Contains(declaration, "TIMESTAMP") {
				text = "2026-10-08 " + text
			}
			if declaration == "TIMETZ" || declaration == "TIMESTAMPTZ" {
				text += "+00"
			}
			raw := enginetest.Statement(t, "SELECT "+declaration+"(3) '"+text+"' FROM t").(*parser.Select).Targets[0].(parser.TypedLiteralExpr)
			e := engine.New()
			b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			coercion, ok := node.(plan.CoercionExpr)
			if !ok || coercion.Mode != sqltype.Explicit || coercion.Usage.Typmod != 3 {
				t.Fatal(node)
			}
			literal := coercion.Child.(plan.LiteralExpr)
			if literal.Usage.Typmod != -1 {
				t.Fatal(literal.Usage)
			}
			unrounded, err := literal.Value.Type().Output(context.Background(), literal.Value)
			if err != nil || !strings.Contains(unrounded, ".1234") {
				t.Fatal(unrounded, err)
			}
			value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
			if err != nil {
				t.Fatal(err)
			}
			rounded, err := value.Type().Output(context.Background(), value)
			if err != nil || strings.Contains(rounded, ".1234") || !strings.Contains(rounded, ".123") {
				t.Fatal(rounded, err)
			}
			if len(b.Prepared.Temporals) != 1 || b.Prepared.Temporals[0].Typmod != -1 || !b.Prepared.Temporals[0].Typed || b.Prepared.Temporals[0].TypedTypmod != 3 {
				t.Fatal(b.Prepared.Temporals)
			}
			description := core.Description{Temporals: b.Prepared.Temporals}
			raw.Text = "must not be reparsed"
			again := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, &description)
			rebound, err := again.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			got, err := (stored.Deparser{View: e.CatalogRead()}).SQL(context.Background(), rebound, stored.DeparseParent{})
			want, err2 := (stored.Deparser{View: e.CatalogRead()}).SQL(context.Background(), node, stored.DeparseParent{})
			if err != nil || err2 != nil || got != want || !strings.Contains(got, ".1234") {
				t.Fatal(got, want, err, err2)
			}
		})
	}
}

func TestDeparseConstraintsReviewSignedConstantNormalization(t *testing.T) {
	e := engine.New()
	for _, tc := range []struct {
		expression string
		id         sqltype.ID
		output     string
	}{
		{"-2147483648", sqltype.Int4, "-2147483648"},
		{"-2147483649", sqltype.Int8, "-2147483649"},
		{"+2147483648", sqltype.Int8, "2147483648"},
		{"-1.20", sqltype.Numeric, "-1.20"},
	} {
		b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := enginetest.Statement(t, "SELECT "+tc.expression+" FROM t").(*parser.Select).Targets[0]
		node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(tc.expression, err)
		}
		if _, ok := node.(plan.LiteralExpr); !ok || node.ResultUsage().Type.ID() != tc.id {
			t.Fatal(tc.expression, node)
		}
		value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
		if err != nil {
			t.Fatal(err)
		}
		text, err := value.Type().Output(context.Background(), value)
		if err != nil || text != tc.output {
			t.Fatal(tc.expression, text, err)
		}
	}
}

func TestDeparseConstraintsReviewTypedIntervalZoneProvider(t *testing.T) {
	e := engine.New()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := enginetest.Statement(t, "SELECT TIMESTAMP '2026-10-08 00:00:00' AT TIME ZONE INTERVAL '02:00' HOUR TO MINUTE FROM t").(*parser.Select).Targets[0]
	node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		t.Fatal(err)
	}
	conversion := node.(functions.Timezone)
	value, err := conversion.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil {
		t.Fatal(err)
	}
	expected, err := conversion.Usage.Type.Input(context.Background(), "2026-10-07 22:00:00+00", -1)
	if err != nil {
		t.Fatal(err)
	}
	actualMicros, err := value.Timestamp()
	expectedMicros, expectedErr := expected.Timestamp()
	if err != nil || expectedErr != nil || actualMicros != expectedMicros {
		t.Fatal(value, expected, err, expectedErr)
	}
}
