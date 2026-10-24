package table

import (
	"errors"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestCheckCreateShape(t *testing.T) {
	for _, tc := range []struct {
		sql                     string
		temporary, seen         bool
		existing                bool
		code, message, position string
	}{
		{sql: "CREATE TABLE t(a int)"},
		{sql: "CREATE TABLE t(a int)", seen: true, code: "42P07", message: `relation "t" already exists`},
		{sql: "CREATE TEMP TABLE t(a int) PARTITION BY RANGE(a)", temporary: true, code: "0A000", message: "temporary partition definitions are unsupported", position: "28"},
		{sql: "CREATE TEMP TABLE t PARTITION OF p DEFAULT", temporary: true, seen: true, code: "0A000", message: "temporary partition definitions are unsupported", position: "21"},
		{sql: "CREATE TABLE t(a int) ON COMMIT DROP", seen: true, code: "42P16", message: "ON COMMIT can only be used on temporary tables"},
		{sql: "CREATE TEMP TABLE t(a int) ON COMMIT DROP", temporary: true},
		{sql: "CREATE TABLE s.t(a int) INHERITS (p) PARTITION BY RANGE(a)", seen: true, code: "42P07", message: `relation "t" already exists`},
		{sql: "CREATE TABLE IF NOT EXISTS t(a int) INHERITS (p) PARTITION BY RANGE(a)", seen: true, existing: true},
		{sql: "CREATE TABLE t(a int) INHERITS (p) PARTITION BY RANGE(a)", code: "42P17", message: "cannot create partitioned table as inheritance child"},
	} {
		statements, err := parser.Parse(tc.sql)
		if err != nil {
			t.Fatal(tc.sql, err)
		}
		existing, err := CheckCreateShape(statements[0].(*parser.CreateTable), tc.temporary, tc.seen)
		if existing != tc.existing {
			t.Fatalf("%s: existing = %v", tc.sql, existing)
		}
		if tc.code == "" {
			if err != nil {
				t.Fatalf("%s: %v", tc.sql, err)
			}
			continue
		}
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message || diagnostic.Position != tc.position {
			t.Fatalf("%s: error = %#v", tc.sql, err)
		}
	}
}
