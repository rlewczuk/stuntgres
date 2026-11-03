package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgvisibilityCheckMetadata(t *testing.T) {
	pvcCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT p.proname::text AS name,p.pronargs::integer AS nargs,p.proargtypes::text AS args,p.prorettype::bigint AS result,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.proretset AS sets,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosecdef AS definer,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language,p.prorows::integer AS rows FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='pv_part' AND p.proname IN ('pg_check_visible','pg_check_frozen','pg_truncate_visibility_map') ORDER BY p.proname"
		pvBoth(t, p, b, c, fixture.Select(q,
			[]string{"name", "nargs", "args", "result", "allargs", "modes", "names", "sets", "strict", "volatility", "parallel", "definer", "source", "binary", "language", "rows"},
			[]string{"TEXT", "INT4", "TEXT", "INT8", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT", "INT4"},
			[][]any{
				{"pg_check_frozen", int64(1), "2205", int64(27), "{2205,27}", "{i,o}", "{\"\",t_ctid}", true, true, "v", "u", false, "pg_check_frozen", "$libdir/pg_visibility", "c", int64(1000)},
				{"pg_check_visible", int64(1), "2205", int64(27), "{2205,27}", "{i,o}", "{\"\",t_ctid}", true, true, "v", "u", false, "pg_check_visible", "$libdir/pg_visibility", "c", int64(1000)},
				{"pg_truncate_visibility_map", int64(1), "2205", int64(2278), nil, nil, nil, false, true, "v", "u", false, "pg_truncate_visibility_map", "$libdir/pg_visibility", "c", int64(0)},
			}))
		pvBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname='pg_visibility' AND d.classid='pg_proc'::regclass AND d.deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(8)}}))
		q = "SELECT p.proname::text AS name,has_function_privilege('pg_stat_scan_tables',p.oid,'EXECUTE') AS scan,has_function_privilege('pg_monitor',p.oid,'EXECUTE') AS monitor,i.initprivs=p.proacl AS initial FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_init_privs i ON i.objoid=p.oid AND i.classoid='pg_proc'::regclass AND i.objsubid=0 AND i.privtype='e' WHERE n.nspname='pv_part' AND p.proname IN ('pg_check_visible','pg_check_frozen','pg_truncate_visibility_map') ORDER BY p.proname"
		pvBoth(t, p, b, c, fixture.Select(q, []string{"name", "scan", "monitor", "initial"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"pg_check_frozen", true, true, true}, {"pg_check_visible", true, true, true}, {"pg_truncate_visibility_map", false, false, true}}))
		pvcChecks(t, p, b, c, nil, nil)
		pvcTruncate(t, p, b, c, nil)
		pvcTruncate(t, p, b, c, "pv_part.heap")
		for _, fn := range pvcFunctions[:2] {
			for _, q := range []string{"SELECT pv_part." + fn + "(NULL::regclass) AS result", "SELECT x.ct FROM pv_part." + fn + "(NULL::regclass) AS x(ct)"} {
				name := "result"
				if q[7:8] == "x" {
					name = "ct"
				}
				pvBoth(t, p, b, c, pvMetadata(fixture.Select(q, []string{name}, []string{"TID"}, nil), b.PG))
			}
		}
	})
}

