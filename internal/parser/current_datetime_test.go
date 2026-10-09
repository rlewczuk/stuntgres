package parser

import (
	"errors"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestCurrentDateTimeGrammar(t *testing.T) {
	for _, kind := range []CurrentDateTimeKind{CurrentDate, CurrentTime, CurrentTimestamp, LocalTime, LocalTimestamp} {
		for _, precision := range []int{-1, 0, 3, 6, 7, 2147483647} {
			if kind == CurrentDate && precision != -1 {
				continue
			}
			source := kind.Name()
			if precision >= 0 {
				source += "(" + strconv.Itoa(precision) + ")"
			}
			statements, err := Parse("/*é🙂*/ SELECT " + source)
			if err != nil || len(statements) != 1 {
				t.Fatal(source, statements, err)
			}
			selectAST, ok := statements[0].(*Select)
			if !ok {
				t.Fatal(statements)
			}
			leaf, ok := selectAST.Targets[0].(CurrentDateTimeExpr)
			if !ok || leaf.Kind != kind || leaf.HasPrecision != (precision >= 0) || precision >= 0 && leaf.Precision != int32(precision) || leaf.Pos.Character != 15 || ExpressionStart(leaf) != leaf.Pos {
				t.Fatal(source, leaf)
			}
		}
	}
	for _, q := range []string{
		"SELECT CURRENT_DATE,CURRENT_TIME,CURRENT_TIMESTAMP,LOCALTIME,LOCALTIMESTAMP",
		"SELECT CURRENT_DATE=CURRENT_DATE,LOCALTIME(3)::TEXT,CURRENT_TIMESTAMP AT TIME ZONE 'UTC'",
		"SELECT COALESCE(CURRENT_TIME,NULL)", "SELECT num_nulls(CURRENT_DATE)",
		"CREATE TABLE t(d DATE DEFAULT CURRENT_DATE,z TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP(3))",
		"CREATE VIEW v AS SELECT LOCALTIMESTAMP FROM t",
		`SELECT "current_timestamp"() FROM t`, `SELECT pg_catalog."current_timestamp"() FROM t`,
	} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, q := range []string{"SELECT CURRENT_DATE()", "SELECT CURRENT_TIMESTAMP(-1)", "SELECT LOCALTIME($1)", "SELECT CURRENT_TIME(1.5)", "SELECT LOCALTIMESTAMP(2147483648)"} {
		_, err := Parse(q)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal(q, err)
		}
	}
}

func TestCurrentDateTimeOrdinaryNames(t *testing.T) {
	for _, kind := range []CurrentDateTimeKind{CurrentDate, CurrentTime, CurrentTimestamp, LocalTime, LocalTimestamp} {
		for _, name := range []string{kind.Name(), strings.ToUpper(kind.Name())} {
			quoted := `"` + name + `"`
			q := "CREATE TABLE " + quoted + " (" + quoted + " INTEGER)"
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(q, statements, err)
			}
			table := statements[0].(*CreateTable)
			if table.Name.Parts[0].Text != name || table.Elements[0].Column.Name != name {
				t.Fatal(q, table)
			}
			q = "SELECT " + quoted + " FROM " + quoted
			statements, err = Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(q, statements, err)
			}
			column, ok := statements[0].(*Select).Targets[0].(ColumnExpr)
			if !ok || len(column.Name.Parts) != 1 || column.Name.Parts[0].Text != name {
				t.Fatal(q, statements)
			}
			for _, prefix := range []string{"", "pg_catalog."} {
				q = "SELECT " + prefix + quoted + "() FROM t"
				statements, err = Parse(q)
				if err != nil || len(statements) != 1 {
					t.Fatal(q, statements, err)
				}
				call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
				wantParts := 1
				if prefix != "" {
					wantParts = 2
				}
				if !ok || len(call.Name.Parts) != wantParts || call.Name.Parts[wantParts-1].Text != name || len(call.Args) != 0 {
					t.Fatal(q, statements)
				}
			}
		}
		for _, q := range []string{
			"CREATE TABLE public." + kind.Name() + " (x INTEGER)",
			"INSERT INTO public." + kind.Name() + " (x) VALUES(1)",
		} {
			if _, err := Parse(q); err != nil {
				t.Fatal(q, err)
			}
		}
		q := "SELECT pg_catalog." + kind.Name() + "() FROM t"
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
		if !ok || len(call.Name.Parts) != 2 || call.Name.Parts[0].Text != "pg_catalog" || call.Name.Parts[1].Text != kind.Name() || len(call.Args) != 0 {
			t.Fatal(q, statements)
		}
	}
}

func TestCurrentDateTimeStringExpressions(t *testing.T) {
	for _, kind := range []CurrentDateTimeKind{CurrentDate, CurrentTime, CurrentTimestamp, LocalTime, LocalTimestamp} {
		valid, found := stringSingleton(CurrentDateTimeExpr{Kind: kind})
		if !valid || found {
			t.Fatal("current leaf must be valid without claiming a string consumer", kind, valid, found)
		}
		for _, query := range []string{
			"SELECT normalize(" + kind.Name() + "::TEXT)",
			"SELECT " + kind.Name() + "::TEXT IS NORMALIZED",
		} {
			statements, err := Parse(query)
			if err != nil || len(statements) != 1 {
				t.Fatal(query, statements, err)
			}
			selected, ok := statements[0].(*Select)
			if !ok || !ContainsStringExpression(selected.Targets[0]) {
				t.Fatal("existing string consumer rejected current leaf", query, statements)
			}
		}
	}
	query := "SELECT CURRENT_DATE::TEXT,normalize(CURRENT_DATE::TEXT),CURRENT_DATE::TEXT IS NORMALIZED"
	statements, err := Parse(query)
	if err != nil || len(statements) != 1 {
		t.Fatal(query, statements, err)
	}
	selected, ok := statements[0].(*Select)
	if !ok || len(selected.Targets) != 3 || ContainsStringExpression(selected.Targets[0]) || !ContainsStringExpression(selected.Targets[1]) || !ContainsStringExpression(selected.Targets[2]) {
		t.Fatal("mixed current/string expression classification", query, statements)
	}
}
