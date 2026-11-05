package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func bcCatalogIdentity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	bcBoth(t, p, b, c, fixture.Select("SELECT extversion,extrelocatable,pg_get_userbyid(extowner)=current_user AS owner FROM pg_extension WHERE extname='pg_buffercache'", []string{"extversion", "extrelocatable", "owner"}, []string{"TEXT", "BOOL", "BOOL"}, [][]any{{"1.6", true, true}}))
	bcBoth(t, p, b, c, fixture.Select("SELECT version,superuser,trusted,relocatable FROM pg_available_extension_versions WHERE name='pg_buffercache' AND version='1.6'", []string{"version", "superuser", "trusted", "relocatable"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"1.6", true, false, true}}))
	q := "SELECT p.pronargs::integer AS nargs,p.prorettype::bigint AS result,p.proretset,p.provolatile::text AS volatility,p.proisstrict,p.proparallel::text AS parallel,p.prosecdef,p.proargnames::text AS names,p.proallargtypes::text AS outputs,l.lanname::text AS language,p.prosrc,p.probin,pg_get_userbyid(p.proowner)=current_user AS owner FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.oid='bc_part.pg_buffercache_pages()'::regprocedure"
	bcBoth(t, p, b, c, fixture.Select(q, []string{"nargs", "result", "proretset", "volatility", "proisstrict", "parallel", "prosecdef", "names", "outputs", "language", "prosrc", "probin", "owner"}, []string{"INT4", "INT8", "BOOL", "TEXT", "BOOL", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{int64(0), int64(2249), true, "v", false, "s", false, nil, nil, "c", "pg_buffercache_pages", "$libdir/pg_buffercache", true}}))
	var ext, proc, relation, typ, array int64
	if err := c.QueryRowContext(p.Ctx, "SELECT e.oid::bigint,p.oid::bigint,c.oid::bigint,c.reltype::bigint,t.typarray::bigint FROM pg_extension e CROSS JOIN pg_proc p CROSS JOIN pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE e.extname='pg_buffercache' AND p.oid='bc_part.pg_buffercache_pages()'::regprocedure AND c.oid='bc_part.pg_buffercache'::regclass").Scan(&ext, &proc, &relation, &typ, &array); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{ext, proc, relation, typ, array} {
		if id <= 0 {
			t.Fatal("generated identity absent", ext, proc, relation, typ, array)
		}
	}
	if typ == array {
		t.Fatal("row and array types alias within pg_type", typ)
	}
	bcBoth(t, p, b, c, fixture.Select("SELECT c.relkind::text AS kind,t.typtype::text AS type,t.typrelid::bigint AS relation,a.typelem::bigint AS element FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid=$1::oid", []string{"kind", "type", "relation", "element"}, []string{"TEXT", "TEXT", "INT8", "INT8"}, [][]any{{"v", "c", relation, typ}}, relation))
	for _, member := range []struct{ class, object int64 }{{1255, proc}, {1259, relation}} {
		bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid=$1::oid AND objid=$2::oid AND objsubid=0 AND refclassid=3079 AND refobjid=$3::oid AND refobjsubid=0 AND deptype='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, member.class, member.object, ext))
	}
	for _, tc := range []struct {
		query  string
		object int64
	}{
		{"SELECT count(*) AS n FROM pg_init_privs i JOIN pg_proc p ON p.oid=i.objoid WHERE i.classoid=1255 AND i.objoid=$1::oid AND i.objsubid=0 AND i.privtype='e' AND i.initprivs=p.proacl", proc},
		{"SELECT count(*) AS n FROM pg_init_privs i JOIN pg_class c ON c.oid=i.objoid WHERE i.classoid=1259 AND i.objoid=$1::oid AND i.objsubid=0 AND i.privtype='e' AND i.initprivs=c.relacl", relation},
	} {
		bcBoth(t, p, b, c, fixture.Select(tc.query, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, tc.object))
	}
}

