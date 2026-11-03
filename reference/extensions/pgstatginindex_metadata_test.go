package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstatginindexMetadata(t *testing.T) {
	pgstatginindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pgstatginindexBuild(t, p, b, c)
		q := "SELECT p.proname::text AS name,p.pronargs::integer AS nargs,p.proargtypes::text AS args,p.prorettype::bigint AS result,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.proretset AS sets,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='gg_part' AND p.proname='pgstatginindex'"
		pgstatginindexBoth(t, p, b, c, fixture.Select(q,
			[]string{"name", "nargs", "args", "result", "allargs", "modes", "names", "sets", "strict", "volatility", "parallel", "source", "binary", "language"},
			[]string{"TEXT", "INT4", "TEXT", "INT8", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"},
			[][]any{{"pgstatginindex", int64(1), "2205", int64(2249), "{2205,23,23,20}", "{i,o,o,o}", "{relname,version,pending_pages,pending_tuples}", false, true, "v", "s", "pgstatginindex_v1_5", "$libdir/pgstattuple", "c"}}))
		q = "SELECT count(*) AS n FROM pg_depend d JOIN pg_proc p ON p.oid=d.objid JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1255 AND d.refclassid=3079 AND d.deptype='e' AND e.extname='pgstattuple'"
		pgstatginindexBoth(t, p, b, c, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(9)}}))
		q = "SELECT EXISTS(SELECT 1 FROM aclexplode(p.proacl) a WHERE a.grantee=0 AND a.privilege_type='EXECUTE') AS public_exec,has_function_privilege('pg_stat_scan_tables',p.oid,'EXECUTE') AS scan_exec,i.privtype::text AS kind,i.initprivs=p.proacl AS initial_acl FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_init_privs i ON i.objoid=p.oid AND i.classoid=1255 AND i.objsubid=0 WHERE n.nspname='gg_part' AND p.proname='pgstatginindex'"
		pgstatginindexBoth(t, p, b, c, fixture.Select(q, []string{"public_exec", "scan_exec", "kind", "initial_acl"}, []string{"BOOL", "BOOL", "TEXT", "BOOL"}, [][]any{{false, true, "e", true}}))
		pgstatginindexBoth(t, p, b, c, pgstatginindexStats("SELECT * FROM gg_part.pgstatginindex('gg_part.gg_array'::regclass)"))
		pgstatginindexBoth(t, p, b, c, fixture.Select("SELECT gg_part.pgstatginindex('gg_part.gg_array') AS s", []string{"s"}, []string{"RECORD"}, [][]any{{"(2,0,0)"}}))
		for _, arg := range []string{"NULL", "NULL::regclass", "$1::regclass"} {
			var args []any
			if arg == "$1::regclass" {
				args = []any{nil}
			}
			pgstatginindexBoth(t, p, b, c, pgstatginindexNull("SELECT * FROM gg_part.pgstatginindex("+arg+")", args...))
			pgstatginindexBoth(t, p, b, c, fixture.Select("SELECT gg_part.pgstatginindex("+arg+") AS s", []string{"s"}, []string{"RECORD"}, [][]any{{nil}}, args...))
		}
		pgstatginindexBoth(t, p, b, c, fixture.Select("SELECT (gg_part.pgstatginindex(relname => $1::regclass)).pending_tuples AS n,(gg_part.pgstatginindex($1::regclass)).version AS v", []string{"n", "v"}, []string{"INT8", "INT4"}, [][]any{{int64(0), int64(2)}}, "gg_part.gg_vector"))
		pgstatginindexBoth(t, p, b, c, fixture.Select("SELECT s.v,s.p,s.t FROM gg_part.pgstatginindex('gg_part.gg_array') AS s(v,p,t)", []string{"v", "p", "t"}, pgstatginindexTypes, [][]any{{int64(2), int64(0), int64(0)}}))
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE gg_part.gg_inputs(id integer,n text)", 0),
			fixture.Exec("INSERT INTO gg_part.gg_inputs VALUES(1,'gg_part.gg_array'),(2,NULL),(3,'gg_part.gg_vector')", 3),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		q = "SELECT i.id,s.pending_tuples FROM gg_part.gg_inputs i CROSS JOIN LATERAL gg_part.pgstatginindex(i.n::regclass) s ORDER BY i.id"
		pgstatginindexBoth(t, p, b, c, fixture.Select(q, []string{"id", "pending_tuples"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(0)}, {int64(2), nil}, {int64(3), int64(0)}}))
		pgstatginindexBoth(t, p, b, c, fixture.Select("SELECT s.pending_tuples FROM (SELECT n FROM gg_part.gg_inputs WHERE false) i CROSS JOIN LATERAL gg_part.pgstatginindex(i.n::regclass) s", []string{"pending_tuples"}, []string{"INT8"}, nil))
	})
}

