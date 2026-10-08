package engine

import (
	"context"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
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
			raw := statement(t, "SELECT "+declaration+"(3) '"+text+"' FROM t").(*parser.Select).Targets[0].(parser.TypedLiteralExpr)
			e := New()
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			node, err := b.bind(raw, expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			coercion, ok := node.(coercionExpression)
			if !ok || coercion.mode != sqltype.Explicit || coercion.usage.Typmod != 3 {
				t.Fatal(node)
			}
			literal := coercion.child.(literalExpression)
			if literal.usage.Typmod != -1 {
				t.Fatal(literal.usage)
			}
			unrounded, err := literal.value.Type().Output(context.Background(), literal.value)
			if err != nil || !strings.Contains(unrounded, ".1234") {
				t.Fatal(unrounded, err)
			}
			value, err := node.Eval(&EvalContext{Context: context.Background()})
			if err != nil {
				t.Fatal(err)
			}
			rounded, err := value.Type().Output(context.Background(), value)
			if err != nil || strings.Contains(rounded, ".1234") || !strings.Contains(rounded, ".123") {
				t.Fatal(rounded, err)
			}
			if len(b.temporals) != 1 || b.temporals[0].typmod != -1 || !b.temporals[0].typed || b.temporals[0].typedTypmod != 3 {
				t.Fatal(b.temporals)
			}
			description := StatementDescription{temporals: b.temporals}
			raw.Text = "must not be reparsed"
			again := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, &description)
			rebound, err := again.bind(raw, expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			got, err := (pgRenderer{view: e.CatalogRead()}).sql(context.Background(), rebound, 0)
			want, err2 := (pgRenderer{view: e.CatalogRead()}).sql(context.Background(), node, 0)
			if err != nil || err2 != nil || got != want || !strings.Contains(got, ".1234") {
				t.Fatal(got, want, err, err2)
			}
		})
	}
}

func TestDeparseConstraintsReviewUnknownNullProvenance(t *testing.T) {
	e := New()
	for _, expression := range []string{"num_nulls(NULL)", "num_nulls(NULL::unknown)", "num_nonnulls(CAST(NULL AS pg_catalog.unknown))"} {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := statement(t, "SELECT "+expression+" FROM t").(*parser.Select).Targets[0]
		node, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(expression, err)
		}
		leaf := node.(nullCountExpression).args[0].(literalExpression)
		if !leaf.unknownNull || !leaf.value.IsNull() || leaf.usage.Type.ID() != sqltype.Text {
			t.Fatal(leaf)
		}
		if text, err := literalSQL(context.Background(), leaf); err != nil || text != "NULL::unknown" {
			t.Fatal(text, err)
		}
		typed := leaf
		typed.unknownNull = false
		if b.sameGroupExpression(leaf, typed) {
			t.Fatal("unknown and concrete NULL conflated")
		}
		target := New()
		retagged, changed, err := retagBoundTypes(node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
			typ, _ := target.CatalogRead().LookupType(d.ID())
			return typ
		})
		if err != nil || !changed {
			t.Fatal(changed, err)
		}
		remapped, err := remapBoundExpression(retagged, func(ref ObjectRef) ObjectRef { return ref }, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err := (pgRenderer{view: target.CatalogRead()}).sql(context.Background(), remapped, 0)
		if err != nil || !strings.Contains(got, "NULL::unknown") {
			t.Fatal(got, err)
		}
		treeType, _ := target.CatalogRead().LookupType(sqltype.PgNodeTree)
		tree, err := catalogTree(treeType, remapped)
		if err != nil {
			t.Fatal(err)
		}
		serialized, err := treeType.Output(context.Background(), tree)
		if err != nil || !strings.Contains(serialized, "null unknown") {
			t.Fatal(serialized, err)
		}
		bad := leaf
		bad.value = sqltype.String(leaf.usage.Type, "not null")
		if _, err := literalSQL(context.Background(), bad); err == nil {
			t.Fatal("invalid provenance accepted")
		}
	}
}

func TestDeparseConstraintsReviewLiteralQuotePolicy(t *testing.T) {
	e := New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Text)
	literal := literalExpression{expressionInfo: expressionInfo{usage: typ.Usage()}, value: sqltype.String(typ, "a'b\\c")}
	for _, tc := range []struct {
		off  bool
		want string
	}{{false, "'a''b\\c'::text"}, {true, "'a''b\\\\c'::text"}} {
		got, err := (pgRenderer{view: e.CatalogRead(), nonstandardStrings: tc.off}).sql(context.Background(), literal, 0)
		if err != nil || got != tc.want {
			t.Fatal(got, tc.want, err)
		}
	}
}

func TestDeparseConstraintsReviewUnknownNullConsumerBoundary(t *testing.T) {
	e := New()
	for _, expression := range []string{"NULL::unknown=1", "CAST(NULL AS pg_catalog.unknown)=1", "NULL::unknown::integer IS NULL", "NULL::unknown::pg_node_tree IS NULL"} {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := statement(t, "SELECT "+expression+" FROM t").(*parser.Select).Targets[0]
		node, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(expression, err)
		}
		value, err := node.Eval(&EvalContext{Context: context.Background()})
		if err != nil {
			t.Fatal(expression, err)
		}
		if node.ResultUsage().Type.ID() != sqltype.Bool {
			t.Fatal(node.ResultUsage())
		}
		if strings.HasSuffix(expression, "=1") {
			if !value.IsNull() {
				t.Fatal(expression, value)
			}
		} else {
			text, err := value.Type().Output(context.Background(), value)
			if err != nil || value.IsNull() || text != "t" {
				t.Fatal(expression, text, err)
			}
		}
	}
	for _, expression := range []string{"'x'::unknown", "$1::unknown", "1::unknown", "CAST(NULL AS missing_schema.unknown)", "NULL::unknown(1)"} {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := statement(t, "SELECT "+expression+" FROM t").(*parser.Select).Targets[0]
		if _, err := b.bind(raw, expressionPolicy{context: projectionContext}); err == nil {
			t.Fatal("admitted concrete UNKNOWN", expression)
		}
	}
}

func TestDeparseConstraintsReviewSignedConstantNormalization(t *testing.T) {
	e := New()
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
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := statement(t, "SELECT "+tc.expression+" FROM t").(*parser.Select).Targets[0]
		node, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(tc.expression, err)
		}
		if _, ok := node.(literalExpression); !ok || node.ResultUsage().Type.ID() != tc.id {
			t.Fatal(tc.expression, node)
		}
		value, err := node.Eval(&EvalContext{Context: context.Background()})
		if err != nil {
			t.Fatal(err)
		}
		text, err := value.Type().Output(context.Background(), value)
		if err != nil || text != tc.output {
			t.Fatal(tc.expression, text, err)
		}
	}
}

func TestDeparseConstraintsReviewConcreteNullCountArgument(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := statement(t, "SELECT num_nulls(NULL::integer) FROM t").(*parser.Select).Targets[0]
	node, err := b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	leaf := node.(nullCountExpression).args[0].(literalExpression)
	if leaf.unknownNull || leaf.usage.Type.ID() != sqltype.Int4 || !leaf.value.IsNull() {
		t.Fatal(leaf)
	}
	text, err := (pgRenderer{view: e.CatalogRead()}).sql(context.Background(), node, 0)
	if err != nil || text != "num_nulls(NULL::integer)" {
		t.Fatal(text, err)
	}
	value, err := node.Eval(&EvalContext{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	count, err := value.Integer()
	if err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
