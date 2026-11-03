package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheDiagnostics(t *testing.T) {
	bcScenario(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		missing := func(q, signature string, position int) fixture.Step {
			s := bcError(q, "42883", "function "+signature+" does not exist")
			s.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			s.Diagnostic.Position = fixture.Position(position)
			return s
		}
		t.Run("absent_installation", func(t *testing.T) {
			q := "SELECT * FROM " + bcFunction()
			bcFailure(t, p, b, c, missing(q, "bc_part.pg_buffercache_pages()", 15), "prepare")
		})
		bcDDL(t, p, b, c, "CREATE EXTENSION pg_buffercache SCHEMA bc_part")
		tests := []struct {
			name  string
			step  fixture.Step
			stage string
		}{
			{"missing_record_definition", bcError("SELECT * FROM bc_part.pg_buffercache_pages()", "42601", "a column definition list is required for functions returning \"record\""), "prepare"},
			{"scalar_record", bcError("SELECT bc_part.pg_buffercache_pages()", "XX000", "return type must be a row type"), "execute"},
			{"seven_outputs", bcError("SELECT * FROM "+bcFrom("bc_part.pg_buffercache_pages", bcNames[:7], bcSQLTypes[:7]), "XX000", "incorrect number of output arguments"), "execute"},
			{"ten_outputs", bcError("SELECT * FROM "+bcFrom("bc_part.pg_buffercache_pages", append(append([]string{}, bcNames...), "extra"), append(append([]string{}, bcSQLTypes...), "integer")), "XX000", "incorrect number of output arguments"), "execute"},
			{"wrong_arity", missing("SELECT * FROM bc_part.pg_buffercache_pages(1)", "bc_part.pg_buffercache_pages(integer)", 15), "prepare"},
			{"missing_schema", bcError("SELECT * FROM bc_missing.pg_buffercache_pages()", "3F000", "schema \"bc_missing\" does not exist"), "prepare"},
		}
		tests[0].step.Diagnostic.Position = fixture.Position(15)
		tests[5].step.Diagnostic.Position = fixture.Position(15)
		wrongTypes := append([]string{}, bcSQLTypes...)
		wrongTypes[0] = "bigint"
		s := bcError("SELECT * FROM "+bcFrom("bc_part.pg_buffercache_pages", bcNames, wrongTypes), "42804", "function return row and query-specified return row do not match")
		s.Diagnostic.Detail = "Returned type integer at ordinal position 1, but query expects bigint."
		tests = append(tests, struct {
			name  string
			step  fixture.Step
			stage string
		}{"wrong_output_type", s, "execute"})
		duplicate := append([]string{}, bcNames...)
		duplicate[1] = duplicate[0]
		s = bcError("SELECT * FROM "+bcFrom("bc_part.pg_buffercache_pages", duplicate, bcSQLTypes), "42701", "column name \"bufferid\" specified more than once")
		tests = append(tests, struct {
			name  string
			step  fixture.Step
			stage string
		}{"duplicate_output_name", s, "prepare"})
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) { bcFailure(t, p, b, c, tc.step, tc.stage) })
		}
		bcDDL(t, p, b, c, "SET search_path=pg_catalog")
		defer c.ExecContext(p.Ctx, "RESET search_path")
		t.Run("unqualified_not_visible", func(t *testing.T) {
			q := "SELECT * FROM pg_buffercache_pages()"
			bcFailure(t, p, b, c, missing(q, "pg_buffercache_pages()", strings.Index(q, "pg_buffercache_pages")+1), "prepare")
		})
		// A failed call cannot poison later public source execution. Eight is
		// a genuine historical prefix, not a coerced nine-column declaration.
		q := "SELECT * FROM " + bcFrom("bc_part.pg_buffercache_pages", bcNames[:8], bcSQLTypes[:8]) + " WHERE false"
		success := fixture.Select(q, bcNames[:8], bcTypes[:8], nil)
		success.CheckMetadata = func(t *testing.T, rows *sql.Rows) { bcMetadata(t, rows, b.PG, false, bcNames[:8], bcTypes[:8]) }
		bcBoth(t, p, b, c, success)
	})
}
