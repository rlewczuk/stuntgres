package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSystemRowsIdentity(t *testing.T) {
	srCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		srDeclaration(t, p, b, c)
		srRun(t, p, b, c, fixture.Exec("CREATE VIEW sr_view AS SELECT id,payload FROM sr TABLESAMPLE system_rows(3)", 0))
		srBoth(t, p, b, c, srCount("SELECT count(*) AS n FROM sr_view", 3))
		t.Run("restrict_dependency", func(t *testing.T) {
			s := fixture.ImplicitError("DROP EXTENSION tsm_system_rows RESTRICT", fixture.Diagnostic{
				Code: "2BP01", Message: "cannot drop extension tsm_system_rows because other objects depend on it",
				Detail: "view sr_view depends on function system_rows(internal)",
				Hint:   "Use DROP ... CASCADE to drop the dependent objects too.",
			})
			srBoth(t, p, b, c, s)
		})
		srRun(t, p, b, c, fixture.Exec("CREATE VIEW sr_all AS SELECT id,payload FROM sr TABLESAMPLE system_rows(11)", 0))
		srBoth(t, p, b, c, fixture.Select("SELECT id,payload FROM sr_all ORDER BY id", srNames, srTypes, srRows()))
		var original int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'system_rows(internal)'::regprocedure::oid").Scan(&original); err != nil || original <= 0 {
			t.Fatal("installed procedure identity", original, err)
		}
		srRun(t, p, b, c, fixture.Exec("CREATE SCHEMA sr_moved", 0))
		srRun(t, p, b, c, fixture.Exec("ALTER EXTENSION tsm_system_rows SET SCHEMA sr_moved", 0))
		srBoth(t, p, b, c, srSubset("sr", "sr_moved.system_rows", "3", 3))
		srRun(t, p, b, c, fixture.Exec("ALTER FUNCTION sr_moved.system_rows(internal) RENAME TO genuine_rows", 0))
		srBoth(t, p, b, c, srSubset("sr", "sr_moved.genuine_rows", "3", 3))
		srBoth(t, p, b, c, srCount("SELECT count(*) AS n FROM sr_view", 3))
		srBoth(t, p, b, c, fixture.Select("SELECT 'sr_moved.genuine_rows(internal)'::regprocedure::oid::bigint AS identity", []string{"identity"}, []string{"INT8"}, [][]any{{original}}))
		// An ordinary callable SQL function with the reused name and a different
		// signature cannot inherit the moved provider's handler capability.
		t.Run("name_reuse", func(t *testing.T) {
			srRun(t, p, b, c, fixture.Exec("CREATE FUNCTION system_rows(integer) RETURNS integer LANGUAGE SQL AS 'SELECT 77'", 0))
			srBoth(t, p, b, c, fixture.Select("SELECT system_rows(1) AS value", []string{"value"}, []string{"INT4"}, [][]any{{int64(77)}}))
			srBoth(t, p, b, c, srError("SELECT * FROM sr TABLESAMPLE system_rows(1)", "42704", "tablesample method system_rows does not exist", 30))
			srBoth(t, p, b, c, srSubset("sr", "sr_moved.genuine_rows", "3", 3))
			srRun(t, p, b, c, fixture.Exec("DROP FUNCTION system_rows(integer)", 0))
		})
		t.Run("internal_wrong_result", func(t *testing.T) {
			// Arbitrary internal/C ABI declarations are an external PostgreSQL
			// mechanism. Native covers the real binder with typed descriptors.
			if !b.PG {
				return
			}
			srRun(t, p, b, c, fixture.Exec("CREATE FUNCTION system_rows(internal) RETURNS integer LANGUAGE internal AS 'int4abs'", 0))
			srBoth(t, p, b, c, srError("SELECT * FROM sr TABLESAMPLE system_rows(1)", "42809", "function system_rows must return type tsm_handler", 30))
			srBoth(t, p, b, c, srSubset("sr", "sr_moved.genuine_rows", "3", 3))
			srRun(t, p, b, c, fixture.Exec("DROP FUNCTION system_rows(internal)", 0))
		})
		srRun(t, p, b, c, fixture.Exec("ALTER FUNCTION sr_moved.genuine_rows(internal) RENAME TO system_rows", 0))
		srRun(t, p, b, c, fixture.Exec("ALTER EXTENSION tsm_system_rows SET SCHEMA public", 0))
		h := srHeld(t, p, c, "SELECT count(*) AS n FROM sr TABLESAMPLE system_rows($1)")
		srRun(t, p, b, h, srCount(h.SQL, 3, int64(3)))
		srRun(t, p, b, c, fixture.Exec("DROP EXTENSION tsm_system_rows CASCADE", 0))
		srBoth(t, p, b, c, srCount("SELECT count(*) AS n FROM pg_class WHERE relname IN('sr_view','sr_all') AND relnamespace='public'::regnamespace", 0))
		t.Run("held_dropped_handler", func(t *testing.T) {
			if b.PG {
				// Bounded upstream cache defect, tied to this genuinely captured
				// handler identity rather than an allocator-specific golden.
				want := fixture.ControlsReferenceDiagnostic("XX000", fmt.Sprintf("cache lookup failed for function %d", original))
				srQueryDiagnostic(t, p, h, h.SQL, want, int64(3))
				return
			}
			srRun(t, p, b, h, srError(h.SQL, "42704", "tablesample method system_rows does not exist", 42, int64(3)))
		})
		srRun(t, p, b, c, fixture.Exec("CREATE EXTENSION tsm_system_rows", 0))
		srBoth(t, p, b, c, srCount(h.SQL, 3, int64(3)))
		t.Run("held_recreated_handler", func(t *testing.T) {
			if b.PG {
				want := fixture.ControlsReferenceDiagnostic("XX000", fmt.Sprintf("cache lookup failed for function %d", original))
				srQueryDiagnostic(t, p, h, h.SQL, want, int64(3))
				return
			}
			srRun(t, p, b, h, srCount(h.SQL, 3, int64(3)))
		})
		srDeclaration(t, p, b, c)
	})
}