func TestReferencePgBuffercacheIdentity(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bc_install_user", "bc_object_owner"})
	bcScenario(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		count := func(source fixture.Executor, n int64) {
			t.Helper()
			bcRun(t, p, b, source, fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='pg_buffercache'", []string{"n"}, []string{"INT8"}, [][]any{{n}}))
		}
		absent := func() {
			t.Helper()
			count(c, 0)
			for _, q := range []string{
				"SELECT count(*) AS n FROM pg_proc WHERE pronamespace='bc_part'::regnamespace AND proname IN ('pg_buffercache_pages','pg_buffercache_evict','pg_buffercache_evict_relation','pg_buffercache_evict_all')",
				"SELECT count(*) AS n FROM pg_class WHERE relnamespace='bc_part'::regnamespace AND relname='pg_buffercache'",
				"SELECT count(*) AS n FROM pg_type WHERE typnamespace='bc_part'::regnamespace AND typname IN ('pg_buffercache','_pg_buffercache')",
			} {
				bcBoth(t, p, b, c, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}
		}
		bcDDL(t, p, b, c, "CREATE ROLE bc_install_user", "CREATE ROLE bc_object_owner", "GRANT CREATE,USAGE ON SCHEMA bc_part TO bc_install_user", "SET SESSION AUTHORIZATION bc_install_user")
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		s := bcError("CREATE EXTENSION pg_buffercache SCHEMA bc_part", "42501", "permission denied to create extension \"pg_buffercache\"")
		s.SelectRows = false
		s.Diagnostic.Hint = "Must be superuser to create this extension."
		bcFailure(t, p, b, c, s, "execute")
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION", "BEGIN", "CREATE EXTENSION pg_buffercache SCHEMA bc_part")
		count(c, 1)
		bcDDL(t, p, b, c, "ROLLBACK")
		absent()
		bcDDL(t, p, b, c, "BEGIN", "SAVEPOINT bc_save", "CREATE EXTENSION pg_buffercache SCHEMA bc_part", "ROLLBACK TO bc_save")
		absent()
		bcDDL(t, p, b, c, "COMMIT")
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		bcDDL(t, p, b, c, "BEGIN", "CREATE EXTENSION pg_buffercache SCHEMA bc_part")
		count(c, 1)
		count(peer, 0)
		bcDDL(t, p, b, c, "COMMIT")
		count(peer, 1)
		bcCatalogIdentity(t, p, b, c)
		s = bcError("CREATE EXTENSION pg_buffercache SCHEMA bc_part", "42710", "extension \"pg_buffercache\" already exists")
		s.SelectRows = false
		bcFailure(t, p, b, c, s, "execute")
		var notices []fixture.Diagnostic
		_, nc, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		fixture.RunStep(t, p.Ctx, nc, rec, fixture.Exec("CREATE EXTENSION IF NOT EXISTS pg_buffercache SCHEMA bc_part", 0))
		want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: "extension \"pg_buffercache\" already exists, skipping"}}
		if !reflect.DeepEqual(notices, want) {
			t.Fatal("IF NOT EXISTS notice", notices, want)
		}
		if err := nc.Close(); err != nil {
			t.Fatal(err)
		}

		capacity := bcCapacity(t, p, c)
		var originalProc, originalView, originalType, originalArray int64
		identityQuery := "SELECT p.oid::bigint,c.oid::bigint,c.reltype::bigint,t.typarray::bigint FROM pg_proc p CROSS JOIN pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE p.oid=$1::regprocedure AND c.oid=$2::regclass"
		if err := c.QueryRowContext(p.Ctx, identityQuery, "bc_part.pg_buffercache_pages()", "bc_part.pg_buffercache").Scan(&originalProc, &originalView, &originalType, &originalArray); err != nil {
			t.Fatal(err)
		}
		membership := func(n int64) {
			t.Helper()
			q := "SELECT count(*) AS n FROM pg_depend WHERE refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='pg_buffercache') AND deptype='e' AND ((classid=1255 AND objid=$1::oid) OR (classid=1259 AND objid=$2::oid))"
			bcBoth(t, p, b, c, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{n}}, originalProc, originalView))
		}
		membership(2)
		bcDDL(t, p, b, c, "CREATE SCHEMA bc_moved", "CREATE VIEW bc_part.identity_wrapper AS SELECT * FROM bc_part.pg_buffercache")
		wrapper := func() {
			t.Helper()
			bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM bc_part.identity_wrapper", []string{"n"}, []string{"INT8"}, [][]any{{capacity}}))
		}
		wrapper()
		for _, q := range []string{
			"ALTER FUNCTION bc_part.pg_buffercache_pages() OWNER TO bc_object_owner",
			"ALTER VIEW bc_part.pg_buffercache OWNER TO bc_object_owner",
			"ALTER FUNCTION bc_part.pg_buffercache_pages() RENAME TO pages_kept",
			"ALTER FUNCTION bc_part.pages_kept() SET SCHEMA bc_moved",
			"ALTER VIEW bc_part.pg_buffercache RENAME TO cache_kept",
			"ALTER VIEW bc_part.cache_kept SET SCHEMA bc_moved",
			"ALTER EXTENSION pg_buffercache DROP FUNCTION bc_moved.pages_kept()",
			"ALTER EXTENSION pg_buffercache DROP VIEW bc_moved.cache_kept",
		} {
			bcDDL(t, p, b, c, q)
			wrapper()
		}
		membership(0)
		bcBoth(t, p, b, c, fixture.Select(identityQuery, []string{"oid", "oid", "reltype", "typarray"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{originalProc, originalView, originalType, originalArray}}, "bc_moved.pages_kept()", "bc_moved.cache_kept"))
		bcDDL(t, p, b, c, "CREATE FUNCTION bc_part.pg_buffercache_pages() RETURNS integer LANGUAGE SQL AS 'SELECT 77'")
		bcBoth(t, p, b, c, fixture.Select("SELECT bc_part.pg_buffercache_pages() AS sentinel", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(77)}}))
		moved := bcFrom("bc_moved.pages_kept", bcNames, bcSQLTypes)
		bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM "+moved, []string{"n"}, []string{"INT8"}, [][]any{{capacity}}))
		wrapper()
		bcDDL(t, p, b, c,
			"ALTER EXTENSION pg_buffercache ADD FUNCTION bc_moved.pages_kept()",
			"ALTER EXTENSION pg_buffercache ADD VIEW bc_moved.cache_kept")
		wrapper()
		membership(2)
		bcBoth(t, p, b, c, fixture.Select("SELECT pg_get_userbyid(proowner) AS owner FROM pg_proc WHERE oid='bc_moved.pages_kept()'::regprocedure", []string{"owner"}, []string{"NAME"}, [][]any{{"bc_object_owner"}}))
		bcBoth(t, p, b, c, fixture.Select("SELECT pg_get_userbyid(relowner) AS owner FROM pg_class WHERE oid='bc_moved.cache_kept'::regclass", []string{"owner"}, []string{"NAME"}, [][]any{{"bc_object_owner"}}))
		bcDDL(t, p, b, c, "DROP VIEW bc_part.identity_wrapper", "DROP EXTENSION pg_buffercache CASCADE", "DROP FUNCTION bc_part.pg_buffercache_pages()")
		absent()
		bcInstallConflicts(t, p, b, c)
		bcDDL(t, p, b, c,
			"ALTER DEFAULT PRIVILEGES IN SCHEMA bc_part GRANT EXECUTE ON FUNCTIONS TO bc_install_user",
			"ALTER DEFAULT PRIVILEGES IN SCHEMA bc_part GRANT SELECT ON TABLES TO bc_install_user",
			"CREATE EXTENSION pg_buffercache SCHEMA bc_part")
		bcCatalogIdentity(t, p, b, c)
		bcDDL(t, p, b, c, "SET SESSION AUTHORIZATION bc_install_user")
		bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM bc_part.pg_buffercache", []string{"n"}, []string{"INT8"}, [][]any{{capacity}}))
		bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM "+bcFunction(), []string{"n"}, []string{"INT8"}, [][]any{{capacity}}))
		bcDDL(t, p, b, c, "RESET SESSION AUTHORIZATION",
			"DROP EXTENSION pg_buffercache",
			"ALTER DEFAULT PRIVILEGES IN SCHEMA bc_part REVOKE EXECUTE ON FUNCTIONS FROM bc_install_user",
			"ALTER DEFAULT PRIVILEGES IN SCHEMA bc_part REVOKE SELECT ON TABLES FROM bc_install_user")
	})
}

