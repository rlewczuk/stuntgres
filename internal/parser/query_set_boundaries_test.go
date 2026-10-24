package parser

import (
	"fmt"
	"testing"
)

func TestQuerySetInsertScalarBoundaries(t *testing.T) {
	for _, cell := range []string{"abs(1)", "TRUE AND FALSE"} {
		for _, shape := range []string{"VALUES(%s) ORDER BY 1", "VALUES(%s) LIMIT 1", "(VALUES(%s) LIMIT 1)", "((VALUES(%s) ORDER BY 1 LIMIT 1))"} {
			q := "CREATE TABLE prefix(v INT); /*é*/ INSERT INTO t " + fmt.Sprintf(shape, cell)
			statements, err := Parse(q)
			if err == nil || len(statements) != 0 {
				t.Fatalf("restricted INSERT cells widened: %q statements=%#v error=%v", q, statements, err)
			}
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
	// Keep the same restricted-path row-close diagnostic for bare VALUES and
	// decorated sources; do not invent a new rejection at the call opener.
	for _, q := range []string{
		"INSERT INTO t VALUES(abs(1))",
		"INSERT INTO t VALUES(abs(1)) LIMIT 1",
		"INSERT INTO t VALUES(abs(1)) ORDER BY 1",
	} {
		keywordDiagnostic(t, q, "42601", `syntax error at or near ")"`, "28")
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
