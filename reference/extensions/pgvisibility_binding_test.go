package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgvisibilityMetadata(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pv_default"})
	pvCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT p.proname::text AS name,p.pronargs::integer AS nargs,p.proargtypes::text AS args,p.prorettype::bigint AS result,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.proretset AS sets,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosecdef AS definer,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='pv_part' AND p.proname IN ('pg_visibility','pg_visibility_map','pg_visibility_map_summary') ORDER BY p.proname,p.proargtypes::text"
		pvBoth(t, p, b, c, fixture.Select(q,
			[]string{"name", "nargs", "args", "result", "allargs", "modes", "names", "sets", "strict", "volatility", "parallel", "definer", "source", "binary", "language"},
			[]string{"TEXT", "INT4", "TEXT", "INT8", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT"},
			[][]any{
				{"pg_visibility", int64(1), "2205", int64(2249), "{2205,20,16,16,16}", "{i,o,o,o,o}", "{\"\",blkno,all_visible,all_frozen,pd_all_visible}", true, true, "v", "u", false, "pg_visibility_rel", "$libdir/pg_visibility", "c"},
				{"pg_visibility", int64(2), "2205 20", int64(2249), "{2205,20,16,16,16}", "{i,i,o,o,o}", "{\"\",blkno,all_visible,all_frozen,pd_all_visible}", false, true, "v", "u", false, "pg_visibility", "$libdir/pg_visibility", "c"},
				{"pg_visibility_map", int64(1), "2205", int64(2249), "{2205,20,16,16}", "{i,o,o,o}", "{\"\",blkno,all_visible,all_frozen}", true, true, "v", "u", false, "pg_visibility_map_rel", "$libdir/pg_visibility", "c"},
				{"pg_visibility_map", int64(2), "2205 20", int64(2249), "{2205,20,16,16}", "{i,i,o,o}", "{\"\",blkno,all_visible,all_frozen}", false, true, "v", "u", false, "pg_visibility_map", "$libdir/pg_visibility", "c"},
				{"pg_visibility_map_summary", int64(1), "2205", int64(2249), "{2205,20,20}", "{i,o,o}", "{\"\",all_visible,all_frozen}", false, true, "v", "u", false, "pg_visibility_map_summary", "$libdir/pg_visibility", "c"},
			}))
		pvBoth(t, p, b, c, fixture.Select("SELECT extversion,extrelocatable,obj_description(oid,'pg_extension') AS description FROM pg_extension WHERE extname='pg_visibility'", []string{"extversion", "extrelocatable", "description"}, []string{"TEXT", "BOOL", "TEXT"}, [][]any{{"1.2", true, "examine the visibility map (VM) and page-level visibility info"}}))
		pvBoth(t,p,b,c,fixture.Select("SELECT version,superuser,trusted,relocatable FROM pg_available_extension_versions WHERE name='pg_visibility' AND version='1.2'",[]string{"version","superuser","trusted","relocatable"},[]string{"TEXT","BOOL","BOOL","BOOL"},[][]any{{"1.2",true,false,true}}))
		pvBoth(t,p,b,c,fixture.Select("SELECT name::text AS name,default_version FROM pg_available_extensions WHERE name IN ('pg_visibility','pgvisibility') ORDER BY name",[]string{"name","default_version"},[]string{"TEXT","TEXT"},[][]any{{"pg_visibility","1.2"}}))
		// The oracle owns eight members; native deliberately owns only five. The
		// selected five each require real membership and initial-ACL records.
		q = "SELECT count(*) AS n FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_depend d ON d.objid=p.oid AND d.classid='pg_proc'::regclass AND d.deptype='e' JOIN pg_extension e ON e.oid=d.refobjid JOIN pg_init_privs i ON i.objoid=p.oid AND i.classoid='pg_proc'::regclass AND i.objsubid=0 AND i.privtype='e' WHERE n.nspname='pv_part' AND e.extname='pg_visibility' AND p.proname IN ('pg_visibility','pg_visibility_map','pg_visibility_map_summary') AND i.initprivs=p.proacl AND has_function_privilege('pg_stat_scan_tables',p.oid,'EXECUTE') AND has_function_privilege('pg_monitor',p.oid,'EXECUTE')"
		pvBoth(t, p, b, c, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}))
		if !b.PG {
			pvBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname='pg_visibility' AND d.classid='pg_proc'::regclass AND d.deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}))
		}
		for _, q := range []string{"CREATE ROLE pv_default", "DROP EXTENSION pg_visibility", "ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO pv_default", "CREATE EXTENSION pg_visibility SCHEMA pv_part"} { pvRun(t, p, b, c, fixture.Exec(q, 0)) }
		defer c.ExecContext(p.Ctx, "ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM pv_default")
		pvBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='pv_part' AND p.proname IN ('pg_visibility','pg_visibility_map','pg_visibility_map_summary') AND has_function_privilege('pv_default',p.oid,'EXECUTE')", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}))
		for _, s := range []fixture.Step{
			fixture.Select("SELECT * FROM pv_part.pg_visibility_map(NULL::regclass)", pvMapRowsNames, pvMapRowsTypes, nil),
			fixture.Select("SELECT * FROM pv_part.pg_visibility(NULL::regclass)", pvPageRowsNames, pvPageRowsTypes, nil),
			fixture.Select("SELECT * FROM pv_part.pg_visibility_map(NULL::regclass,0)", pvMapNames, pvMapTypes, [][]any{{nil, nil}}),
			fixture.Select("SELECT * FROM pv_part.pg_visibility(NULL::regclass,0)", pvPageNames, pvPageTypes, [][]any{{nil, nil, nil}}),
			fixture.Select("SELECT * FROM pv_part.pg_visibility_map_summary(NULL::regclass)", pvMapNames, pvSummaryTypes, [][]any{{nil, nil}}),
			fixture.Select("SELECT pv_part.pg_visibility_map(NULL::regclass,0) AS result", []string{"result"}, []string{"RECORD"}, [][]any{{nil}}),
		} {
			pvBoth(t, p, b, c, pvMetadata(s, b.PG))
			s.Query += " WHERE false"; s.Rows = nil
			pvBoth(t, p, b, c, pvMetadata(s, b.PG))
		}
	})
}