func bcInstallConflicts(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, view := range []bool{false, true} {
		setup, drop := "CREATE FUNCTION bc_part.pg_buffercache_pages() RETURNS integer LANGUAGE SQL AS 'SELECT 77'", "DROP FUNCTION bc_part.pg_buffercache_pages()"
		code, message := "42723", "function \"pg_buffercache_pages\" already exists with same argument types"
		if view {
			setup, drop = "CREATE VIEW bc_part.pg_buffercache AS SELECT 77 AS sentinel", "DROP VIEW bc_part.pg_buffercache"
			code, message = "42P07", "relation \"pg_buffercache\" already exists"
		}
		bcDDL(t, p, b, c, setup)
		s := bcError("CREATE EXTENSION pg_buffercache SCHEMA bc_part", code, message)
		s.SelectRows = false
		// Exact released script Context is a source-informed checkpoint fixture;
		// root resolves line/whitespace against pinned 18.6 before production.
		if view {
			s.Diagnostic.Context = "SQL statement \"CREATE VIEW pg_buffercache AS\n\tSELECT P.* FROM pg_buffercache_pages() AS P\n\t(bufferid integer, relfilenode oid, reltablespace oid, reldatabase oid,\n\t relforknumber int2, relblocknumber int8, isdirty bool, usagecount int2,\n\t pinning_backends int4)\"\nextension script file \"pg_buffercache--1.2.sql\", near line 13"
		} else {
			s.Diagnostic.Context = "SQL statement \"CREATE FUNCTION pg_buffercache_pages()\nRETURNS SETOF RECORD\nAS '$libdir/pg_buffercache', 'pg_buffercache_pages'\nLANGUAGE C PARALLEL SAFE\"\nextension script file \"pg_buffercache--1.2.sql\", near line 7"
		}
		bcFailure(t, p, b, c, s, "execute")
		bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='pg_buffercache'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if view {
			bcBoth(t, p, b, c, fixture.Select("SELECT sentinel FROM bc_part.pg_buffercache", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(77)}}))
			bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE pronamespace='bc_part'::regnamespace AND proname='pg_buffercache_pages'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		} else {
			bcBoth(t, p, b, c, fixture.Select("SELECT bc_part.pg_buffercache_pages() AS sentinel", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(77)}}))
			bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_type WHERE typnamespace='bc_part'::regnamespace AND typname IN ('pg_buffercache','_pg_buffercache')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		bcDDL(t, p, b, c, drop)
		bcBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_type WHERE typnamespace='bc_part'::regnamespace AND typname IN ('pg_buffercache','_pg_buffercache')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	}
}
