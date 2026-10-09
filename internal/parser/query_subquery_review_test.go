package parser

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestQuerySubqueryQualifiedOperatorAST(t *testing.T) {
	for _, symbol := range []string{"=", "<>", "<", ">", "<=", ">=", "!="} {
		for _, quantifier := range []string{"ANY", "SOME", "ALL"} {
			for _, schema := range []string{"pg_catalog", "public"} {
				sql := "SELECT /*é*/ 1 OPERATOR(" + schema + "." + symbol + ") " + quantifier + "(SELECT DISTINCT 1) AS matched"
				t.Run(sql, func(t *testing.T) {
					statements, err := Parse(sql)
					if err != nil || len(statements) != 1 {
						t.Fatal(statements, err)
					}
					selected := statements[0].(*Select)
					expr, ok := selected.Targets[0].(SubqueryExpr)
					kind := SubqueryAny
					if quantifier == "ALL" {
						kind = SubqueryAll
					}
					operatorOffset := strings.Index(sql, "OPERATOR")
					symbolOffset := strings.Index(sql, "."+symbol) + 1
					operatorPosition := Position{Offset: operatorOffset, Character: utf8.RuneCountInString(sql[:operatorOffset]) + 1}
					symbolPosition := Position{Offset: symbolOffset, Character: utf8.RuneCountInString(sql[:symbolOffset]) + 1}
					if !ok || expr.Kind != kind || expr.Negate || expr.Pos != operatorPosition || len(expr.Operator.Parts) != 2 || expr.Query == nil || expr.Query.Distinct == nil || len(expr.Query.Distinct.On) != 0 {
						t.Fatal(expr)
					}
					operatorText := symbol
					if operatorText == "!=" {
						operatorText = "<>"
					}
					if expr.Operator.Parts[0] != (Identifier{Text: schema, Pos: operatorPosition}) || expr.Operator.Parts[1] != (Identifier{Text: operatorText, Pos: symbolPosition}) {
						t.Fatal(expr.Operator)
					}
				})
			}
		}
	}
	for _, sql := range []string{
		"SELECT ROW(1,2) OPERATOR(pg_catalog.=) ANY(SELECT 1,2)",
		"SELECT (1,2) OPERATOR(pg_catalog.<>) ALL(SELECT 3,4)",
		"SELECT (1+2) OPERATOR(pg_catalog.>=) SOME(SELECT 3)",
		"SELECT 1 OPERATOR(pg_catalog.+) 2 OPERATOR(pg_catalog.=) ANY(SELECT 3)",
		"SELECT 1 OPERATOR(pg_catalog.=) ANY(SELECT 1) AS any,1 OPERATOR(pg_catalog.=) SOME(SELECT 1) AS some",
	} {
		statements, err := Parse(sql)
		if err != nil || len(statements) != 1 {
			t.Fatal(sql, statements, err)
		}
		if _, ok := statements[0].(*Select).Targets[0].(SubqueryExpr); !ok {
			t.Fatal(sql, statements[0])
		}
	}
}

func TestQuerySubqueryBareOperatorMigration(t *testing.T) {
	for _, tc := range []struct {
		sql    string
		symbol string
		negate bool
	}{
		{"SELECT 1 IN(SELECT 1)", "=", false},
		{"SELECT 1 NOT IN(SELECT 1)", "=", true},
		{"SELECT 1 = ANY(SELECT 1)", "=", false},
		{"SELECT 1 <> SOME(SELECT 2)", "<>", false},
		{"SELECT 1 <= ALL(SELECT 2)", "<=", false},
	} {
		statements, err := Parse(tc.sql)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.sql, statements, err)
		}
		expr := statements[0].(*Select).Targets[0].(SubqueryExpr)
		if len(expr.Operator.Parts) != 1 || expr.Operator.Parts[0].Text != tc.symbol || expr.Operator.Parts[0].Pos != expr.Pos || expr.Pos.Character != 10 || expr.Negate != tc.negate {
			t.Fatal(tc.sql, expr)
		}
	}
	for _, sql := range []string{
		"SELECT 1 in",
		"SELECT 1 any,2 some",
		"CREATE TABLE t(row INT)",
		"CREATE TABLE t(v ROW)",
		"SELECT 1 NOT IN(SELECT 2),1 NOT BETWEEN 2 AND 3,'a' NOT LIKE 'b'",
	} {
		statements, err := Parse(sql)
		if err != nil || len(statements) != 1 {
			t.Fatal(sql, statements, err)
		}
	}
}

func TestQuerySubqueryQualifiedOperatorWholeInput(t *testing.T) {
	for _, sql := range []string{
		"SELECT 1 OPERATOR(pg_catalog.=) ANY()",
		"SELECT 1 OPERATOR(pg_catalog.=) ANY(SELECT 1",
		"SELECT 1 OPERATOR(pg_catalog.=) ANY(SELECT 1) unexpected junk",
		"SELECT 1 OPERATOR(pg_catalog.) ANY(SELECT 1)",
		"SELECT 1 OPERATOR(pg_catalog.=) ANY(SELECT 1); SELECT (",
	} {
		statements, err := Parse("INSERT INTO t VALUES(1);" + sql)
		var diagnostic *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal(sql, statements, err)
		}
	}
}
