package engine

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
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
			value, err := node.Eval(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}})
			if err != nil {
				t.Fatal(err)
			}
			rounded, err := value.Type().Output(context.Background(), value)
			if err != nil || strings.Contains(rounded, ".1234") || !strings.Contains(rounded, ".123") {
				t.Fatal(rounded, err)
			}
			if len(b.prepared.temporals) != 1 || b.prepared.temporals[0].typmod != -1 || !b.prepared.temporals[0].typed || b.prepared.temporals[0].typedTypmod != 3 {
				t.Fatal(b.prepared.temporals)
			}
			description := StatementDescription{temporals: b.prepared.temporals}
			raw.Text = "must not be reparsed"
			again := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, &description)
			rebound, err := again.bind(raw, expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			got, err := (pgRenderer{view: e.CatalogRead()}).sql(context.Background(), rebound, pgRenderParent{})
			want, err2 := (pgRenderer{view: e.CatalogRead()}).sql(context.Background(), node, pgRenderParent{})
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
		leaf := node.(functions.NullCount).Args[0].(plan.LiteralExpr)
		if !leaf.UnknownNull || !leaf.Value.IsNull() || leaf.Usage.Type.ID() != sqltype.Text {
			t.Fatal(leaf)
		}
		if text, err := literalSQL(context.Background(), leaf); err != nil || text != "NULL::unknown" {
			t.Fatal(text, err)
		}
		typed := leaf
		typed.UnknownNull = false
		if b.sameGroupExpression(leaf, typed) {
			t.Fatal("unknown and concrete NULL conflated")
		}
		target := New()
		retagged, changed, err := retagBoundTypes(target.CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
			typ, _ := target.CatalogRead().LookupType(d.ID())
			return typ
		})
		if err != nil || !changed {
			t.Fatal(changed, err)
		}
		remapped, err := remapBoundExpression(retagged, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err := (pgRenderer{view: target.CatalogRead()}).sql(context.Background(), remapped, pgRenderParent{})
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
		bad.Value = sqltype.String(leaf.Usage.Type, "not null")
		if _, err := literalSQL(context.Background(), bad); err == nil {
			t.Fatal("invalid provenance accepted")
		}
	}
}

func TestDeparseConstraintsReviewLiteralQuotePolicy(t *testing.T) {
	e := New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Text)
	literal := plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: sqltype.String(typ, "a'b\\c")}
	for _, tc := range []struct {
		off  bool
		want string
	}{{false, "'a''b\\c'::text"}, {true, "'a''b\\\\c'::text"}} {
		got, err := (pgRenderer{view: e.CatalogRead(), nonstandardStrings: tc.off}).sql(context.Background(), literal, pgRenderParent{})
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
		value, err := node.Eval(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}})
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
	for _, tc := range []struct{ expression, code, message string }{
		{"'x'::unknown", "0A000", "unsupported column type"},
		{"$1::unknown", "0A000", "unsupported column type"},
		{"1::unknown", "0A000", "unsupported column type"},
		{"CAST(NULL AS missing_schema.unknown)", "3F000", `schema "missing_schema" does not exist`},
	} {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := statement(t, "SELECT "+tc.expression+" FROM t").(*parser.Select).Targets[0]
		_, err := b.bind(raw, expressionPolicy{context: projectionContext})
		diagnostic, ok := err.(*sqlstate.Error)
		if !ok || diagnostic.Code != tc.code || diagnostic.Message != tc.message {
			t.Fatal(tc.expression, err)
		}
	}
	_, err := parser.Parse("SELECT NULL::unknown(1) FROM t")
	diagnostic, ok := err.(*sqlstate.Error)
	if !ok || diagnostic.Code != "0A000" || diagnostic.Message != "type modifiers are unsupported" || diagnostic.Position != "21" {
		t.Fatal(err)
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
		if _, ok := node.(plan.LiteralExpr); !ok || node.ResultUsage().Type.ID() != tc.id {
			t.Fatal(tc.expression, node)
		}
		value, err := node.Eval(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}})
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
	leaf := node.(functions.NullCount).Args[0].(plan.LiteralExpr)
	if leaf.UnknownNull || leaf.Usage.Type.ID() != sqltype.Int4 || !leaf.Value.IsNull() {
		t.Fatal(leaf)
	}
	text, err := (pgRenderer{view: e.CatalogRead()}).sql(context.Background(), node, pgRenderParent{})
	if err != nil || text != "num_nulls(NULL::integer)" {
		t.Fatal(text, err)
	}
	value, err := node.Eval(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}})
	if err != nil {
		t.Fatal(err)
	}
	count, err := value.Integer()
	if err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestDeparseConstraintsReviewTypedIntervalZoneProvider(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := statement(t, "SELECT TIMESTAMP '2026-10-08 00:00:00' AT TIME ZONE INTERVAL '02:00' HOUR TO MINUTE FROM t").(*parser.Select).Targets[0]
	node, err := b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	conversion := node.(functions.Timezone)
	value, err := conversion.Eval(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}})
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

