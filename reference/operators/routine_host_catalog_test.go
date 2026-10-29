package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoutineHostCatalog(t *testing.T) {
	hostScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hostFunction(t, p, b, c, "host_catalog_inc", "increment-v1", hostIncrementPython, "", true)
		hostBag(t, p, b, c, "host_catalog_inc('a=>7,b=>NULL'::hstore)", [][]any{{"a", "8"}, {"b", nil}, {"c", "3"}})
		language := hostRuntimeLanguage(b)
		hostRows(t, p, b, c, "SELECT t.trftype=h.oid AS type_ok,t.trflang=l.oid AS language_ok,f.proargtypes='2281'::oidvector AS from_arg,f.prorettype=2281 AS from_result,r.proargtypes='2281'::oidvector AS to_arg,r.prorettype=h.oid AS to_result FROM pg_transform t JOIN pg_type h ON h.oid=t.trftype JOIN pg_language l ON l.oid=t.trflang JOIN pg_proc f ON f.oid=t.trffromsql JOIN pg_proc r ON r.oid=t.trftosql WHERE h.typname='hstore' AND l.lanname=$1", []string{"type_ok", "language_ok", "from_arg", "from_result", "to_arg", "to_result"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true}}, language)
		if b.PG {
			return
		}
		defer routineLanguagesCleanup(t, c, "DROP LANGUAGE IF EXISTS host_catalog_alias CASCADE", "DROP LANGUAGE IF EXISTS host_catalog_renamed CASCADE")()
		hostExec(t, p, b, c, "CREATE LANGUAGE host_catalog_alias HANDLER go_host_call_handler")
		hostExec(t, p, b, c, "CREATE FUNCTION host_catalog_alias_fn(v hstore) RETURNS hstore LANGUAGE host_catalog_alias AS 'increment-v1'")
		hostBag(t, p, b, c, "host_catalog_alias_fn('a=>7'::hstore)", [][]any{{"a", "8"}, {"c", "3"}})
		hostRows(t, p, b, c, "SELECT count(*) AS v FROM pg_transform t JOIN pg_language l ON l.oid=t.trflang WHERE l.lanname='host_catalog_alias'", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}})
		hostNativeError(t, p.Ctx, c, "CREATE OR REPLACE LANGUAGE host_catalog_alias HANDLER go_host_from_call_handler", "incompatible host language replacement has dependent routines")
		hostBag(t, p, b, c, "host_catalog_alias_fn('a=>7'::hstore)", [][]any{{"a", "8"}, {"c", "3"}})
		hostExec(t, p, b, c, "DROP FUNCTION host_catalog_alias_fn(hstore)")
		hostExec(t, p, b, c, "CREATE OR REPLACE LANGUAGE host_catalog_alias HANDLER go_host_from_call_handler")
		hostExec(t, p, b, c, "CREATE FUNCTION host_catalog_alias_fn(v hstore) RETURNS hstore LANGUAGE host_catalog_alias AS 'direction-v1'")
		hostBag(t, p, b, c, "host_catalog_alias_fn('a=>7,b=>NULL'::hstore)", [][]any{{"a", "9"}, {"b", nil}})
		hostExec(t, p, b, c, "ALTER LANGUAGE go_host RENAME TO host_catalog_renamed")
		hostExec(t, p, b, c, "CREATE LANGUAGE go_host HANDLER go_host_from_call_handler")
		hostBag(t, p, b, c, "host_catalog_inc('a=>7'::hstore)", [][]any{{"a", "8"}, {"c", "3"}})
		hostExec(t, p, b, c, "ALTER FUNCTION go_host_call_handler() RENAME TO host_catalog_handler")
		hostExec(t, p, b, c, "ALTER FUNCTION go_host_from_hstore(internal) RENAME TO host_catalog_from")
		hostExec(t, p, b, c, "ALTER FUNCTION go_host_to_hstore(internal) RENAME TO host_catalog_to")
		hostBag(t, p, b, c, "host_catalog_inc('a=>7'::hstore)", [][]any{{"a", "8"}, {"c", "3"}})
		var originalType int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_type WHERE typname='hstore'").Scan(&originalType); err != nil {
			t.Fatal(err)
		}
		if _, err := c.ExecContext(p.Ctx, "DROP EXTENSION hstore RESTRICT"); err == nil {
			t.Fatal("independent host type dependencies were not enforced")
		}
		hostBag(t, p, b, c, "host_catalog_inc('a=>7'::hstore)", [][]any{{"a", "8"}, {"c", "3"}})
		hostExec(t, p, b, c, "DROP EXTENSION hstore CASCADE")
		hostRows(t, p, b, c, "SELECT count(*) AS v FROM pg_transform", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}})
		hostExec(t, p, b, c, "CREATE EXTENSION hstore")
		var freshType int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_type WHERE typname='hstore'").Scan(&freshType); err != nil || freshType == originalType {
			t.Fatalf("hstore generation identity %d -> %d/%v", originalType, freshType, err)
		}
		hostRows(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='host_catalog_inc'", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}})
		hostExec(t, p, b, c, "CREATE FUNCTION host_catalog_new(v hstore) RETURNS hstore LANGUAGE host_catalog_renamed AS 'double-v1'")
		hostBag(t, p, b, c, "host_catalog_new('a=>7,b=>NULL'::hstore)", [][]any{{"a", "14"}, {"b", nil}, {"d", "second"}})
	})
}

func TestReferenceRoutineHostFeatureSmoke(t *testing.T) {
	hostScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hostFunction(t, p, b, c, "host_smoke_inc", "increment-v1", hostIncrementPython, "", true)
		hostFunction(t, p, b, c, "host_smoke_null", "null-v1", "if v is None:\n    return {'n':'handled'}\nreturn v", "", true)
		hostBag(t, p, b, c, "host_smoke_inc('a=>1,b=>NULL'::hstore)", [][]any{{"a", "2"}, {"b", nil}, {"c", "3"}})
		hostBag(t, p, b, c, "host_smoke_null(NULL::hstore)", [][]any{{"n", "handled"}})
		hostMetadata(t, p, b, c, "SELECT host_smoke_inc('a=>1'::hstore) AS v")
		hostFunction(t, p, b, c, "host_smoke_wrong", "wrong-object-v1", "return 7", "", true)
		if b.PG {
			d := hostPythonError("42809", "not a Python mapping", "host_smoke_wrong")
			d.Context = "while creating return value\n" + d.Context
			hostError(t, p, c, "SELECT host_smoke_wrong('a=>1'::hstore)", d)
		} else {
			hostNativeError(t, p.Ctx, c, "SELECT host_smoke_wrong('a=>1'::hstore)", "host: result is not a nonnil map")
		}
	})
}
