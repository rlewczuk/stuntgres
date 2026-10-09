package parser

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFormatRawCallsAndResolutionOnlyBoundary(t *testing.T) {
	for _, query := range []string{
		"SELECT format('%s',1)", "SELECT FORMAT('%s',1)", `SELECT "FORMAT"('%s',1)`,
		"SELECT pg_catalog.format('%s',format('%s',1))",
		"SELECT format('%s',VARIADIC '{a,b}'::NAME[])",
		"SELECT format('%s',VARIADIC names) FROM t",
		"SELECT CASE WHEN TRUE THEN format('%s',1) ELSE 'no' END",
		"SELECT format('%s',i) FROM t WHERE format('%s',i)='1' ORDER BY format('%s',i)",
		"CREATE TABLE format(format TEXT DEFAULT format('%s',1))",
		"SELECT DATE(3) 'epoch' FROM date",
		"SELECT CAST(i AS integer),int4(i),count(*) FROM t",
	} {
		if _, err := Parse(query); err != nil {
			t.Fatal(query, err)
		}
	}
	statements, err := Parse("SELECT format('%s',missing_format_child(1))")
	if err != nil {
		t.Fatal(err)
	}
	statements, err = Parse("SELECT format FROM format")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := statements[0].(*Select).Targets[0].(ColumnExpr); !ok {
		t.Fatal("bare format must remain an identifier")
	}
}

func TestFormatIdentifierCanonicalContext(t *testing.T) {
	for _, tc := range []struct {
		name  string
		quote bool
	}{
		{"foo", false}, {"_foo", false}, {"a$b", true}, {"abort", false}, {"select", true}, {"join", true}, {"X", true}, {"é", true}, {"", true}, {"a b", true}, {"1a", true},
	} {
		got, err := IdentifierNeedsQuotesContext(context.Background(), tc.name)
		if err != nil || got != tc.quote || got != IdentifierNeedsQuotes(tc.name) {
			t.Fatal(tc, got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := IdentifierNeedsQuotesContext(ctx, strings.Repeat("a", 4096)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestFormatParameterProjectionAdmissionBoundary(t *testing.T) {
	for _, query := range []string{"SELECT format('%s',$1),$1", "SELECT $1,format('%s',$1)"} {
		statements, err := Parse(query)
		if err != nil || !statements[0].(*Select).IsSingleton() {
			t.Fatal(query, err)
		}
	}
	for _, query := range []string{
		"SELECT $1", "SELECT 1,format('%s',1)",
		"SELECT CAST($1 AS text),format('%s',$1)",
		"SELECT format('%s',$1),lower('x'),$1",
		"SELECT format('%s',$1),$1 WHERE false",
	} {
		if _, err := Parse(query); err == nil {
			t.Fatalf("unsupported singleton projection widened: %s", query)
		}
	}
}

func TestFormatMissingChildAdmissionScope(t *testing.T) {
	for _, query := range []string{
		"SELECT format(NULL,missing_format_child())",
		"SELECT pg_catalog.format('%s',lower(missing_format_child(1)))",
		"SELECT format('%s',format('%s',missing_format_child(1)))",
	} {
		if _, err := Parse(query); err != nil {
			t.Fatal(query, err)
		}
	}
	for _, query := range []string{
		"SELECT missing_format_child(1) FROM t",
		"SELECT format('%s',1),missing_format_child(1) FROM t",
		"SELECT format('%s',1); SELECT missing_format_child(1) FROM t",
		"CREATE TABLE format(x TEXT DEFAULT missing_format_child(1))",
		"CREATE TABLE public.format(x TEXT DEFAULT missing_format_child(1))",
	} {
		if _, err := Parse(query); err == nil {
			t.Fatalf("format argument context leaked: %s", query)
		}
	}
}

func TestFormatRegclassArrayCastClassification(t *testing.T) {
	for _, tc := range []struct {
		expr   string
		array  bool
		fetch  bool
		syntax CastSyntax
	}{
		{"'{1,NULL}'::regclass[]", true, false, PostfixCast},
		{"CAST('{1,NULL}' AS regclass[])", true, false, StandardCast},
		{"'1'::regclass", false, false, PostfixCast},
		{"CAST('1' AS regclass)", false, false, StandardCast},
		{"CAST('{1,NULL}' AS regclass[])", true, true, StandardCast},
		{"CAST('1' AS regclass)", false, true, StandardCast},
	} {
		query := "SELECT format('%s'," + tc.expr + ")"
		if tc.fetch {
			query = "SELECT v FROM t FETCH FIRST " + tc.expr + " ROWS ONLY"
		}
		t.Run(query, func(t *testing.T) {
			statements, err := Parse(query)
			if err != nil {
				t.Fatal(err)
			}
			selectNode := statements[0].(*Select)
			var operand Expr
			if tc.fetch {
				operand = selectNode.Bounds.Count
			} else {
				operand = selectNode.Targets[0].(FunctionExpr).Args[1]
			}
			if tc.array {
				cast, ok := operand.(CastExpr)
				if !ok || cast.TypeName.Name != "regclass" || len(cast.TypeName.ArrayBounds) != 1 || cast.Syntax != tc.syntax {
					t.Fatalf("array annotation must retain CastExpr type, bounds and syntax, got %#v", operand)
				}
			} else if _, ok := operand.(RegclassExpr); !ok {
				t.Fatalf("scalar annotation must remain RegclassExpr, got %#v", operand)
			}
		})
	}
}
