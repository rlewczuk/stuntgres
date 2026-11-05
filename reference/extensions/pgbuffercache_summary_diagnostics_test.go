package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func bcsMissing(query, signature string) fixture.Step {
	s := bcError(query, "42883", "function "+signature+" does not exist")
	s.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	s.Diagnostic.Position = fixture.Position(15)
	return s
}

func TestReferencePgBuffercacheSummaryDiagnostics(t *testing.T) {
	t.Run("resolution", func(t *testing.T) {
		bcScenario(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, name := range bcsRoutines {
				t.Run(name+"_absent", func(t *testing.T) {
					q := "SELECT * FROM bc_part." + name + "()"
					bcFailure(t, p, b, c, bcsMissing(q, "bc_part."+name+"()"), "prepare")
				})
			}
			bcDDL(t, p, b, c, "CREATE EXTENSION pg_buffercache SCHEMA bc_part")
			capacity := bcCapacity(t, p, c)
			for _, name := range bcsRoutines {
				for _, tc := range []struct{ label, argument, signature string }{
					{"integer", "1", "integer"}, {"unknown_null", "NULL", "unknown"}, {"typed_null", "NULL::integer", "integer"},
				} {
					t.Run(name+"_"+tc.label, func(t *testing.T) {
						q := "SELECT * FROM bc_part." + name + "(" + tc.argument + ")"
						bcFailure(t, p, b, c, bcsMissing(q, "bc_part."+name+"("+tc.signature+")"), "prepare")
					})
				}
				t.Run(name+"_missing_schema", func(t *testing.T) {
					q := "SELECT * FROM bc_missing." + name + "()"
					s := bcError(q, "3F000", "schema \"bc_missing\" does not exist")
					s.Diagnostic.Position = fixture.Position(15)
					bcFailure(t, p, b, c, s, "prepare")
				})
				t.Run(name+"_typed_column_list", func(t *testing.T) {
					q := "SELECT * FROM bc_part." + name + "() AS s(x integer)"
					s := bcError(q, "42601", "a column definition list is redundant for a function with OUT parameters")
					s.Diagnostic.Position = fixture.Position(strings.Index(q, "x integer") + 1)
					bcFailure(t, p, b, c, s, "prepare")
				})
			}
			// Wrong arity/shape cannot sample storage or poison later execution.
			bcsNative(t, p, b, c, capacity, 0, nil, [6]int64{capacity})
		})
	})
	for _, name := range bcsRoutines {
		t.Run(name+"_installation_collision", func(t *testing.T) {
			bcScenario(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				bcDDL(t, p, b, c, "CREATE FUNCTION bc_part."+name+"() RETURNS integer LANGUAGE SQL AS 'SELECT 77'")
				var original int64
				if err := c.QueryRowContext(p.Ctx, "SELECT $1::regprocedure::oid::bigint", "bc_part."+name+"()").Scan(&original); err != nil || original <= 0 {
					t.Fatal("collision original identity", original, err)
				}
				statement := "CREATE FUNCTION pg_buffercache_summary(\n    OUT buffers_used int4,\n    OUT buffers_unused int4,\n    OUT buffers_dirty int4,\n    OUT buffers_pinned int4,\n    OUT usagecount_avg float8)\nAS '$libdir/pg_buffercache', 'pg_buffercache_summary'\nLANGUAGE C PARALLEL SAFE"
				line := 6
				if name == "pg_buffercache_usage_counts" {
					statement = "CREATE FUNCTION pg_buffercache_usage_counts(\n    OUT usage_count int4,\n    OUT buffers int4,\n    OUT dirty int4,\n    OUT pinned int4)\nRETURNS SETOF record\nAS '$libdir/pg_buffercache', 'pg_buffercache_usage_counts'\nLANGUAGE C PARALLEL SAFE"
					line = 15
				}
				s := bcError("CREATE EXTENSION pg_buffercache SCHEMA bc_part", "42723", "function \""+name+"\" already exists with same argument types")
				s.SelectRows = false
				s.Diagnostic.Context = "SQL statement \"" + statement + "\"\nextension script file \"pg_buffercache--1.3--1.4.sql\", near line " + fmt.Sprint(line)
				bcFailure(t, p, b, c, s, "execute")
				for _, q := range []string{
					"SELECT count(*) AS n FROM pg_extension WHERE extname='pg_buffercache'",
					"SELECT count(*) AS n FROM pg_class WHERE relnamespace='bc_part'::regnamespace AND relname='pg_buffercache'",
					"SELECT count(*) AS n FROM pg_proc WHERE pronamespace='bc_part'::regnamespace AND proname LIKE 'pg_buffercache%' AND proname<>'" + name + "'",
				} {
					bcBoth(t, p, b, c, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
				bcBoth(t, p, b, c, fixture.Select("SELECT $1::regprocedure::oid::bigint AS oid", []string{"oid"}, []string{"INT8"}, [][]any{{original}}, "bc_part."+name+"()"))
				bcBoth(t, p, b, c, fixture.Select("SELECT bc_part."+name+"() AS sentinel", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(77)}}))
				bcDDL(t, p, b, c, "DROP FUNCTION bc_part."+name+"()", "CREATE EXTENSION pg_buffercache SCHEMA bc_part")
				bcsSummaryBounds(t, bcsQuery(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache_summary()", true, false), bcCapacity(t, p, c), false)
			})
		})
	}
}