func TestReferencePgstatginindexBinding(t *testing.T) {
	t.Run("accepted", func(t *testing.T) {
		pgstatginindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			pgstatginindexSeed(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN gg_part.gg_oid AS regclass", 0))
			for _, arg := range []string{"'gg_part.gg_array'", "'gg_part.gg_array'::text::regclass", "'gg_part.gg_array'::regclass", "'gg_part.gg_array'::gg_part.gg_oid", "relname => 'gg_part.gg_array'"} {
				pgstatginindexBoth(t, p, b, c, pgstatginindexStats("SELECT * FROM gg_part.pgstatginindex("+arg+")"))
			}
			var oid int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'gg_part.gg_array'::regclass::oid").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			for _, typ := range []string{"oid", "integer", "bigint"} {
				pgstatginindexBoth(t, p, b, c, pgstatginindexStats("SELECT * FROM gg_part.pgstatginindex($1::"+typ+")", oid))
			}
			q := "SELECT * FROM gg_part.pgstatginindex($1)"
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
			for _, arg := range []any{"gg_part.gg_array", nil, "gg_part.gg_vector", "gg_part.gg_heap_pkey", "gg_part.gg_array"} {
				s := pgstatginindexStats(q, arg)
				if arg == nil {
					s = pgstatginindexNull(q, arg)
				}
				if arg == "gg_part.gg_heap_pkey" {
					s = pgstatindexError(q, "42809", "relation \"gg_heap_pkey\" is not a GIN index", arg)
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			pgstatginindexContents(t, p, b, c, []int64{1, 2}, []int64{1})
			for _, q := range []string{"SET search_path=gg_part,pg_catalog", "CREATE FUNCTION public.pgstatginindex(regclass) RETURNS integer LANGUAGE SQL AS 'SELECT 77'", "SET search_path=public,gg_part,pg_catalog"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			pgstatginindexBoth(t, p, b, c, fixture.Select("SELECT pgstatginindex(NULL::regclass) AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(77)}}))
			pgstatginindexBoth(t, p, b, c, pgstatginindexStats("SELECT * FROM gg_part.pgstatginindex('gg_part.gg_array')"))
		})
	})
	for _, tc := range []struct{ name, arg, signature string }{
		{"boolean", "true", "gg_part.pgstatginindex(boolean)"},
		{"zero_arguments", "", "gg_part.pgstatginindex()"},
		{"two_arguments", "NULL::regclass,NULL::regclass", "gg_part.pgstatginindex(regclass, regclass)"},
		{"wrong_argument_name", "reloid => NULL::regclass", "gg_part.pgstatginindex(reloid => regclass)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pgstatginindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				pgstatginindexMissing(t, p, b, c, "SELECT * FROM gg_part.pgstatginindex("+tc.arg+")", tc.signature)
			})
		})
	}
	t.Run("absent_before_install", func(t *testing.T) {
		pgstatginindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP EXTENSION pgstattuple", 0))
			pgstatginindexMissing(t, p, b, c, "SELECT * FROM gg_part.pgstatginindex(NULL::regclass)", "gg_part.pgstatginindex(regclass)")
		})
	})
}

func pgstatginindexMissing(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, signature string) {
	t.Helper()
	d := fixture.ControlsReferenceDiagnostic("42883", "function "+signature+" does not exist")
	d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	d.Position = "15"
	s := fixture.ImplicitError(q, d)
	s.SelectRows = true
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	st, err := c.PrepareContext(p.Ctx, q)
	if st != nil {
		st.Close()
		t.Fatal("invalid signature prepared successfully")
	}
	fixture.AssertDiagnostic(t, err, &d)
}
