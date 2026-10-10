package parser

import (
	"errors"
	"testing"
)

func TestQueryDistinctOnPositionedSources(t *testing.T) {
	for _, q := range []string{
		"SELECT DISTINCT ON ((SELECT k FROM keys FOR UPDATE),(SELECT k FROM keys)) id FROM events ORDER BY 1",
		"INSERT INTO dest SELECT DISTINCT ON ((SELECT k FROM keys)) id FROM events",
		"CREATE VIEW v AS SELECT DISTINCT ON ((SELECT k FROM keys)) id FROM events",
		"CREATE TABLE dest AS SELECT DISTINCT ON ((SELECT k FROM keys)) id FROM events",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		queries, tables := 0, 0
		err = VisitQuerySources(statements[0], func(s *Select) error { queries++; return nil }, func(table *TableReference, owner *Select) error {
			tables++
			if table.Name.Parts[0].Text == "keys" && owner == nil {
				t.Fatal("missing ON subquery owner")
			}
			return nil
		})
		expected := 2
		if q[0] == 'S' {
			expected = 3
		}
		if err != nil || queries != expected || tables != expected {
			t.Fatal(q, queries, tables, err)
		}
		sentinel := errors.New("stop ON-only source")
		visits := 0
		err = VisitQuerySources(statements[0], nil, func(table *TableReference, _ *Select) error {
			visits++
			if visits == 2 {
				return sentinel
			}
			return nil
		})
		if err != sentinel || visits != 2 {
			t.Fatal("source callback failed to stop", visits, err)
		}
	}
}

func TestQueryDistinctOnWholeStatementGrammar(t *testing.T) {
	statements, err := Parse("/* λ */ SELECT DISTINCT ON (a+1,b) id FROM t ORDER BY b,a+1,id")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	s := statements[0].(*Select)
	if len(s.Distinct.On) != 2 || ExpressionStart(s.Distinct.On[0]).Character != 29 || ExpressionStart(s.Distinct.On[1]).Character != 33 {
		t.Fatal(s.Distinct)
	}
	for _, q := range []string{"SELECT DISTINCT ON () id FROM t", "SELECT DISTINCT ON (a,) id FROM t", "SELECT DISTINCT ON (a) id FROM t trailing junk"} {
		if statements, err := Parse(q); err == nil || len(statements) != 0 {
			t.Fatal("invalid ON syntax admitted", q, statements, err)
		}
	}
}
