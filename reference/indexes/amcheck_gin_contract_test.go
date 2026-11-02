package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAmcheckGINContract(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "gin_am_addon")
	heapScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		t.Run("installation", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			d := heapDiagnostic("42883", "function gin_index_check(unknown) does not exist")
			d.Hint, d.Position = heapMissingHint, "8"
			heapBoth(t, p, b, c, heapError("SELECT gin_index_check('gin_probe_i')", d))
			tx, err := c.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("CREATE EXTENSION amcheck", 0))
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT count(*) AS members FROM pg_proc WHERE proname='gin_index_check'", []string{"members"}, []string{"INT8"}, [][]any{{int64(1)}}))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Select("SELECT count(*) AS installations FROM pg_extension WHERE extname='amcheck'", []string{"installations"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Select("SELECT count(*) AS members FROM pg_proc WHERE proname='gin_index_check'", []string{"members"}, []string{"INT8"}, [][]any{{int64(0)}}))
		})
		run(fixture.Exec("CREATE EXTENSION amcheck", 0))
		run(fixture.Exec("CREATE TABLE gin_probe(a integer[])", 0))
		run(fixture.Exec("CREATE INDEX gin_probe_i ON gin_probe USING gin(a)", 0))
		run(fixture.Exec("INSERT INTO gin_probe VALUES('{1,1,2}'),('{}'),(NULL)", 3))
		t.Run("declaration", func(t *testing.T) {
			q := `SELECT p.proname::text AS name,p.pronargs,p.proargtypes::text AS args,p.prorettype::bigint AS result,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.pronargdefaults,p.proargdefaults::text AS defaults,p.proretset,p.proisstrict,p.provolatile,p.proparallel,p.prosecdef,p.prosrc,p.probin,l.lanname::text AS language FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='public' AND p.proname='gin_index_check'`
			heapBoth(t, p, b, c, fixture.Select(q,
				[]string{"name", "pronargs", "args", "result", "allargs", "modes", "names", "pronargdefaults", "defaults", "proretset", "proisstrict", "provolatile", "proparallel", "prosecdef", "prosrc", "probin", "language"},
				[]string{"TEXT", "INT2", "TEXT", "INT8", "TEXT", "TEXT", "TEXT", "INT2", "TEXT", "BOOL", "BOOL", "CHAR", "CHAR", "BOOL", "TEXT", "TEXT", "TEXT"},
				[][]any{{"gin_index_check", int64(1), "2205", int64(2278), nil, nil, "{index}", int64(0), nil, false, true, "v", "u", false, "gin_index_check", "$libdir/amcheck", "c"}}))
			// Public catalogs expose reciprocal membership and initial ACLs, not
			// the native manifest's private slot number. No allocator OID is a golden.
			q = `SELECT e.extversion,e.extrelocatable,n.nspname,p.proowner=e.extowner AS owned,p.pronamespace=e.extnamespace AS located,p.proacl=i.initprivs AS initial_acl,has_function_privilege(current_user,p.oid,'EXECUTE') AS executable FROM pg_proc p JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.objsubid=0 AND d.deptype='e' JOIN pg_extension e ON d.refclassid='pg_extension'::regclass AND d.refobjid=e.oid JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_init_privs i ON i.classoid='pg_proc'::regclass AND i.objoid=p.oid AND i.objsubid=0 AND i.privtype='e' WHERE e.extname='amcheck' AND p.proname='gin_index_check'`
			heapBoth(t, p, b, c, fixture.Select(q, []string{"extversion", "extrelocatable", "nspname", "owned", "located", "initial_acl", "executable"}, []string{"TEXT", "BOOL", "NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"1.5", true, "public", true, true, true, true}}))
			var owner string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&owner); err != nil {
				t.Fatal(err)
			}
			s := fixture.Select("SELECT grantor::text,grantee::text,privilege_type::text,is_grantable::text FROM information_schema.routine_privileges WHERE routine_schema='public' AND routine_name='gin_index_check'", []string{"grantor", "grantee", "privilege_type", "is_grantable"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{owner, owner, "EXECUTE", "YES"}})
			s.Bag = true
			heapBoth(t, p, b, c, s)
		})
		for _, q := range []string{
			"SELECT gin_index_check('gin_probe_i') AS ok",
			"SELECT gin_index_check('gin_probe_i'::regclass) AS ok",
			"SELECT gin_index_check(index => 'gin_probe_i') AS ok",
			"SELECT ok FROM gin_index_check('gin_probe_i') AS f(ok)",
		} {
			heapBoth(t, p, b, c, amcheckSelect(q, false))
		}
		for _, q := range []string{
			"SELECT gin_index_check(NULL::regclass) AS ok",
			"SELECT ok FROM gin_index_check(NULL::regclass) AS f(ok)",
		} {
			heapBoth(t, p, b, c, amcheckSelect(q, true))
		}
		heapBoth(t, p, b, c, fixture.Select("SELECT gin_index_check('gin_probe_i') IS NULL AS is_null,gin_index_check(NULL::regclass) IS NULL AS null_input", []string{"is_null", "null_input"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}))
		s := fixture.Select("SELECT gin_index_check(r) AS ok FROM (VALUES('gin_probe_i'::regclass),('gin_probe_i'::regclass),(NULL::regclass),(NULL::regclass)) AS inputs(r)", []string{"ok"}, []string{"VOID"}, [][]any{{""}, {""}, {nil}, {nil}})
		s.Bag = true
		heapBoth(t, p, b, c, s)
		t.Run("held_parameters", func(t *testing.T) {
			q := "SELECT gin_index_check($1::regclass) AS ok"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			var oid int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'gin_probe_i'::regclass::oid::bigint").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			for _, input := range []any{"gin_probe_i", oid, nil, "gin_probe_i", nil} {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, amcheckSelect(q, input == nil, input))
			}
		})
		run(fixture.Exec("CREATE SEQUENCE gin_am_once", 0))
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'gin_probe_i'::regclass::oid::bigint").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec(fmt.Sprintf("ALTER SEQUENCE gin_am_once RESTART WITH %d", oid), 0))
			s := amcheckSelect("SELECT gin_index_check(nextval('gin_am_once')::oid::regclass) AS ok", false)
			s.Prepared = prepared
			run(s)
			run(fixture.Select("SELECT last_value::bigint AS evaluations FROM gin_am_once", []string{"evaluations"}, []string{"INT8"}, [][]any{{oid}}))
		}
		t.Run("installed_identity_and_shadow", func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE SCHEMA gin_am_addon", 0))
			run(fixture.Exec("DROP EXTENSION amcheck", 0))
			run(fixture.Exec("CREATE EXTENSION amcheck SCHEMA gin_am_addon", 0))
			q := "SELECT gin_am_addon.gin_index_check($1::regclass) AS ok"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			run(fixture.Exec("CREATE FUNCTION public.gin_index_check(regclass) RETURNS integer LANGUAGE SQL AS $$ SELECT 42 $$", 0))
			run(fixture.Exec("SET search_path=public,gin_am_addon", 0))
			defer c.ExecContext(p.Ctx, "RESET search_path")
			heapBoth(t, p, b, c, fixture.Select("SELECT gin_index_check('gin_probe') AS ordinary", []string{"ordinary"}, []string{"INT4"}, [][]any{{int64(42)}}))
			heapBoth(t, p, b, c, amcheckSelect("SELECT gin_am_addon.gin_index_check(index => 'gin_probe_i') AS ok", false))
			heapBoth(t, p, b, c, amcheckSelect("SELECT ok FROM gin_am_addon.gin_index_check(index => 'gin_probe_i') AS f(ok)", false))
			heapBoth(t, p, b, c, amcheckSelect("SELECT ok FROM gin_am_addon.gin_index_check(NULL::regclass) AS f(ok)", true))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, amcheckSelect(q, false, "gin_probe_i"))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, amcheckSelect(q, true, nil))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, heapError(q, heapDiagnostic("42809", `"gin_probe" is not an index`), "gin_probe"))
			run(fixture.Exec("RESET search_path", 0))
		})
	})
}
