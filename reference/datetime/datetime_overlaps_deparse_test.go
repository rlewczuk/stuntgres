package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatetimeOverlapsStoredDeparse(t *testing.T) {
	datetimeOverlapsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE ov_def(t time,s timestamp,z timestamptz,o timetz,i interval,d date)", 0))
		for _, column := range []string{"t", "s", "z", "o"} {
			shapes := 4
			if column == "o" {
				shapes = 1
			}
			for mask := range shapes {
				name := fmt.Sprintf("ov_def_%s_%d", column, mask)
				second, fourth := column, column
				if mask&1 != 0 {
					second = "i"
				}
				if mask&2 != 0 {
					fourth = "i"
				}
				args := column + ", " + second + ", " + column + ", " + fourth
				sqlExpression := datetimeOverlapsPair(column, second, column, fourth)
				expected := " SELECT ((" + column + ", " + second + ") OVERLAPS (" + column + ", " + fourth + ")) AS hit\n   FROM ov_def;"
				// SQL syntax retains OVERLAPS; ordinary calls retain quoted function syntax.
				for _, ordinary := range []bool{false, true} {
					suffix := "sql"
					expression := sqlExpression
					want := expected
					if ordinary {
						suffix = "call"
						expression = "pg_catalog.overlaps(" + args + ")"
						want = " SELECT \"overlaps\"(" + args + ") AS hit\n   FROM ov_def;"
					}
					view := name + "_" + suffix
					t.Run(view, func(t *testing.T) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
						run(fixture.Exec("CREATE VIEW "+view+" AS SELECT "+expression+" AS hit FROM ov_def", 0))
						for _, prepared := range []bool{false, true} {
							s := fixture.Select("SELECT pg_get_viewdef('"+view+"'::regclass,true)", []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{want}})
							s.Prepared = prepared
							run(s)
						}
					})
				}
			}
		}
		t.Run("local_clock_duration", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE VIEW ov_def_local AS SELECT (d,t) OVERLAPS (d,t) AS hit FROM ov_def", 0))
			expected := " SELECT ((d, t) OVERLAPS (d, t)) AS hit\n   FROM ov_def;"
			run(fixture.Select("SELECT pg_get_viewdef('ov_def_local'::regclass,true)", []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{expected}}))
		})
		run(fixture.Exec("CREATE TABLE ov_def_generated(a timestamp,b timestamp,hit boolean GENERATED ALWAYS AS ((a,b) OVERLAPS (a,b)) STORED)", 0))
		t.Run("generated_expression", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT pg_get_expr(d.adbin,d.adrelid) FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='ov_def_generated'", []string{"pg_get_expr"}, []string{"TEXT"}, [][]any{{"((a, b) OVERLAPS (a, b))"}}))
		})
		if b.PG {
			// PostgreSQL catalog capability: native does not implement pg_attribute.
			t.Run("postgres_bool_type_oid_typmod", func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT atttypid::int AS type_oid,atttypmod FROM pg_catalog.pg_attribute WHERE attrelid='ov_def_generated'::regclass AND attname='hit'", []string{"type_oid", "atttypmod"}, []string{"INT4", "INT4"}, [][]any{{int64(16), int64(-1)}}))
			})
		} else {
			// Public native driver exports BOOL metadata, not an OID/typmod hook.
			// Exercise empty stored-column metadata in direct and prepared modes;
			// provider/binder units independently establish OID16/typmod-1.
			t.Run("native_generated_bool_driver_metadata", func(t *testing.T) {
				s := fixture.Select("SELECT hit FROM ov_def_generated", []string{"hit"}, []string{"BOOL"}, [][]any{})
				s.CheckMetadata = datetimeOverlapsStoredMetadata(false)
				datetimeOverlapsBoth(t, p, b, c, s)
			})
		}
	})
}
