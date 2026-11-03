package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgsurgeryKillMetadata(t *testing.T) {
	t.Run("cohort_acl_and_move", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ks_meta_probe"})
		ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			q := "SELECT p.proname::text AS name,p.pronargs::integer AS nargs,p.proargtypes::text AS args,p.prorettype::bigint AS result,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.proretset AS sets,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosecdef AS definer,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='ks_part' ORDER BY p.proname"
			freezeBoth(t, p, b, c, fixture.Select(q,
				[]string{"name", "nargs", "args", "result", "allargs", "modes", "names", "sets", "strict", "volatility", "parallel", "definer", "source", "binary", "language"},
				[]string{"TEXT", "INT4", "TEXT", "INT8", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT"},
				[][]any{
					{"heap_force_freeze", int64(2), "2205 1010", int64(2278), nil, nil, "{reloid,tids}", false, true, "v", "u", false, "heap_force_freeze", "$libdir/pg_surgery", "c"},
					{"heap_force_kill", int64(2), "2205 1010", int64(2278), nil, nil, "{reloid,tids}", false, true, "v", "u", false, "heap_force_kill", "$libdir/pg_surgery", "c"},
				}))
			freezeBoth(t, p, b, c, fixture.Select("SELECT extversion,extrelocatable,n.nspname::text AS schema FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE extname='pg_surgery'", []string{"extversion", "extrelocatable", "schema"}, []string{"TEXT", "BOOL", "TEXT"}, [][]any{{"1.0", true, "ks_part"}}))
			freezeBoth(t, p, b, c, fixture.Select("SELECT p.proname::text AS name,d.deptype::text AS dependency FROM pg_depend d JOIN pg_proc p ON p.oid=d.objid WHERE d.classid='pg_proc'::regclass AND d.refclassid='pg_extension'::regclass AND d.refobjid=(SELECT oid FROM pg_extension WHERE extname='pg_surgery') ORDER BY p.proname", []string{"name", "dependency"}, []string{"TEXT", "TEXT"}, [][]any{{"heap_force_freeze", "e"}, {"heap_force_kill", "e"}}))
			initial := "SELECT p.proname::text AS name,i.privtype::text AS kind,i.initprivs::text=p.proacl::text AS matches FROM pg_init_privs i JOIN pg_proc p ON p.oid=i.objoid JOIN pg_namespace n ON n.oid=p.pronamespace WHERE i.classoid='pg_proc'::regclass AND i.objsubid=0 AND n.nspname='ks_part' ORDER BY p.proname"
			freezeBoth(t, p, b, c, fixture.Select(initial, []string{"name", "kind", "matches"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{"heap_force_freeze", "e", true}, {"heap_force_kill", "e", true}}))
			freezeRun(t, p, b, c, fixture.Exec("CREATE ROLE ks_meta_probe", 0))
			acl := "SELECT has_function_privilege('ks_meta_probe','ks_part.heap_force_freeze(regclass,tid[])','EXECUTE') AS freeze,has_function_privilege('ks_meta_probe','ks_part.heap_force_kill(regclass,tid[])','EXECUTE') AS kill"
			freezeBoth(t, p, b, c, fixture.Select(acl, []string{"freeze", "kill"}, []string{"BOOL", "BOOL"}, [][]any{{false, false}}))
			freezeRun(t, p, b, c, fixture.Exec("GRANT EXECUTE ON FUNCTION ks_part.heap_force_kill(regclass,tid[]) TO ks_meta_probe", 0))
			freezeBoth(t, p, b, c, fixture.Select(acl, []string{"freeze", "kill"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}))
			freezeBoth(t, p, b, c, fixture.Select(initial, []string{"name", "kind", "matches"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{"heap_force_freeze", "e", true}, {"heap_force_kill", "e", false}}))
			for _, q := range []string{"CREATE SCHEMA ks_moved", "BEGIN", "ALTER EXTENSION pg_surgery SET SCHEMA ks_moved", "ROLLBACK"} {
				freezeRun(t, p, b, c, fixture.Exec(q, 0))
			}
			freezeBoth(t, p, b, c, ksVoid(killSQL, "ks_part.heap", "{}"))
			freezeRun(t, p, b, c, fixture.Exec("ALTER EXTENSION pg_surgery SET SCHEMA ks_moved", 0))
			freezeBoth(t, p, b, c, fixture.Select("SELECT p.proname::text AS name FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='ks_moved' ORDER BY p.proname", []string{"name"}, []string{"TEXT"}, [][]any{{"heap_force_freeze"}, {"heap_force_kill"}}))
			freezeBoth(t, p, b, c, ksVoid("SELECT ks_moved.heap_force_kill($1::regclass,$2::tid[]) AS killed", "ks_part.heap", "{}"))
			for _, q := range []string{"DROP EXTENSION pg_surgery", "BEGIN", "CREATE EXTENSION pg_surgery SCHEMA ks_part", "ROLLBACK"} {
				freezeRun(t, p, b, c, fixture.Exec(q, 0))
			}
			ksNoInstalledMembers(t, p, b, c)
		})
	})
	for _, member := range []struct {
		name string
		line int
	}{{"heap_force_kill", 6}, {"heap_force_freeze", 13}} {
		t.Run("collision_"+member.name, func(t *testing.T) {
			ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				freezeRun(t, p, b, c, fixture.Exec("DROP EXTENSION pg_surgery", 0))
				freezeRun(t, p, b, c, fixture.Exec("CREATE FUNCTION ks_part."+member.name+"(regclass,tid[]) RETURNS void LANGUAGE SQL AS 'SELECT NULL::void'", 0))
				var original int64
				if err := c.QueryRowContext(p.Ctx, "SELECT 'ks_part."+member.name+"(regclass,tid[])'::regprocedure::oid::bigint").Scan(&original); err != nil || original <= 0 {
					t.Fatal("loose function identity", original, err)
				}
				q := "CREATE EXTENSION pg_surgery SCHEMA ks_part"
				s := ksError(q, "42723", "function \""+member.name+"\" already exists with same argument types")
				s.SelectRows = false
				// REL_18_6 installation script, including full extension Context.
				s.Diagnostic.Context = fmt.Sprintf("SQL statement \"CREATE FUNCTION %s(reloid regclass, tids tid[])\nRETURNS VOID\nAS '$libdir/pg_surgery', '%s'\nLANGUAGE C STRICT\"\nextension script file \"pg_surgery--1.0.sql\", near line %d", member.name, member.name, member.line)
				freezeBoth(t, p, b, c, s)
				freezeBoth(t, p, b, c, fixture.Select("SELECT extname FROM pg_extension WHERE extname='pg_surgery'", []string{"extname"}, []string{"NAME"}, nil))
				freezeBoth(t, p, b, c, fixture.Select("SELECT p.proname::text AS name FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='ks_part' ORDER BY p.proname", []string{"name"}, []string{"TEXT"}, [][]any{{member.name}}))
				ksIdentityOID(t, p, b, c, "ks_part."+member.name, original)
				freezeBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_init_privs WHERE classoid='pg_proc'::regclass AND objoid=$1::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, original))
				freezeBoth(t, p, b, c, fixture.Select("SELECT ks_part."+member.name+"('ks_part.heap'::regclass,'{}'::tid[]) AS killed", []string{"killed"}, []string{"VOID"}, [][]any{{nil}}))
			})
		})
	}
}

func ksNoInstalledMembers(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	freezeBoth(t, p, b, c, fixture.Select("SELECT extname FROM pg_extension WHERE extname='pg_surgery'", []string{"extname"}, []string{"NAME"}, nil))
	freezeBoth(t, p, b, c, fixture.Select("SELECT p.proname::text AS name FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='ks_part' AND p.proname IN ('heap_force_freeze','heap_force_kill')", []string{"name"}, []string{"TEXT"}, nil))
}
