package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func bcsInitialPrivilege(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, signature string, changed bool) {
	t.Helper()
	q := "SELECT initprivs=ARRAY[(current_user::text||'=X/'||current_user::text)::aclitem,('pg_monitor=X/'||current_user::text)::aclitem] AS initial FROM pg_init_privs WHERE classoid=1255 AND objoid=$1::regprocedure AND objsubid=0 AND privtype='e'"
	bcBoth(t, p, b, c, fixture.Select(q, []string{"initial"}, []string{"BOOL"}, [][]any{{true}}, signature))
	q = "SELECT i.initprivs<>p.proacl AS changed FROM pg_init_privs i JOIN pg_proc p ON p.oid=i.objoid WHERE i.classoid=1255 AND i.objoid=$1::regprocedure AND i.objsubid=0 AND i.privtype='e'"
	bcBoth(t, p, b, c, fixture.Select(q, []string{"changed"}, []string{"BOOL"}, [][]any{{changed}}, signature))
}

func TestReferencePgBuffercacheSummaryIdentity(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bc_sum_identity_owner"})
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT p.proname::text AS name,p.pronargs::integer AS nargs,p.proargtypes::text AS args,p.prorettype::bigint AS result,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.proretset AS sets,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosecdef AS definer,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language,pg_get_userbyid(p.proowner)=current_user AS owner,p.procost::double precision AS cost,p.prorows::double precision AS rows FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='bc_part' AND p.proname IN ('pg_buffercache_summary','pg_buffercache_usage_counts') ORDER BY p.proname"
		bcBoth(t, p, b, c, fixture.Select(q,
			[]string{"name", "nargs", "args", "result", "allargs", "modes", "names", "sets", "strict", "volatility", "parallel", "definer", "source", "binary", "language", "owner", "cost", "rows"},
			[]string{"TEXT", "INT4", "TEXT", "INT8", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT", "BOOL", "FLOAT8", "FLOAT8"},
			[][]any{
				{"pg_buffercache_summary", int64(0), "", int64(2249), "{23,23,23,23,701}", "{o,o,o,o,o}", "{buffers_used,buffers_unused,buffers_dirty,buffers_pinned,usagecount_avg}", false, false, "v", "s", false, "pg_buffercache_summary", "$libdir/pg_buffercache", "c", true, float64(1), float64(0)},
				{"pg_buffercache_usage_counts", int64(0), "", int64(2249), "{23,23,23,23}", "{o,o,o,o}", "{usage_count,buffers,dirty,pinned}", true, false, "v", "s", false, "pg_buffercache_usage_counts", "$libdir/pg_buffercache", "c", true, float64(1), float64(1000)},
			}))
		bcDDL(t, p, b, c, "CREATE ROLE bc_sum_identity_owner", "CREATE SCHEMA bc_moved", "GRANT USAGE ON SCHEMA bc_part,bc_moved TO bc_sum_identity_owner")
		for i, name := range bcsRoutines {
			t.Run(name, func(t *testing.T) {
				signature := "bc_part." + name + "()"
				var original int64
				if err := c.QueryRowContext(p.Ctx, "SELECT $1::regprocedure::oid::bigint", signature).Scan(&original); err != nil || original <= 0 {
					t.Fatal("resident routine OID", original, err)
				}
				bcsInitialPrivilege(t, p, b, c, signature, false)
				dependency := "SELECT count(*) AS n FROM pg_depend WHERE classid=1255 AND objid=$1::oid AND objsubid=0 AND refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='pg_buffercache') AND refobjsubid=0 AND deptype='e'"
				bcBoth(t, p, b, c, fixture.Select(dependency, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, original))
				wrapper := "bc_part.sum_identity_" + name
				bcDDL(t, p, b, c, "CREATE VIEW "+wrapper+" AS SELECT * FROM "+signature)
				count := int64(1)
				if i == 1 {
					count = 6
				}
				check := func() {
					t.Helper()
					bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM "+wrapper, []string{"n"}, []string{"INT8"}, [][]any{{count}}))
				}
				check()
				kept := name + "_kept"
				bcDDL(t, p, b, c, "ALTER FUNCTION "+signature+" OWNER TO bc_sum_identity_owner", "ALTER FUNCTION "+signature+" RENAME TO "+kept, "ALTER FUNCTION bc_part."+kept+"() SET SCHEMA bc_moved")
				moved := "bc_moved." + kept + "()"
				check()
				bcBoth(t, p, b, c, fixture.Select("SELECT $1::regprocedure::oid::bigint AS oid,pg_get_userbyid(proowner) AS owner FROM pg_proc WHERE oid=$1::regprocedure", []string{"oid", "owner"}, []string{"INT8", "NAME"}, [][]any{{original, "bc_sum_identity_owner"}}, moved))
				bcBoth(t, p, b, c, fixture.Select("SELECT pg_get_viewdef($1::regclass) LIKE $2::text AS renamed", []string{"renamed"}, []string{"BOOL"}, [][]any{{true}}, wrapper, "%"+kept+"%"))
				bcDDL(t, p, b, c, "ALTER EXTENSION pg_buffercache DROP FUNCTION "+moved)
				bcBoth(t, p, b, c, fixture.Select(dependency, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, original))
				check()
				bcDDL(t, p, b, c, "CREATE FUNCTION bc_part."+name+"() RETURNS integer LANGUAGE SQL AS 'SELECT 77'", "SET search_path=bc_part,pg_catalog")
				bcBoth(t, p, b, c, fixture.Select("SELECT "+name+"() AS sentinel", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(77)}}))
				check()
				bcDDL(t, p, b, c, "RESET search_path", "SET ROLE bc_sum_identity_owner")
				bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM "+moved, []string{"n"}, []string{"INT8"}, [][]any{{count}}))
				bcDDL(t, p, b, c, "RESET ROLE", "ALTER EXTENSION pg_buffercache ADD FUNCTION "+moved)
				bcBoth(t, p, b, c, fixture.Select(dependency, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, original))
			})
		}
	})
}
