package parser

import "testing"

func TestQuerySetGrammar(t *testing.T) {
	parse := func(q string) *Select {
		t.Helper()
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		return statements[0].(*Select)
	}
	q := parse("SELECT 1 UNION SELECT 2 INTERSECT SELECT 2 EXCEPT SELECT 1")
	if q.Set == nil || q.Set.Operator != "except" || q.Set.Left.Set == nil || q.Set.Left.Set.Operator != "union" || q.Set.Left.Set.Right.Set == nil || q.Set.Left.Set.Right.Set.Operator != "intersect" {
		t.Fatal("precedence/left association", q)
	}
	q = parse("(SELECT 1 UNION ALL SELECT 2) INTERSECT DISTINCT SELECT 2")
	if q.Set.Operator != "intersect" || q.Set.All || q.Set.Left.Set.Operator != "union" || !q.Set.Left.Set.All || !q.Set.Left.Parenthesized {
		t.Fatal("parentheses/modifiers", q)
	}
	q = parse("(VALUES(1),(1) ORDER BY 1 LIMIT 1) UNION ALL SELECT 2 ORDER BY 1 LIMIT 2 OFFSET 1")
	if q.Set == nil || q.Set.Left.Values == nil || q.Set.Left.Values.Order == nil || q.Set.Left.Values.Bounds == nil || q.Order == nil || q.Bounds == nil || q.Bounds.Offset == nil {
		t.Fatal("arm/result tails", q)
	}
	for _, sql := range []string{"VALUES(1) UNION SELECT 2", "SELECT 1 EXCEPT VALUES(2)", "VALUES(1) INTERSECT ALL VALUES(1)", "((VALUES(1),(2)))"} {
		parse(sql)
	}
	q = parse("VALUES(1) INTERSECT SELECT")
	if q.Set == nil || len(q.Set.Right.Targets) != 0 {
		t.Fatal("zero-target SELECT operand lost", q)
	}
	q = parse("SELECT EXCEPT SELECT")
	if q.Set == nil || len(q.Set.Left.Targets) != 0 || len(q.Set.Right.Targets) != 0 {
		t.Fatal("zero-width set syntax lost", q)
	}
	for _, sql := range []string{"INSERT INTO dst VALUES(1)", "INSERT INTO dst (VALUES(1))", "INSERT INTO dst VALUES(1) UNION ALL SELECT 2", "INSERT INTO dst VALUES(abs(1)) UNION ALL SELECT 2"} {
		statements, err := Parse(sql)
		if err != nil || len(statements) != 1 {
			t.Fatal(sql, statements, err)
		}
		s := statements[0].(*Insert)
		if sql == "INSERT INTO dst VALUES(1) UNION ALL SELECT 2" || sql == "INSERT INTO dst VALUES(abs(1)) UNION ALL SELECT 2" {
			if s.Source == nil || s.Source.Select == nil || s.Source.Select.Set == nil {
				t.Fatal("set INSERT source lost", s)
			}
		} else if len(s.Rows) != 1 || len(s.Rows[0]) != 1 || s.Source != nil {
			t.Fatal("plain INSERT VALUES category lost", s)
		}
	}
	for _, sql := range []string{"SELECT 1 UNION", "SELECT 1 UNION ALL ALL SELECT 2", "VALUES(1) INTERSECT", "CREATE TABLE untouched(v INT); SELECT 1 EXCEPT"} {
		statements, err := Parse(sql)
		if err == nil || len(statements) != 0 {
			t.Fatal("malformed whole input admitted", sql, statements, err)
		}
	}
}
