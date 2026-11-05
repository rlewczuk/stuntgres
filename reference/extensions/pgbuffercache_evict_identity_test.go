package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheEvictionIdentity(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bc_evict_identity_owner"})
	t.Run("resident_routines", func(t *testing.T) {
		bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			bcDDL(t, p, b, c, "CREATE ROLE bc_evict_identity_owner", "CREATE SCHEMA bc_moved", "GRANT USAGE ON SCHEMA bc_part,bc_moved TO bc_evict_identity_owner")
			bceHeap(t, p, b, c, "identity_evict_heap")
			for _, spec := range []struct{ name, formal, argument, wrapper string; single bool }{
				{"pg_buffercache_evict", "integer", "NULL::integer", "saved_single", true},
				{"pg_buffercache_evict_relation", "regclass", "NULL::regclass", "saved_relation", false},
				{"pg_buffercache_evict_all", "", "", "saved_all", false},
			} {
				t.Run(spec.name, func(t *testing.T) {
					signature := "bc_part." + spec.name + "(" + spec.formal + ")"
					var original int64
					if err := c.QueryRowContext(p.Ctx, "SELECT $1::regprocedure::oid::bigint", signature).Scan(&original); err != nil || original <= 0 {
						t.Fatal("authentic routine identity", original, err)
					}
					bcDDL(t, p, b, c, "CREATE VIEW bc_part."+spec.wrapper+" AS SELECT * FROM bc_part."+spec.name+"("+spec.argument+")")
					check := func() {
						t.Helper()
						if spec.formal != "" {
							bcBoth(t, p, b, c, bceNullStep("SELECT * FROM bc_part."+spec.wrapper, spec.single))
						} else {
							bcBoth(t, p, b, c, fixture.Select("SELECT buffers_evicted>=0 AS evicted,buffers_flushed>=0 AS flushed,buffers_skipped>=0 AS skipped FROM bc_part."+spec.wrapper, []string{"evicted", "flushed", "skipped"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
						}
					}
					check()
					kept := spec.name + "_kept"
					for _, q := range []string{
						"ALTER FUNCTION " + signature + " OWNER TO bc_evict_identity_owner",
						"ALTER FUNCTION " + signature + " RENAME TO " + kept,
						"ALTER FUNCTION bc_part." + kept + "(" + spec.formal + ") SET SCHEMA bc_moved",
						"ALTER EXTENSION pg_buffercache DROP FUNCTION bc_moved." + kept + "(" + spec.formal + ")",
					} {
						bcDDL(t, p, b, c, q)
						check()
					}
					moved := "bc_moved." + kept + "(" + spec.formal + ")"
					bcBoth(t, p, b, c, fixture.Select("SELECT $1::regprocedure::oid::bigint AS oid,pg_get_userbyid(proowner) AS owner FROM pg_proc WHERE oid=$1::regprocedure", []string{"oid", "owner"}, []string{"INT8", "NAME"}, [][]any{{original, "bc_evict_identity_owner"}}, moved))
					bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid=1255 AND objid=$1::oid AND refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='pg_buffercache') AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, original))
					bcDDL(t, p, b, c, "CREATE FUNCTION bc_part."+spec.name+"("+spec.formal+") RETURNS integer LANGUAGE SQL AS 'SELECT 77'")
					shadowArg := spec.argument
					if spec.formal == "integer" {
						shadowArg = "-1"
					} else if spec.formal == "regclass" {
						shadowArg = "'bc_part.identity_evict_heap'::regclass"
					}
					bcBoth(t, p, b, c, fixture.Select("SELECT bc_part."+spec.name+"("+shadowArg+") AS sentinel", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(77)}}))
					check()
					warm := bceWarm(t, p, b, c, "identity_evict_heap")
					bceClean(t, p, b, c)
					q := "SELECT * FROM bc_moved." + kept + "()"
					var args []any
					if spec.formal != "" {
						q = "SELECT * FROM bc_moved." + kept + "($1::" + spec.formal + ")"
						args = []any{"bc_part.identity_evict_heap"}
						if spec.single {
							args = []any{bceMainSlot(t, warm)}
						}
					}
					t.Run("renamed_owner_still_not_superuser", func(t *testing.T) {
						bcDDL(t, p, b, c, "SET ROLE bc_evict_identity_owner")
						defer c.ExecContext(p.Ctx, "RESET ROLE")
						s := bceRuntimeDenial(q, spec.name)
						s.Args = args
						bcFailure(t, p, b, c, s, "execute")
					})
					got := bceCall(t, p, b, c, true, spec.single, q, args...)
					if spec.single {
						bceExact(t, got, true, false)
					} else {
						bceCounts(t, b, got, bcCapacity(t, p, c), 1, 0)
					}
					bceCold(t, p, b, c, "identity_evict_heap")
					bcDDL(t, p, b, c, "ALTER EXTENSION pg_buffercache ADD FUNCTION "+moved)
					check()
					bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid=1255 AND objid=$1::oid AND refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='pg_buffercache') AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, original))
				})
			}
			// Prior pages declaration and ordinary convenience-view shape survive.
			bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM "+bcFunction(), []string{"n"}, []string{"INT8"}, [][]any{{bcCapacity(t, p, c)}}))
			bcUnused(t, p, b, c)
		})
	})
	t.Run("installation_atomicity", func(t *testing.T) {
		bcScenario(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			absent := func() {
				t.Helper()
				for _, q := range []string{
					"SELECT count(*) AS n FROM pg_extension WHERE extname='pg_buffercache'",
					"SELECT count(*) AS n FROM pg_proc WHERE pronamespace='bc_part'::regnamespace AND proname IN ('pg_buffercache_pages','pg_buffercache_evict','pg_buffercache_evict_relation','pg_buffercache_evict_all','pg_buffercache_summary','pg_buffercache_usage_counts')",
					"SELECT count(*) AS n FROM pg_class WHERE relnamespace='bc_part'::regnamespace AND relname='pg_buffercache'",
					"SELECT count(*) AS n FROM pg_type WHERE typnamespace='bc_part'::regnamespace AND typname IN ('pg_buffercache','_pg_buffercache')",
				} {
					bcBoth(t, p, b, c, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
			}
			for _, rollback := range []string{"ROLLBACK", "ROLLBACK TO evict_save"} {
				bcDDL(t, p, b, c, "BEGIN", "SAVEPOINT evict_save", "CREATE EXTENSION pg_buffercache SCHEMA bc_part", rollback)
				absent()
				if rollback != "ROLLBACK" {
					bcDDL(t, p, b, c, "COMMIT")
				}
			}
			bcDDL(t, p, b, c, "CREATE EXTENSION pg_buffercache SCHEMA bc_part")
			for _, name := range []string{"pg_buffercache_evict", "pg_buffercache_evict_relation", "pg_buffercache_evict_all"} {
				q := fmt.Sprintf("SELECT count(*) AS n FROM pg_proc WHERE pronamespace='bc_part'::regnamespace AND proname='%s'", name)
				bcBoth(t, p, b, c, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			}
		})
	})
}

// Each collision starts with an independent extension-free catalog. In
// particular, the first declaration comes from the historical 1.4--1.5
// script, not the later DROP/CREATE of the two-OUT 1.6 declaration.
func TestReferencePgBuffercacheEvictionInstallationCollisions(t *testing.T) {
	for _, tc := range []struct {
		label, name, args, input, statement, script string
		line int
	}{
		{
			label: "first_buffer", name: "pg_buffercache_evict", args: "integer", input: "1",
			statement: "CREATE FUNCTION pg_buffercache_evict(IN int)\nRETURNS bool\nAS '$libdir/pg_buffercache', 'pg_buffercache_evict'\nLANGUAGE C PARALLEL SAFE VOLATILE STRICT",
			script: "pg_buffercache--1.4--1.5.sql", line: 3,
		},
		{
			label: "middle_relation", name: "pg_buffercache_evict_relation", args: "regclass", input: "0::regclass",
			statement: "CREATE FUNCTION pg_buffercache_evict_relation(\n    IN regclass,\n    OUT buffers_evicted int4,\n    OUT buffers_flushed int4,\n    OUT buffers_skipped int4)\nAS '$libdir/pg_buffercache', 'pg_buffercache_evict_relation'\nLANGUAGE C PARALLEL SAFE VOLATILE STRICT",
			script: "pg_buffercache--1.5--1.6.sql", line: 33,
		},
		{
			label: "last_all", name: "pg_buffercache_evict_all",
			statement: "CREATE FUNCTION pg_buffercache_evict_all(\n    OUT buffers_evicted int4,\n    OUT buffers_flushed int4,\n    OUT buffers_skipped int4)\nAS '$libdir/pg_buffercache', 'pg_buffercache_evict_all'\nLANGUAGE C PARALLEL SAFE VOLATILE",
			script: "pg_buffercache--1.5--1.6.sql", line: 41,
		},
	} {
		t.Run(tc.label, func(t *testing.T) {
			bcScenario(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				bcDDL(t, p, b, c, fmt.Sprintf("CREATE FUNCTION bc_part.%s(%s) RETURNS integer LANGUAGE SQL AS 'SELECT 77'", tc.name, tc.args))
				var originalOID int64
				identity := fmt.Sprintf("SELECT 'bc_part.%s(%s)'::regprocedure::oid::bigint", tc.name, tc.args)
				if err := c.QueryRowContext(p.Ctx, identity).Scan(&originalOID); err != nil { t.Fatal(identity, err) }
				failure := bcError("CREATE EXTENSION pg_buffercache SCHEMA bc_part", "42723", fmt.Sprintf("function %q already exists with same argument types", tc.name))
				failure.SelectRows = false
				// Released SQL determines statement text independently; the
				// complete Context and lines 3/33/41 are pinned against 18.6.
				failure.Diagnostic.Context = "SQL statement \"" + tc.statement + "\"\nextension script file \"" + tc.script + fmt.Sprintf("\", near line %d", tc.line)
				bcFailure(t, p, b, c, failure, "execute")
				bcBoth(t, p, b, c, fixture.Select(identity+" AS original_oid", []string{"original_oid"}, []string{"INT8"}, [][]any{{originalOID}}))
				call := fmt.Sprintf("SELECT bc_part.%s(%s) AS sentinel", tc.name, tc.input)
				bcBoth(t, p, b, c, fixture.Select(call, []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(77)}}))
				for _, query := range []string{
					"SELECT count(*) AS n FROM pg_extension WHERE extname='pg_buffercache'",
					fmt.Sprintf("SELECT count(*) AS n FROM pg_proc WHERE pronamespace='bc_part'::regnamespace AND proname IN ('pg_buffercache_pages','pg_buffercache_evict','pg_buffercache_evict_relation','pg_buffercache_evict_all','pg_buffercache_summary','pg_buffercache_usage_counts') AND proname<>'%s'", tc.name),
					"SELECT count(*) AS n FROM pg_class WHERE relnamespace='bc_part'::regnamespace AND relname='pg_buffercache'",
					"SELECT count(*) AS n FROM pg_type WHERE typnamespace='bc_part'::regnamespace AND typname IN ('pg_buffercache','_pg_buffercache')",
					fmt.Sprintf("SELECT count(*) AS n FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='bc_part.%s(%s)'::regprocedure AND deptype='e'", tc.name, tc.args),
				} {
					bcBoth(t, p, b, c, fixture.Select(query, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
				bcDDL(t, p, b, c, fmt.Sprintf("DROP FUNCTION bc_part.%s(%s)", tc.name, tc.args), "CREATE EXTENSION pg_buffercache SCHEMA bc_part")
				for _, name := range []string{"pg_buffercache_evict", "pg_buffercache_evict_relation", "pg_buffercache_evict_all"} {
					query := fmt.Sprintf("SELECT count(*) AS n FROM pg_proc WHERE pronamespace='bc_part'::regnamespace AND proname='%s'", name)
					bcBoth(t, p, b, c, fixture.Select(query, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
				}
			})
		})
	}
}