func TestDeparseConstraintsReviewViewLiteralCaptureCutover(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(id INT)")
	run(t, e, "INSERT INTO t VALUES(1)")
	run(t, e, "CREATE VIEW v AS SELECT TIME(3) '04:05:06.1235' AS clock,+2147483648 AS number FROM t")
	view := viewOf(e.CatalogRead().MustRelation(roleTableRef(t, e, "v").Address))
	literal, ok := view.literals[viewLiteralPath{query: view.query.Root.(*plan.ProjectNode).Frame, clause: viewTargetLiteral, target: 0, node: 0}]
	if !ok || literal.Usage.Type.ID() != sqltype.Time || literal.Usage.Typmod != 3 {
		t.Fatal(view.literals)
	}
	raw, err := literal.Value.Time()
	if err != nil || raw != 14706123500 {
		t.Fatal(raw, err)
	}
	literal, ok = view.literals[viewLiteralPath{query: view.query.Root.(*plan.ProjectNode).Frame, clause: viewTargetLiteral, target: 1, node: 1}]
	if !ok || literal.Usage.Type.ID() != sqltype.Int8 {
		t.Fatal(view.literals)
	}
	number, err := literal.Value.Integer()
	if err != nil || number != 2147483648 {
		t.Fatal(number, err)
	}
	rows := tuples(run(t, e, "SELECT clock=TIME '04:05:06.124' AS rounded,number FROM v"))
	if len(rows) != 1 || len(rows[0]) != 2 || rows[0][0] != true || rows[0][1] != int64(2147483648) {
		t.Fatal(rows)
	}
}

func TestDeparseConstraintsReviewNumericComparisonUsage(t *testing.T) {
	for _, declaration := range []string{"NUMERIC(4,2)", "NUMERIC(2,-1)"} {
		for _, constant := range []struct{ input, compact, pretty string }{
			{"CAST('1000' AS SMALLINT)", "('1000'::smallint)::numeric", "'1000'::smallint::numeric"},
			{"1000", "(1000)::numeric", "1000::numeric"},
			{"2147483648", "('2147483648'::bigint)::numeric", "'2147483648'::bigint::numeric"},
		} {
			for _, op := range []string{"<", "=", "IS DISTINCT FROM"} {
				for _, reverse := range []bool{false, true} {
					t.Run(declaration+"/"+constant.input+"/"+op+fmt.Sprint(reverse), func(t *testing.T) {
						left, right := "n", constant.input
						if reverse {
							left, right = right, left
						}
						e := New()
						run(t, e, "CREATE TABLE t(n "+declaration+",CONSTRAINT c CHECK("+left+" "+op+" "+right+"))")
						view := e.CatalogRead()
						d := view.MustRelation(roleTableRef(t, e, "t").Address)
						node := d.Constraints.Check[0].Expression
						for _, pretty := range []bool{false, true} {
							a, b := "n", constant.compact
							if pretty {
								b = constant.pretty
							}
							if reverse {
								a, b = b, a
							}
							want := a + " " + op + " " + b
							if !pretty {
								want = "(" + want + ")"
							}
							got, err := (pgRenderer{view: view, relation: d, pretty: pretty}).sql(context.Background(), node, pgRenderParent{})
							if err != nil || got != want {
								t.Fatalf("got %q, %v; want %q", got, err, want)
							}
						}
					})
				}
			}
		}
	}
	// Genuine stored coercions remain sized; floating synthetic annotations
	// remain base usages. Neither expectation depends on helper/source layout.
	for _, tc := range []struct{ declaration, input, compact, pretty string }{
		{"NUMERIC(4,2)", "n<CAST(1000 AS NUMERIC(4,2))", "(n < (1000)::numeric(4,2))", "n < 1000::numeric(4,2)"},
		{"NUMERIC(2,-1)", "n<CAST(10000 AS NUMERIC(2,-1))", "(n < (10000)::numeric(2,-1))", "n < 10000::numeric(2,-1)"},
		{"DOUBLE PRECISION", "n<1000", "(n < (1000)::double precision)", "n < 1000::double precision"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE t(n "+tc.declaration+",CONSTRAINT c CHECK("+tc.input+"))")
			view := e.CatalogRead()
			d := view.MustRelation(roleTableRef(t, e, "t").Address)
			for _, pretty := range []bool{false, true} {
				want := tc.compact
				if pretty {
					want = tc.pretty
				}
				got, err := (pgRenderer{view: view, relation: d, pretty: pretty}).sql(context.Background(), d.Constraints.Check[0].Expression, pgRenderParent{})
				if err != nil || got != want {
					t.Fatalf("got %q, %v; want %q", got, err, want)
				}
			}
		})
	}
}

