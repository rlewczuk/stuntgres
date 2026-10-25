package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAmcheckHeapOptions(t *testing.T) {
	heapScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		heapInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, tc := range []struct{ args, message, hint string }{
			{"NULL::regclass", "relation cannot be null", ""},
			{"'heap_t',NULL::boolean", "on_error_stop cannot be null", ""},
			{"'heap_t',false,NULL::boolean", "check_toast cannot be null", ""},
			{"'heap_t',false,false,NULL::text", "skip cannot be null", ""},
			{"'heap_t',false,false,'invalid'", "invalid skip option", `Valid skip options are "all-visible", "all-frozen", and "none".`},
			{"NULL::regclass,NULL::boolean,NULL::boolean,NULL::text,0,0", "relation cannot be null", ""},
			{"'heap_t',NULL::boolean,true,'invalid',0,0", "on_error_stop cannot be null", ""},
			{"'heap_t',false,NULL::boolean,'invalid',0,0", "check_toast cannot be null", ""},
			{"'heap_t',false,true,NULL::text,0,0", "skip cannot be null", ""},
			{"'heap_t',false,true,'invalid',0,0", "invalid skip option", `Valid skip options are "all-visible", "all-frozen", and "none".`},
		} {
			t.Run("validation/"+tc.args, func(t *testing.T) {
				d := heapDiagnostic("22023", tc.message)
				d.Hint = tc.hint
				heapBoth(t, p, b, c, heapError("SELECT * FROM verify_heapam("+tc.args+")", d))
			})
		}
		for _, q := range []string{"CREATE TABLE heap_parent(a INT) PARTITION BY RANGE(a)", "CREATE INDEX heap_index ON heap_t(a)", "CREATE VIEW heap_view AS SELECT * FROM heap_t"} {
			run(fixture.Exec(q, 0))
		}
		for _, tc := range []struct{ name, kind string }{{"heap_parent", "partitioned tables"}, {"heap_index", "indexes"}, {"heap_view", "views"}} {
			for _, physical := range []bool{false, true} {
				args := "'"+tc.name+"'"
				if physical { args += ",false,true,'none',0,0" }
				t.Run("kind/"+args, func(t *testing.T) {
					d := heapDiagnostic("42809", `cannot check relation "`+tc.name+`"`)
					d.Detail = "This operation is not supported for "+tc.kind+"."
					heapBoth(t, p, b, c, heapError("SELECT * FROM verify_heapam("+args+")", d))
				})
			}
		}
		// Only the physical phase differs: native has no physical block map.
		physical := func(name, args, pgMessage string) {
			t.Helper()
			t.Run(name, func(t *testing.T) {
				q := "SELECT * FROM verify_heapam("+args+")"
				s := heapEmpty(q)
				if !b.PG {
					s = heapError(q, heapPhysicalDiagnostic())
				} else if pgMessage != "" {
					s = heapError(q, heapDiagnostic("22023", pgMessage))
				}
				heapBoth(t, p, b, c, s)
			})
		}
		physical("empty-out-of-range", "'heap_t',false,false,'none',5,8", "")
		run(fixture.Exec("INSERT INTO heap_t VALUES(1,'small')", 1))
		if b.PG {
			var blocks int64
			if err := c.QueryRowContext(p.Ctx, "SELECT pg_relation_size('heap_t')/current_setting('block_size')::bigint").Scan(&blocks); err != nil || blocks != 1 {
				t.Fatal("one-block physical fixture", blocks, err)
			}
		}
		for _, tc := range []struct{ name, args, message string }{
			{"zero", "'heap_t',false,false,'none',0,0", ""},
			{"negative-start", "'heap_t',false,false,'none',-1,0", "starting block number must be between 0 and 0"},
			{"large-start", "'heap_t',false,false,'none',1,0", "starting block number must be between 0 and 0"},
			{"negative-end", "'heap_t',false,false,'none',0,-1", "ending block number must be between 0 and 0"},
			{"large-end", "'heap_t',false,false,'none',0,1", "ending block number must be between 0 and 0"},
			{"both-invalid-start-first", "'heap_t',false,false,'none',-1,-1", "starting block number must be between 0 and 0"},
			{"toast", "'heap_t',false,true", ""},
			{"visible", "'heap_t',false,false,'all-visible'", ""},
			{"frozen", "'heap_t',false,false,'ALL-FROZEN'", ""},
			{"start-only", "'heap_t',startblock=>0", ""},
			{"end-only", "'heap_t',endblock=>0", ""},
		} { physical(tc.name, tc.args, tc.message) }
		for _, args := range []string{"'heap_t'", "'heap_t',true,false,'NONE',NULL,NULL", "'heap_t',false,false,'nOnE'"} {
			t.Run("logical/"+args, func(t *testing.T) { heapBoth(t, p, b, c, heapEmpty("SELECT * FROM verify_heapam("+args+")")) })
		}
		run(fixture.Exec("CREATE TABLE heap_many(a INT,b TEXT)", 0))
		for i := range 40 { run(fixture.Exec("INSERT INTO heap_many VALUES($1,$2)", 1, i, strings.Repeat("inline", 100))) }
		if b.PG {
			var blocks int64
			if err := c.QueryRowContext(p.Ctx, "SELECT pg_relation_size('heap_many')/current_setting('block_size')::bigint").Scan(&blocks); err != nil || blocks < 2 {
				t.Fatal("multi-block physical fixture", blocks, err)
			}
		}
		physical("valid-reversed", "'heap_many',false,false,'none',1,0", "")
		if b.PG {
			// Native SQL does not expose custom table-AM creation; this released
			// diagnostic is witnessed with genuine PostgreSQL facilities only.
			t.Run("nonheap", func(t *testing.T) {
				if _, err := c.ExecContext(p.Ctx, "CREATE ACCESS METHOD heap_nonheap TYPE TABLE HANDLER heap_tableam_handler"); err != nil { t.Fatal(err) }
				defer c.ExecContext(p.Ctx, "DROP ACCESS METHOD heap_nonheap CASCADE")
				if _, err := c.ExecContext(p.Ctx, "CREATE TABLE heap_nonheap_t(a INT) USING heap_nonheap"); err != nil { t.Fatal(err) }
				heapBoth(t, p, b, c, heapError("SELECT * FROM verify_heapam('heap_nonheap_t')", heapDiagnostic("0A000", "only heap AM is supported")))
			})
		}
		t.Run("foreign-kind", func(t *testing.T) {
			defer c.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS heap_fdw CASCADE")
			for _, q := range []string{"CREATE FOREIGN DATA WRAPPER heap_fdw", "CREATE SERVER heap_server FOREIGN DATA WRAPPER heap_fdw", "CREATE FOREIGN TABLE heap_foreign(a INT) SERVER heap_server"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil { t.Fatal(q, err) }
			}
			d := heapDiagnostic("42809", `cannot check relation "heap_foreign"`)
			d.Detail = "This operation is not supported for foreign tables."
			heapBoth(t, p, b, c, heapError("SELECT * FROM verify_heapam('heap_foreign')", d))
		})
	})
}