func TestReferencePgvisibilityBinding(t *testing.T) {
	pvCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, arg := range []string{"'pv_part.heap'", "'pv_part.heap'::regclass", "'pv_part.heap'::text", "'pv_part.heap'::varchar", "'pv_part.heap'::name::regclass"} {
			for _, block := range []string{"0", "0::smallint", "0::bigint", "blkno => 0"} {
				pvBoth(t, p, b, c, fixture.Select("SELECT * FROM pv_part.pg_visibility_map("+arg+","+block+")", pvMapNames, pvMapTypes, [][]any{{false, false}}))
			}
		}
		oid := pvOID(t, p, c, "pv_part.heap")
		for _, typ := range []string{"oid", "integer", "bigint"} {
			pvBoth(t, p, b, c, fixture.Select("SELECT * FROM pv_part.pg_visibility($1::"+typ+",0)", pvPageNames, pvPageTypes, [][]any{{false, false, false}}, oid))
		}
		pvBoth(t, p, b, c, fixture.Select("SELECT pv_part.pg_visibility_map('pv_part.heap',0) AS result", []string{"result"}, []string{"RECORD"}, [][]any{{"(f,f)"}}))
		pvBoth(t, p, b, c, fixture.Select("SELECT (pv_part.pg_visibility('pv_part.heap',0)).pd_all_visible AS flag", []string{"flag"}, []string{"BOOL"}, [][]any{{false}}))
		pvBoth(t, p, b, c, fixture.Select("SELECT (pv_part.pg_visibility_map_summary('pv_part.heap')).*", pvMapNames, pvSummaryTypes, [][]any{{int64(0), int64(0)}}))
		q := "SELECT * FROM pv_part.pg_visibility($1::regclass,$2::bigint)"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }; defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		for _, s := range []fixture.Step{
			fixture.Select(q, pvPageNames, pvPageTypes, [][]any{{false, false, false}}, "pv_part.heap", int64(0)),
			fixture.Select(q, pvPageNames, pvPageTypes, [][]any{{nil, nil, nil}}, nil, int64(0)),
			fixture.Select(q, pvPageNames, pvPageTypes, [][]any{{nil, nil, nil}}, "pv_part.heap", nil),
		} { pvBoth(t, p, b, c, s); fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
		pvRun(t, p, b, c, fixture.Exec("INSERT INTO pv_part.heap VALUES(1)", 1))
		pvFreeze(t, p, b, c)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, pvPageNames, pvPageTypes, [][]any{{true, true, true}}, "pv_part.heap", int64(0)))
		pvBoth(t, p, b, c, fixture.Select("SELECT i.id,s.blkno,s.all_visible FROM (VALUES(1,'pv_part.heap'::text),(2,NULL::text)) i(id,n) CROSS JOIN LATERAL pv_part.pg_visibility_map(i.n::regclass) s ORDER BY i.id,s.blkno", []string{"id", "blkno", "all_visible"}, []string{"INT4", "INT8", "BOOL"}, [][]any{{int64(1), int64(0), true}}))
		pvBoth(t, p, b, c, fixture.Select("SELECT i.id,s.all_visible FROM (VALUES(1,'pv_part.heap'::text),(2,NULL::text)) i(id,n) CROSS JOIN LATERAL pv_part.pg_visibility_map_summary(i.n::regclass) s ORDER BY i.id", []string{"id", "all_visible"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), nil}}))
		pvRun(t, p, b, c, fixture.Exec("CREATE SEQUENCE pv_part.eval_once", 0))
		// Both arguments share one sequence: the second must observe 2, then 4.
		// Reordering arguments or reevaluating for every OUT column changes it.
		q = "SELECT * FROM pv_part.pg_visibility_map(CASE WHEN nextval('pv_part.eval_once') IN (1,3) THEN 'pv_part.heap'::regclass ELSE NULL END,blkno => CASE WHEN nextval('pv_part.eval_once') IN (2,4) THEN 0::bigint ELSE -1::bigint END)"
		pvBoth(t, p, b, c, fixture.Select(q, pvMapNames, pvMapTypes, [][]any{{true, true}}))
		pvRun(t, p, b, c, fixture.Select("SELECT currval('pv_part.eval_once') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
		for _, spec := range []struct {
			name, suffix, sequence string
			names, types []string
			values [][]any
			set bool
		}{
			{"pg_visibility", ",0", "page_once", pvPageNames, pvPageTypes, [][]any{{true,true,true}}, false},
			{"pg_visibility_map", "", "map_once", pvMapRowsNames, pvMapRowsTypes, [][]any{{int64(0),true,true}}, true},
			{"pg_visibility", "", "rows_once", pvPageRowsNames, pvPageRowsTypes, [][]any{{int64(0),true,true,true}}, true},
			{"pg_visibility_map_summary", "", "summary_once", pvMapNames, pvSummaryTypes, [][]any{{int64(1),int64(1)}}, false},
		} {
			pvRun(t,p,b,c,fixture.Exec("CREATE SEQUENCE pv_part."+spec.sequence,0))
			once := "SELECT * FROM pv_part."+spec.name+"(CASE WHEN nextval('pv_part."+spec.sequence+"')>0 THEN 'pv_part.heap'::regclass ELSE NULL END"+spec.suffix+")"
			if spec.set { once += " ORDER BY blkno" }
			pvBoth(t,p,b,c,fixture.Select(once,spec.names,spec.types,spec.values))
			pvRun(t,p,b,c,fixture.Select("SELECT currval('pv_part."+spec.sequence+"') AS n",[]string{"n"},[]string{"INT8"},[][]any{{int64(2)}}))
			parameterized := "SELECT * FROM pv_part."+spec.name+"($1::regclass"+spec.suffix+")"
			if spec.set { parameterized += " ORDER BY blkno" }
			statement,err:=c.PrepareContext(p.Ctx,parameterized)
			if err!=nil {t.Fatal(err)}
			defer statement.Close()
			h:=fixture.HeldStatement{Executor:c,Statement:statement,SQL:parameterized}
			var nullRows [][]any
			if !spec.set {nullRows=[][]any{make([]any,len(spec.names))}}
			for _, s:=range []fixture.Step{
				fixture.Select(parameterized,spec.names,spec.types,spec.values,"pv_part.heap"),
				fixture.Select(parameterized,spec.names,spec.types,nullRows,nil),
				fixture.Select(parameterized,spec.names,spec.types,spec.values,"pv_part.heap"),
			} {fixture.RunStep(t,p.Ctx,h,b.Recorder,pvMetadata(s,b.PG))}
		}
		for _, tc := range []struct{ call, signature string }{
			{"pg_visibility()", "pv_part.pg_visibility()"},
			{"pg_visibility(true)", "pv_part.pg_visibility(boolean)"},
			{"pg_visibility('pv_part.heap'::regclass,true)", "pv_part.pg_visibility(regclass, boolean)"},
			{"pg_visibility('pv_part.heap'::regclass,0,0)", "pv_part.pg_visibility(regclass, integer, integer)"},
			{"pg_visibility_map(relation => 'pv_part.heap'::regclass)", "pv_part.pg_visibility_map(relation => regclass)"},
			{"pg_visibility_map('pv_part.heap'::regclass,missing => 0)", "pv_part.pg_visibility_map(regclass, missing => integer)"},
			{"pg_visibility_map('pv_part.heap'::name,0)", "pv_part.pg_visibility_map(name, integer)"},
			{"pg_visibility_map_summary(blkno => 'pv_part.heap'::regclass)", "pv_part.pg_visibility_map_summary(blkno => regclass)"},
		} {
			q = "SELECT * FROM pv_part."+tc.call
			s := pvError(q, "42883", "function "+tc.signature+" does not exist")
			s.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			s.Diagnostic.Position = "15"
			pvRun(t, p, b, c, s)
			bad, err := c.PrepareContext(p.Ctx, q)
			if bad != nil { bad.Close() }
			fixture.AssertDiagnostic(t, err, s.Diagnostic)
		}
		q = "SELECT * FROM pv_part.pg_visibility_map('pv_part.heap',blkno => 0,blkno => 1)"
		s := pvError(q, "42601", "argument name \"blkno\" used more than once")
		s.Diagnostic.Position = fixture.Position(strings.LastIndex(q, "blkno") + 1)
		pvRun(t, p, b, c, s)
		bad, err := c.PrepareContext(p.Ctx, q); if bad != nil { bad.Close() }; fixture.AssertDiagnostic(t, err, s.Diagnostic)
	})
}