func TestReferencePgvisibilityCheckBinding(t *testing.T) {
	pvcCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		oid := pvOID(t, p, c, "pv_part.heap")
		for _, fn := range pvcFunctions {
			name, typ := "t_ctid", "TID"
			var expected [][]any
			if fn == pvcFunctions[2] {
				name = fn
				typ = "VOID"
				expected = [][]any{{""}}
			}
			for _, arg := range []string{"'pv_part.heap'", "'pv_part.heap'::regclass", "'pv_part.heap'::text", "'pv_part.heap'::varchar"} {
				pvBoth(t, p, b, c, pvMetadata(fixture.Select("SELECT * FROM pv_part."+fn+"("+arg+")", []string{name}, []string{typ}, expected), b.PG))
			}
			for _, cast := range []string{"oid", "integer", "bigint"} {
				pvBoth(t, p, b, c, fixture.Select("SELECT * FROM pv_part."+fn+"($1::"+cast+")", []string{name}, []string{typ}, expected, oid))
			}
			q := "SELECT * FROM pv_part." + fn + "($1::regclass)"
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
			for _, arg := range []any{nil, "pv_part.heap", nil} {
				rows := expected
				if typ == "VOID" && arg == nil {
					rows = [][]any{{nil}}
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, pvMetadata(fixture.Select(q, []string{name}, []string{typ}, rows, arg), b.PG))
			}
			pvRun(t, p, b, c, fixture.Exec("CREATE SEQUENCE pv_part.once_"+fn, 0))
			q = "SELECT * FROM pv_part." + fn + "(CASE WHEN nextval('pv_part.once_" + fn + "')>0 THEN 'pv_part.heap'::regclass ELSE NULL END)"
			pvBoth(t, p, b, c, fixture.Select(q, []string{name}, []string{typ}, expected))
			pvRun(t, p, b, c, fixture.Select("SELECT currval('pv_part.once_"+fn+"') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
			for _, bad := range []struct{ args, sig string }{{"", ""}, {"true", "boolean"}, {"'pv_part.heap'::regclass,0", "regclass, integer"}, {"relation => 'pv_part.heap'::regclass", "relation => regclass"}, {"'pv_part.heap'::name", "name"}} {
				q = "SELECT * FROM pv_part." + fn + "(" + bad.args + ")"
				s := pvError(q, "42883", "function pv_part."+fn+"("+bad.sig+") does not exist")
				s.Diagnostic.Position = "15"
				s.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				pvRun(t, p, b, c, s)
				st, err := c.PrepareContext(p.Ctx, q)
				if st != nil {
					st.Close()
				}
				fixture.AssertDiagnostic(t, err, s.Diagnostic)
			}
		}
		for _, fn := range pvcFunctions[:2] {
			q := "SELECT 1 WHERE pv_part." + fn + "('pv_part.heap') IS NULL"
			s := pvError(q, "0A000", "set-returning functions are not allowed in WHERE")
			s.Diagnostic.Position = "16"
			pvRun(t, p, b, c, s)
		}
	})
}

func TestReferencePgvisibilityCheckHealthy(t *testing.T) {
	pvcCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pvcChecks(t, p, b, c, "pv_part.heap", nil)
		pvRun(t, p, b, c, fixture.Exec("INSERT INTO pv_part.heap VALUES(1)", 1))
		pvcChecks(t, p, b, c, "pv_part.heap", nil)
		pvFreeze(t, p, b, c)
		pvOneFrozen(t, p, b, c)
		pvcChecks(t, p, b, c, "pv_part.heap", nil)
		peer := pvConn(t, p, b)
		defer peer.Close()
		defer pvRollback(peer)
		for _, q := range []string{"BEGIN", "INSERT INTO pv_part.heap VALUES(2)"} {
			n := int64(0)
			if q != "BEGIN" {
				n = 1
			}
			pvRun(t, p, b, peer, fixture.Exec(q, n))
		}
		pvcChecks(t, p, b, peer, "pv_part.heap", nil)
		pvcChecks(t, p, b, c, "pv_part.heap", nil)
		pvBoth(t, p, b, c, fixture.Select("SELECT k FROM pv_part.heap ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		pvRun(t, p, b, peer, fixture.Exec("ROLLBACK", 0))
		for _, q := range []string{"BEGIN", "UPDATE pv_part.heap SET k=3 WHERE k=1"} {
			n := int64(0)
			if q != "BEGIN" {
				n = 1
			}
			pvRun(t, p, b, c, fixture.Exec(q, n))
		}
		pvcChecks(t, p, b, c, "pv_part.heap", nil)
		pvcChecks(t, p, b, peer, "pv_part.heap", nil)
		pvRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
		for _, q := range []string{"CREATE UNLOGGED TABLE pv_part.ul(k integer)", "CREATE TEMP TABLE pvc_temp(k integer)", "CREATE MATERIALIZED VIEW pv_part.mv AS SELECT k FROM pv_part.heap WITH NO DATA", "CREATE TABLE pv_part.parent(k integer) PARTITION BY RANGE(k)", "CREATE TABLE pv_part.leaf PARTITION OF pv_part.parent DEFAULT"} {
			pvRun(t, p, b, c, fixture.Exec(q, 0))
		}
		for _, target := range []string{"pv_part.ul", "pvc_temp", "pv_part.mv", "pv_part.leaf"} {
			pvcChecks(t, p, b, c, target, nil)
			pvcTruncate(t, p, b, c, target)
			pvcChecks(t, p, b, c, target, nil)
		}
	})
}