// Release-observed presentation obligations. These also have real returned-SQL
// consumers; printer-only regressions cannot be hidden by semantic equivalence.
func TestDeparseConstraintsReviewConditionalPresentation(t *testing.T) {
	for _, tc := range []struct{ input, compact, pretty string }{
		{"(-a+(b-1)*2)/2>0", "((((- a) + ((b - 1) * 2)) / 2) > 0)", "(((- a) + (b - 1) * 2) / 2) > 0"},
		{"CASE WHEN flag THEN 1 ELSE 1/a END>0", "(\nCASE\n    WHEN flag THEN 1\n    ELSE (1 / a)\nEND > 0)", "\nCASE\n    WHEN flag THEN 1\n    ELSE 1 / a\nEND > 0"},
		{"CASE WHEN flag THEN a END>0", "(\nCASE\n    WHEN flag THEN a\n    ELSE NULL::integer\nEND > 0)", "\nCASE\n    WHEN flag THEN a\n    ELSE NULL::integer\nEND > 0"},
		{"a IS NOT DISTINCT FROM b", "(NOT (a IS DISTINCT FROM b))", "NOT a IS DISTINCT FROM b"},
		{"CASE WHEN flag THEN (a&b) ELSE 1 END>0", "(\nCASE\n    WHEN flag THEN (a & b)\n    ELSE 1\nEND > 0)", "\nCASE\n    WHEN flag THEN a & b\n    ELSE 1\nEND > 0"},
		{"abs(a&b)>0", "(abs((a & b)) > 0)", "abs(a & b) > 0"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE t(a INT,b INT,flag BOOL,CONSTRAINT c CHECK("+tc.input+"))")
			view := e.CatalogRead()
			d := view.MustRelation(roleTableRef(t, e, "t").Address)
			for _, pretty := range []bool{false, true} {
				want := tc.compact
				if pretty {
					want = tc.pretty
				}
				got, err := (pgRenderer{view: view, relation: d, pretty: pretty}).sql(context.Background(), d.Constraints.Check[0].Expression, pgRenderParent{})
				if err != nil || got != want {
					t.Fatalf("got %q, %v; want %q", got, err, want)
				}
			}
		})
	}
}

func TestDeparseConstraintsReviewPrinterContexts(t *testing.T) {
	for _, tc := range []struct{ name, input, compact, pretty string }{
		{"high_under_low", "a+b*c", "(a + (b * c))", "a + b * c"},
		{"low_under_high", "(a+b)*c", "((a + b) * c)", "(a + b) * c"},
		{"same_left", "(a-b)-c", "((a - b) - c)", "a - b - c"},
		{"same_right", "a-(b-c)", "(a - (b - c))", "a - (b - c)"},
		{"addition_right", "a+(b+c)", "(a + (b + c))", "a + (b + c)"},
		{"division_right", "a/(b/c)", "(a / (b / c))", "a / (b / c)"},
		{"division_left", "(a/b)/c", "((a / b) / c)", "a / b / c"},
		{"unary_top", "-a", "(- a)", "- a"},
		{"unary_operand", "-a+b", "((- a) + b)", "(- a) + b"},
		{"nested_unary", "-(-a)", "(- (- a))", "- (- a)"},
		{"separated_function", "abs(a+b)", "abs((a + b))", "abs(a + b)"},
		{"separated_bitwise", "abs(a&b)", "abs((a & b))", "abs(a & b)"},
		{"bitwise_operand", "a+(b&c)", "(a + (b & c))", "a + (b & c)"},
		{"bitwise_parent", "(a+b)&c", "((a + b) & c)", "(a + b) & c"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE t(a INT,b INT,c INT,CONSTRAINT ck CHECK(("+tc.input+")>0))")
			view := e.CatalogRead()
			d := view.MustRelation(roleTableRef(t, e, "t").Address)
			node := d.Constraints.Check[0].Expression.(plan.ComparisonExpr).Left
			for _, pretty := range []bool{false, true} {
				want := tc.compact
				if pretty {
					want = tc.pretty
				}
				got, err := (pgRenderer{view: view, relation: d, pretty: pretty}).sql(context.Background(), node, pgRenderParent{})
				if err != nil || got != want {
					t.Fatalf("got %q, %v; want %q", got, err, want)
				}
			}
		})
	}
}
