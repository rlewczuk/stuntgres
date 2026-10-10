package parser

import (
	"errors"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestQueryWindowGrammar(t *testing.T) {
	for _, q := range []string{
		"SELECT row_number() OVER(),count(*) OVER()",
		"SELECT row_number() OVER(PARTITION BY p ORDER BY k DESC NULLS LAST,id) FROM t",
		"SELECT pg_catalog.row_number() OVER ordered,count(*) OVER(base) FROM t WINDOW base AS(PARTITION BY p),ordered AS(base ORDER BY k,id)",
		`SELECT "row_number"() OVER "Window" AS "Rank" FROM t WINDOW "Window" AS ()`,
		"SELECT row_number() OVER()+1 FROM t WHERE id>0",
		"SELECT row_number() OVER() AS over,count(*) OVER() AS partition",
		"SELECT num_nulls(1) OVER() FROM t",
		"SELECT count(row_number() OVER()) OVER() FROM t",
		"SELECT row_number() FILTER(WHERE TRUE) OVER() FROM t",
		"SELECT id FROM t WINDOW unused AS ()",
		"(SELECT row_number() OVER() FROM t) UNION ALL (SELECT count(*) OVER() FROM u)",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
	}
	statements, err := Parse("SELECT /*é*/ pg_catalog.row_number() OVER (missing) FROM t")
	if err != nil {
		t.Fatal(err)
	}
	call := statements[0].(*Select).Targets[0].(AggregateExpr)
	if call.Name != "row_number" || call.Schema == nil || call.Schema.Text != "pg_catalog" || call.Pos.Character != 14 || call.Window == nil || call.Window.Bare || call.Window.Name.Text != "missing" || call.Window.Pos.Character != 43 {
		t.Fatal(call)
	}
}
func TestQueryWindowWholeInputAndFrameBoundary(t *testing.T) {
	for _, q := range []string{"SELECT count(*) OVER(ORDER BY) FROM t", "SELECT row_number() OVER (garbage nonsense) FROM t"} {
		statements, err := Parse("INSERT INTO t VALUES(1);" + q)
		var d *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &d) || d.Code != "42601" {
			t.Fatal(q, statements, err)
		}
	}
	q := "SELECT count(*) OVER(ORDER BY id ROWS UNBOUNDED PRECEDING) FROM t"
	statements, err := Parse("INSERT INTO t VALUES(1);" + q)
	var d *sqlstate.Error
	if len(statements) != 0 || !errors.As(err, &d) || d.Code != "0A000" || d.Message != "explicit window frames are unsupported" {
		t.Fatal(statements, err)
	}
}
func TestQueryWindowSourcesVisitKeysAndDefinitions(t *testing.T) {
	statements, err := Parse("SELECT row_number() OVER(ORDER BY (SELECT id FROM inline_key)) FROM base WINDOW unused AS(PARTITION BY (SELECT id FROM named_key))")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	err = VisitQuerySources(statements[0], nil, func(table *TableReference, _ *Select) error {
		names = append(names, table.Name.Parts[0].Text)
		return nil
	})
	if err != nil || len(names) != 3 || names[0] != "base" || names[1] != "inline_key" || names[2] != "named_key" {
		t.Fatal(names, err)
	}
}

func TestQueryWindowSpecificationOwnership(t *testing.T) {
	statements, err := Parse("SELECT row_number() OVER(PARTITION BY p),count(*) OVER(ORDER BY k) FROM t WINDOW unused AS(PARTITION BY q);SELECT 1")
	if err != nil || len(statements) != 2 {
		t.Fatal(statements, err)
	}
	query := statements[0].(*Select)
	first := query.Targets[0].(AggregateExpr).Window
	second := query.Targets[1].(AggregateExpr).Window
	if first == nil || len(first.Partition) != 1 || len(first.Order) != 0 || first.Partition[0].(ColumnExpr).Name.Parts[0].Text != "p" {
		t.Fatal(first)
	}
	if second == nil || len(second.Partition) != 0 || len(second.Order) != 1 || second.Order[0].Expr.(ColumnExpr).Name.Parts[0].Text != "k" {
		t.Fatal(second)
	}
	if len(query.Windows) != 1 || len(query.Windows[0].Spec.Partition) != 1 || query.Windows[0].Spec.Partition[0].(ColumnExpr).Name.Parts[0].Text != "q" {
		t.Fatal(query.Windows)
	}
}

func TestQueryWindowKeywordLabels(t *testing.T) {
	for _, word := range []string{"over", "window"} {
		orderLabel(t, "SELECT x AS "+word+" FROM t", word)
		orderLabel(t, `SELECT x "`+word+`" FROM t`, word)
		statements, err := Parse("SELECT x " + word + " FROM t")
		var diagnostic *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal(word, statements, err)
		}
	}
}

func TestQueryWindowReviewKeywordTypeRoles(t *testing.T) {
	for _, word := range []string{"over", "partition", "rows", "range", "groups", "preceding", "following", "unbounded", "current"} {
		for _, q := range []string{
			"CREATE TYPE " + word + " AS ENUM('x')",
			"CREATE DOMAIN " + word + " AS INT",
			"CREATE TABLE t(v " + word + ")",
			"CREATE DOMAIN wrapped AS " + word,
			"CREATE TABLE t(v " + word + ".partition)",
			"SELECT 'x'::" + word + " FROM t",
			"SELECT 'x'::" + word + ".partition FROM t",
		} {
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(word, q, statements, err)
			}
		}
	}
}

func TestQueryWindowReview2RepeatedOverWholeInput(t *testing.T) {
	for _, callee := range []string{"row_number()", "num_nulls(1)"} {
		for _, shape := range []struct{ first, second string }{{"()", "()"}, {"first", "second"}} {
			for _, token := range []string{"OVER", "over", "OvEr"} {
				for _, prefix := range []string{"", "/*é*/ "} {
					q := prefix + "INSERT INTO audit VALUES(99);SELECT " + callee + " OVER " + shape.first + " " + token + " " + shape.second
					statements, err := Parse(q)
					var got *sqlstate.Error
					want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: `syntax error at or near "` + token + `"`, Position: strconv.Itoa(len([]rune(q[:strings.LastIndex(q, token)])) + 1)}
					if len(statements) != 0 || !errors.As(err, &got) || *got != want {
						t.Fatal(q, statements, got, err, want)
					}
				}
			}
		}
	}
}
