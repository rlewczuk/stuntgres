package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgPrewarmIdentity(t *testing.T) {
	t.Run("authentic_identity", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"pw_identity_owner"})
		pwScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			pwInsert(t, p, b, c, "pw_part.heap", 1)
			bcDDL(t, p, b, c, "CREATE ROLE pw_identity_owner", "CREATE SCHEMA pw_moved", "GRANT USAGE ON SCHEMA pw_part,pw_moved TO pw_identity_owner", "CREATE VIEW pw_part.bound AS SELECT pw_part.pg_prewarm('pw_part.heap'::regclass) AS warmed")
			var routine int64
			if err := c.QueryRowContext(p.Ctx, "SELECT $1::regprocedure::oid::bigint", pwSignature).Scan(&routine); err != nil || routine <= 0 { t.Fatal("real installed identity", routine, err) }
			q := "SELECT count(*) AS n FROM pg_depend WHERE classid='pg_proc'::regclass AND objid=$1::oid AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='pg_prewarm') AND deptype='e'"
			bcBoth(t, p, b, c, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, routine))
			// Default PUBLIC ACL is NULL, so do not fabricate a pg_init_privs
			// row merely because the procedure is an extension member.
			bcBoth(t, p, b, c, fixture.Select("SELECT proacl IS NULL AS ordinary_acl FROM pg_proc WHERE oid=$1::oid", []string{"ordinary_acl"}, []string{"BOOL"}, [][]any{{true}}, routine))
			bound := func() { pwRoutes(t, p, b, c, pwViewCount("SELECT warmed FROM pw_part.bound", 1, b.PG)) }
			bound()
			for _, ddl := range []string{"ALTER TABLE pw_part.heap RENAME TO kept", "CREATE TABLE pw_part.heap(k integer)", "ALTER TABLE pw_part.kept SET SCHEMA pw_moved", "ALTER FUNCTION "+pwSignature+" RENAME TO warmer", "ALTER FUNCTION pw_part.warmer(regclass,text,text,bigint,bigint) SET SCHEMA pw_moved", "ALTER FUNCTION pw_moved.warmer(regclass,text,text,bigint,bigint) OWNER TO pw_identity_owner"} {
				bcDDL(t, p, b, c, ddl)
				bound()
			}
			bcBoth(t, p, b, c, fixture.Select("SELECT 'pw_moved.warmer(regclass,text,text,bigint,bigint)'::regprocedure::oid::bigint AS identity", []string{"identity"}, []string{"INT8"}, [][]any{{routine}}))
			bcDDL(t, p, b, c, "CREATE FUNCTION pw_part.pg_prewarm(regclass,text DEFAULT 'buffer',text DEFAULT 'main',bigint DEFAULT NULL,bigint DEFAULT NULL) RETURNS bigint LANGUAGE SQL AS 'SELECT 77::bigint'")
			pwRoutes(t, p, b, c, pwCount("SELECT pw_part.pg_prewarm(NULL::regclass) AS warmed", 77))
			bound()
			bcDDL(t, p, b, c, "ALTER EXTENSION pg_prewarm DROP FUNCTION pw_moved.warmer(regclass,text,text,bigint,bigint)")
			bound()
			bcBoth(t, p, b, c, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, routine))
			bcDDL(t, p, b, c, "ALTER EXTENSION pg_prewarm ADD FUNCTION pw_moved.warmer(regclass,text,text,bigint,bigint)")
			bcBoth(t, p, b, c, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, routine))
			bcDDL(t, p, b, c, "DROP FUNCTION pw_part.pg_prewarm(regclass,text,text,bigint,bigint)", "ALTER FUNCTION pw_moved.warmer(regclass,text,text,bigint,bigint) RENAME TO pg_prewarm")
			// A relocatable extension still requires all members to begin in
			// its recorded schema. Individual routine moves do not relocate it.
			s := pwError("ALTER EXTENSION pg_prewarm SET SCHEMA pw_moved", "0A000", "extension \"pg_prewarm\" does not support SET SCHEMA")
			s.SelectRows = false
			s.Diagnostic.Detail = "function pw_moved.pg_prewarm(regclass,text,text,bigint,bigint) is not in the extension's schema \"pw_part\""
			bcRun(t, p, b, c, s)
			bound()
			bcBoth(t, p, b, c, fixture.Select("SELECT n.nspname::text AS schema,e.extrelocatable AS relocatable FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='pg_prewarm'", []string{"schema", "relocatable"}, []string{"TEXT", "BOOL"}, [][]any{{"pw_part", true}}))
			bcBoth(t, p, b, c, fixture.Select("SELECT p.oid::bigint AS identity,n.nspname::text AS schema FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.oid=$1::oid", []string{"identity", "schema"}, []string{"INT8", "TEXT"}, [][]any{{routine, "pw_moved"}}, routine))
			bcDDL(t, p, b, c, "ALTER FUNCTION pw_moved.pg_prewarm(regclass,text,text,bigint,bigint) SET SCHEMA pw_part", "ALTER EXTENSION pg_prewarm SET SCHEMA pw_moved")
			bound()
			pwRoutes(t, p, b, c, pwCount("SELECT pw_moved.pg_prewarm('pw_moved.kept') AS warmed", 1))
			bcBoth(t, p, b, c, fixture.Select("SELECT position('pw_moved.pg_prewarm' in pg_get_viewdef('pw_part.bound'::regclass))>0 AS current_name", []string{"current_name"}, []string{"BOOL"}, [][]any{{true}}))
			bcDDL(t, p, b, c, "BEGIN", "SAVEPOINT keep", "DROP EXTENSION pg_prewarm CASCADE", "ROLLBACK TO keep", "COMMIT")
			bound()
		})
	})
	t.Run("atomic_install", func(t *testing.T) {
		pwScenario(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			absent := func() { bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='pg_prewarm'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})) }
			bcDDL(t, p, b, c, "BEGIN", "CREATE EXTENSION pg_prewarm SCHEMA pw_part", "ROLLBACK")
			absent()
			bcDDL(t, p, b, c, "CREATE FUNCTION pw_part.pg_prewarm(regclass,text,text,bigint,bigint) RETURNS bigint LANGUAGE SQL AS 'SELECT 77::bigint'")
			s := pwError("CREATE EXTENSION pg_prewarm SCHEMA pw_part", "42723", "function \"pg_prewarm\" already exists with same argument types")
			s.SelectRows = false
			s.Diagnostic.Context = "SQL statement \"CREATE FUNCTION pg_prewarm(regclass,\n\t\t\t\t\t\t   mode text default 'buffer',\n\t\t\t\t\t\t   fork text default 'main',\n\t\t\t\t\t\t   first_block int8 default null,\n\t\t\t\t\t\t   last_block int8 default null)\nRETURNS int8\nAS '$libdir/pg_prewarm', 'pg_prewarm'\nLANGUAGE C PARALLEL SAFE\"\nextension script file \"pg_prewarm--1.1.sql\", near line 7"
			bcRun(t, p, b, c, s)
			absent()
			pwRoutes(t, p, b, c, pwCount(pwCall, 77, nil, nil, nil, nil, nil))
			bcDDL(t, p, b, c, "DROP FUNCTION "+pwSignature, "CREATE EXTENSION pg_prewarm SCHEMA pw_part", "BEGIN", "SAVEPOINT installed", "DROP EXTENSION pg_prewarm", "ROLLBACK TO installed", "COMMIT")
			pwBodyError(t, p, b, c, pwError("SELECT pw_part.pg_prewarm(NULL::regclass)", "22023", "relation cannot be null"))
			bcDDL(t, p, b, c, "DROP EXTENSION pg_prewarm")
			absent()
		})
	})
	t.Run("default_acl_history", func(t *testing.T) {
		pwScenario(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			bcDDL(t, p, b, c, "ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC")
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO PUBLIC"); err != nil { t.Error(err) }
			}()
			bcDDL(t, p, b, c, "CREATE EXTENSION pg_prewarm SCHEMA pw_part")
			// CREATE honors current default ACLs, but only script GRANT/REVOKE
			// or explicit member adoption records an initial-privilege row.
			q := "SELECT count(p.oid) AS identities,bool_and(p.proacl IS NOT NULL) AS current_acl,count(i.objoid) AS initial_rows FROM pg_proc p LEFT JOIN pg_init_privs i ON i.classoid='pg_proc'::regclass AND i.objoid=p.oid AND i.objsubid=0 AND i.privtype='e' WHERE p.oid=$1::regprocedure"
			bcBoth(t, p, b, c, fixture.Select(q, []string{"identities", "current_acl", "initial_rows"}, []string{"INT8", "BOOL", "INT8"}, [][]any{{int64(1), true, int64(0)}}, pwSignature))
			bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS acl_entries,bool_and(x.grantee=p.proowner AND x.grantor=p.proowner AND x.privilege_type='EXECUTE' AND NOT x.is_grantable) AS owner_only FROM pg_proc p CROSS JOIN LATERAL aclexplode(p.proacl) x WHERE p.oid=$1::regprocedure", []string{"acl_entries", "owner_only"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), true}}, pwSignature))
			bcDDL(t, p, b, c, "ALTER EXTENSION pg_prewarm DROP FUNCTION "+pwSignature, "ALTER EXTENSION pg_prewarm ADD FUNCTION "+pwSignature)
			q = "SELECT count(*) AS identities,bool_and(p.proacl=i.initprivs AND p.proacl IS NOT NULL) AS initial_acl FROM pg_proc p JOIN pg_init_privs i ON i.classoid='pg_proc'::regclass AND i.objoid=p.oid AND i.objsubid=0 AND i.privtype='e' WHERE p.oid=$1::regprocedure"
			bcBoth(t, p, b, c, fixture.Select(q, []string{"identities", "initial_acl"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), true}}, pwSignature))
			bcDDL(t, p, b, c, "GRANT EXECUTE ON FUNCTION "+pwSignature+" TO PUBLIC")
			q = "SELECT count(*) AS identities,bool_and(p.proacl<>i.initprivs) AS independent_history FROM pg_proc p JOIN pg_init_privs i ON i.classoid='pg_proc'::regclass AND i.objoid=p.oid AND i.objsubid=0 AND i.privtype='e' WHERE p.oid=$1::regprocedure"
			bcBoth(t, p, b, c, fixture.Select(q, []string{"identities", "independent_history"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), true}}, pwSignature))
		})
	})
}
