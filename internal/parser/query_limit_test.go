package parser

import (
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestQueryLimitGrammar(t *testing.T) {
	for _, query := range []string{
		"SELECT id FROM t LIMIT $1 OFFSET $2",
		"SELECT id FROM t OFFSET NULL LIMIT ALL",
		"SELECT id FROM t OFFSET 1 ROW FETCH NEXT (1+1) ROWS ONLY",
		"SELECT id FROM t FETCH FIRST ROW ONLY OFFSET 1 ROWS",
		"SELECT id FROM t LIMIT nextval('s')",
		"SELECT id FROM t LIMIT random()",
		"SELECT id FROM t ORDER BY id FOR UPDATE LIMIT 1",
		"SELECT id FROM t ORDER BY id LIMIT 1 FOR UPDATE",
		"SELECT nextval('s') LIMIT 0",
		"SELECT 1 LIMIT 1",
		"VALUES(1) LIMIT 1",
		"INSERT INTO t VALUES(1),(2) LIMIT 1",
		"INSERT INTO t (VALUES(1),(2) OFFSET 1 FETCH NEXT ROW ONLY)",
		"INSERT INTO t (SELECT LIMIT 0)",
		"INSERT INTO t SELECT 1 LIMIT 0",
	} {
		t.Run(query, func(t *testing.T) {
			statements, err := Parse(query)
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
			switch s := statements[0].(type) {
			case *Select:
				if s.Bounds == nil && (s.Values == nil || s.Values.Bounds == nil) {
					t.Fatal("lost count decoration")
				}
			case *Insert:
				if s.Source == nil {
					t.Fatal("decorated producer remained contextual VALUES")
				}
			default:
				t.Fatalf("unexpected statement %T", s)
			}
		})
	}
	for _, query := range []string{
		"SELECT id FROM t LIMIT 1 LIMIT 2", "SELECT id FROM t OFFSET 1 OFFSET 2",
		"SELECT id FROM t LIMIT 1 FETCH FIRST ROW ONLY",
		"SELECT id FROM t FETCH FIRST 1+1 ROW ONLY",
		"SELECT id FROM t FETCH FIRST 1 ROW WITH TIES",
		"SELECT id FROM t LIMIT 1 junk", "SELECT id FROM t LIMIT",
	} {
		t.Run("reject/"+query, func(t *testing.T) {
			statements, err := Parse("INSERT INTO t VALUES(9);" + query)
			if err == nil || statements != nil {
				t.Fatal("partial script accepted", statements, err)
			}
		})
	}
	_, err := Parse("SELECT id FROM t LIMIT 1,2")
	d, ok := err.(*sqlstate.Error)
	if !ok || d.Code != "42601" || d.Message != "LIMIT #,# syntax is not supported" || d.Hint != "Use separate LIMIT and OFFSET clauses." || d.Position != "18" {
		t.Fatal(err)
	}
}

func TestQueryLimitLockingTail(t *testing.T) {
	for _, clause := range []string{"FOR KEY SHARE", "FOR SHARE", "FOR NO KEY UPDATE", "FOR UPDATE"} {
		for _, prefix := range []string{"SELECT id FROM t ORDER BY id ", "INSERT INTO out SELECT id FROM t ", "INSERT INTO out SELECT FROM t "} {
			for _, tail := range []struct{ before, after string }{
				{"", ""},
				{"LIMIT 1 ", ""},
				{"OFFSET 1 FETCH NEXT ROW ONLY ", ""},
				{"", " LIMIT 1"},
				{"", " LIMIT 1 OFFSET 1"},
				{"", " OFFSET 1 FETCH NEXT ROW ONLY"},
			} {
				query := prefix + tail.before + clause + tail.after + ";"
				statements, err := Parse(query)
				if err != nil || len(statements) != 1 {
					t.Fatal(query, statements, err)
				}
				var selected *Select
				switch s := statements[0].(type) {
				case *Select:
					selected = s
				case *Insert:
					selected = s.Source.Select
				default:
					t.Fatalf("unexpected statement %T", s)
				}
				if len(selected.Locking) != 1 || selected.Locking[0].Mode != clause || selected.Locking[0].Pos.Offset != strings.Index(query, clause) {
					t.Fatal(query, selected.Locking)
				}
				if (selected.Bounds != nil) != (tail.before != "" || tail.after != "") {
					t.Fatal(query, selected.Bounds)
				}
			}
		}
	}
}

func TestQueryLimitZeroWidthContinuation(t *testing.T) {
	for _, query := range []string{
		"INSERT INTO out SELECT",
		"INSERT INTO out (SELECT)",
		"INSERT INTO out SELECT LIMIT 0",
		"INSERT INTO out (SELECT OFFSET 1 FETCH NEXT ROW ONLY)",
		"INSERT INTO out SELECT t.like,t.ilike FROM t LIMIT 1",
		"INSERT INTO out SELECT 1 AS like,2 AS ilike LIMIT 1",
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
	}
	// Bare type/function keywords still require a call opener. Complete
	// ordinary calls reach binding, which owns missing-signature diagnostics.
	for _, tc := range []struct{ word, end string }{{"LIKE", "28"}, {"ILIKE", "29"}} {
		statements, err := Parse("INSERT INTO out SELECT " + tc.word)
		if statements != nil || err == nil {
			t.Fatal(tc.word, statements, err)
		}
		d, ok := err.(*sqlstate.Error)
		if !ok || d.Code != "42601" || d.Message != "syntax error at end of input" || d.Position != tc.end {
			t.Fatal(tc.word, err)
		}
		statements, err = Parse("INSERT INTO out SELECT " + tc.word + "('x')")
		if err != nil || len(statements) != 1 || statements[0].(*Insert).Source == nil {
			t.Fatal(tc.word, statements, err)
		}
	}
}
