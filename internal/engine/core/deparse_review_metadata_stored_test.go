package core_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDeparseConstraintsReviewUnknownNullProvenance(t *testing.T) {
	e := engine.New()
	for _, expression := range []string{"num_nulls(NULL)", "num_nulls(NULL::unknown)", "num_nonnulls(CAST(NULL AS pg_catalog.unknown))"} {
		b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := enginetest.Statement(t, "SELECT "+expression+" FROM t").(*parser.Select).Targets[0]
		node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(expression, err)
		}
		leaf := node.(functions.NullCount).Args[0].(plan.LiteralExpr)
		if !leaf.UnknownNull || !leaf.Value.IsNull() || leaf.Usage.Type.ID() != sqltype.Text {
			t.Fatal(leaf)
		}
		if text, err := regobj.ConstantLiteral(context.Background(), leaf); err != nil || text != "NULL::unknown" {
			t.Fatal(text, err)
		}
		typed := leaf
		typed.UnknownNull = false
		if b.SameGroupExpression(leaf, typed) {
			t.Fatal("unknown and concrete NULL conflated")
		}
		target := engine.New()
		retagged, changed, err := stored.RetagTypes(target.CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
			typ, _ := target.CatalogRead().LookupType(d.ID())
			return typ
		})
		if err != nil || !changed {
			t.Fatal(changed, err)
		}
		mapper := stored.ViewQueryRemapper{Catalog: target.CatalogRead(), Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, Procedure: replacementProcedurePolicyFixture(e, target)}
		query, err := mapper.Query(&plan.ValuesNode{Rows: [][]expr.Expr{{retagged}}})
		if err != nil {
			t.Fatal(err)
		}
		retagged = query.(*plan.ValuesNode).Rows[0][0]
		remapped, err := stored.RemapExpression(retagged, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err := (stored.Deparser{View: target.CatalogRead()}).SQL(context.Background(), remapped, stored.DeparseParent{})
		if err != nil || !strings.Contains(got, "NULL::unknown") {
			t.Fatal(got, err)
		}
		treeType, _ := target.CatalogRead().LookupType(sqltype.PgNodeTree)
		tree, err := plan.CatalogTree(treeType, remapped)
		if err != nil {
			t.Fatal(err)
		}
		serialized, err := treeType.Output(context.Background(), tree)
		if err != nil || !strings.Contains(serialized, "null unknown") {
			t.Fatal(serialized, err)
		}
		bad := leaf
		bad.Value = sqltype.String(leaf.Usage.Type, "not null")
		if _, err := regobj.ConstantLiteral(context.Background(), bad); err == nil {
			t.Fatal("invalid provenance accepted")
		}
	}
}

func TestDeparseConstraintsReviewLiteralQuotePolicy(t *testing.T) {
	e := engine.New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Text)
	literal := plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: sqltype.String(typ, "a'b\\c")}
	for _, tc := range []struct {
		off  bool
		want string
	}{{false, "'a''b\\c'::text"}, {true, "'a''b\\\\c'::text"}} {
		got, err := (stored.Deparser{View: e.CatalogRead(), NonstandardStrings: tc.off}).SQL(context.Background(), literal, stored.DeparseParent{})
		if err != nil || got != tc.want {
			t.Fatal(got, tc.want, err)
		}
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
						e := engine.New()
						enginetest.Run(t, e, "CREATE TABLE t(n "+declaration+",CONSTRAINT c CHECK("+left+" "+op+" "+right+"))")
						view := e.CatalogRead()
						d := view.MustRelation(enginetest.RoleTableRef(t, e, "t").Address)
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
							got, err := (stored.Deparser{View: view, Relation: d, Pretty: pretty}).SQL(context.Background(), node, stored.DeparseParent{})
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
			e := engine.New()
			enginetest.Run(t, e, "CREATE TABLE t(n "+tc.declaration+",CONSTRAINT c CHECK("+tc.input+"))")
			view := e.CatalogRead()
			d := view.MustRelation(enginetest.RoleTableRef(t, e, "t").Address)
			for _, pretty := range []bool{false, true} {
				want := tc.compact
				if pretty {
					want = tc.pretty
				}
				got, err := (stored.Deparser{View: view, Relation: d, Pretty: pretty}).SQL(context.Background(), d.Constraints.Check[0].Expression, stored.DeparseParent{})
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
			e := engine.New()
			enginetest.Run(t, e, "CREATE TABLE t(a INT,b INT,flag BOOL,CONSTRAINT c CHECK("+tc.input+"))")
			view := e.CatalogRead()
			d := view.MustRelation(enginetest.RoleTableRef(t, e, "t").Address)
			for _, pretty := range []bool{false, true} {
				want := tc.compact
				if pretty {
					want = tc.pretty
				}
				got, err := (stored.Deparser{View: view, Relation: d, Pretty: pretty}).SQL(context.Background(), d.Constraints.Check[0].Expression, stored.DeparseParent{})
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
			e := engine.New()
			enginetest.Run(t, e, "CREATE TABLE t(a INT,b INT,c INT,CONSTRAINT ck CHECK(("+tc.input+")>0))")
			view := e.CatalogRead()
			d := view.MustRelation(enginetest.RoleTableRef(t, e, "t").Address)
			node := d.Constraints.Check[0].Expression.(plan.ComparisonExpr).Left
			for _, pretty := range []bool{false, true} {
				want := tc.compact
				if pretty {
					want = tc.pretty
				}
				got, err := (stored.Deparser{View: view, Relation: d, Pretty: pretty}).SQL(context.Background(), node, stored.DeparseParent{})
				if err != nil || got != want {
					t.Fatalf("got %q, %v; want %q", got, err, want)
				}
			}
		})
	}
}