func srDeclaration(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	srBoth(t, p, b, c, fixture.Select("SELECT extversion,extrelocatable,extnamespace='public'::regnamespace AS namespace,extconfig IS NULL AS no_config,extcondition IS NULL AS no_condition,obj_description(oid,'pg_extension') AS comment FROM pg_extension WHERE extname='tsm_system_rows'", []string{"extversion", "extrelocatable", "namespace", "no_config", "no_condition", "comment"}, []string{"TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT"}, [][]any{{"1.0", true, true, true, true, "TABLESAMPLE method which accepts number of rows as a limit"}}))
	srBoth(t, p, b, c, fixture.Select("SELECT version,superuser,trusted,relocatable,schema,requires FROM pg_available_extension_versions WHERE name='tsm_system_rows' AND version='1.0'", []string{"version", "superuser", "trusted", "relocatable", "schema", "requires"}, []string{"TEXT", "BOOL", "BOOL", "BOOL", "NAME", "_NAME"}, [][]any{{"1.0", true, true, true, nil, nil}}))
	srBoth(t, p, b, c, fixture.Select("SELECT p.pronargs::integer AS nargs,p.proargtypes::text AS arguments,p.prorettype::bigint AS result,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.oid='system_rows(internal)'::regprocedure", []string{"nargs", "arguments", "result", "strict", "volatility", "parallel", "source", "binary", "language"}, []string{"INT4", "TEXT", "INT8", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "2281", int64(3310), true, "v", "u", "tsm_system_rows_handler", "$libdir/tsm_system_rows", "c"}}))
	srBoth(t, p, b, c, srCount("SELECT count(*) AS n FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.refclassid='pg_extension'::regclass AND e.extname='tsm_system_rows' AND d.deptype='e' AND d.classid='pg_proc'::regclass AND d.objid='system_rows(internal)'::regprocedure", 1))
	srBoth(t, p, b, c, srCount("SELECT count(*) AS n FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.refclassid='pg_extension'::regclass AND e.extname='tsm_system_rows' AND d.deptype='e'", 1))
}
