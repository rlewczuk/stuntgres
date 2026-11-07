package parser

import (
	"fmt"
	"testing"
)

func TestQuerySetInsertScalarBoundaries(t *testing.T) {
	for _, cell := range []string{"TRUE AND FALSE"} {
		for _, shape := range []string{"VALUES(%s) ORDER BY 1", "VALUES(%s) LIMIT 1", "(VALUES(%s) LIMIT 1)", "((VALUES(%s) ORDER BY 1 LIMIT 1))"} {
			q := "CREATE TABLE prefix(v INT); /*é*/ INSERT INTO t " + fmt.Sprintf(shape, cell)
			statements, err := Parse(q)
			if err == nil || len(statements) != 0 {
				t.Fatalf("restricted INSERT cells widened: %q statements=%#v error=%v", q, statements, err)
			}
		}
	}
	for _, shape := range []string{"VALUES(%s) ORDER BY 1", "VALUES(%s) LIMIT 1", "(VALUES(%s) LIMIT 1)", "((VALUES(%s) ORDER BY 1 LIMIT 1))"} {
		q := "CREATE TABLE prefix(v INT); /*é*/ INSERT INTO t " + fmt.Sprintf(shape, "abs(1)")
		statements, err := Parse(q)
		if err != nil || len(statements) != 2 {
			t.Fatal("selected decorated INSERT scalar rejected in script", q, statements, err)
		}
	}
	for _, q := range []string{
		"INSERT INTO t VALUES(1+2)",
		"INSERT INTO t ((VALUES(1+2) ORDER BY 1 LIMIT 1))",
		"INSERT INTO t VALUES(DEFAULT) LIMIT 1",
		"INSERT INTO t ((VALUES(1),(DEFAULT) ORDER BY 1 LIMIT 1))",
		"INSERT INTO t (VALUES($1) LIMIT 1)",
		"INSERT INTO t VALUES(1+2) UNION ALL SELECT 4",
		"INSERT INTO t (VALUES(abs(1)) LIMIT 1) UNION ALL SELECT 2",
		"INSERT INTO t ((VALUES(CASE WHEN TRUE THEN 1 ELSE 2 END)) UNION ALL SELECT 3)",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatalf("valid scalar/set boundary rejected: %q statements=%#v error=%v", q, statements, err)
		}
	}
	// Selected calls now remain ordinary scalar cells through plain and
	// decorated VALUES, without losing the call tree or source decorations.
	for _, q := range []string{
		"INSERT INTO t VALUES(abs(1))",
		"INSERT INTO t VALUES(abs(1)) LIMIT 1",
		"INSERT INTO t VALUES(abs(1)) ORDER BY 1",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal("selected INSERT scalar rejected", q, statements, err)
		}
		insert := statements[0].(*Insert)
		rows := insert.Rows
		if insert.Source != nil {
			rows = insert.Source.Values.Rows
			if insert.Source.Values.Order == nil && insert.Source.Values.Bounds == nil {
				t.Fatal("decorated INSERT scalar lost tail", q, insert)
			}
		}
		call, ok := rows[0][0].(FunctionExpr)
		if !ok || call.Name.Parts[0].Text != "abs" || len(call.Args) != 1 || call.Args[0].(LiteralExpr).Value.Integer != 1 {
			t.Fatal("selected INSERT scalar lost call", q, rows)
		}
	}
}

func TestQuerySetValuesLockInformation(t *testing.T) {
	for _, mode := range []string{"UPDATE", "NO KEY UPDATE", "SHARE", "KEY SHARE"} {
		for _, shape := range []string{"VALUES(1) FOR %s", "(VALUES(1) FOR %s)", "((VALUES(1) ORDER BY 1 LIMIT 1 FOR %s))"} {
			values := fmt.Sprintf(shape, mode)
			for _, q := range []string{"INSERT INTO t " + values, "SELECT * FROM (" + values + ") AS d"} {
				statements, err := Parse(q)
				if err != nil {
					continue
				} // An explicit parser diagnostic also prevents information loss.
				if len(statements) != 1 {
					t.Fatal(q, statements)
				}
				switch stmt := statements[0].(type) {
				case *Insert:
					if stmt.Source == nil || stmt.Source.Select == nil || len(stmt.Source.Select.Locking) == 0 {
						t.Fatalf("INSERT erased VALUES locking: %q %#v", q, stmt)
					}
				case *Select:
					alias, ok := stmt.From.(*AliasReference)
					if !ok {
						t.Fatal(q, stmt.From)
					}
					derived, ok := alias.Input.(*DerivedQuery)
					if !ok || len(derived.Query.Locking) == 0 {
						t.Fatalf("derived conversion erased VALUES locking: %q %#v", q, alias.Input)
					}
				}
			}
		}
	}
}
