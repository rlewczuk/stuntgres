package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUserOperatorOwnershipSchemaLifecycle(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"userop_owner", "userop_reader", "userop_next"})
	fixture.SchemaReferenceCleanup(t, "userop_old", "userop_new", "userop_moved")
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		userOperatorExec(t, p, b, c, "CREATE ROLE userop_owner LOGIN", "CREATE ROLE userop_reader LOGIN", "CREATE ROLE userop_next LOGIN",
			"CREATE SCHEMA userop_old AUTHORIZATION userop_owner", "CREATE SCHEMA userop_moved AUTHORIZATION userop_owner",
			"GRANT USAGE ON SCHEMA userop_old,userop_moved TO userop_reader,userop_next",
			"SET ROLE userop_owner", "CREATE OPERATOR userop_old.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl)",
			"CREATE VIEW userop_old.result AS SELECT 2 OPERATOR(userop_old.#~#) 3 AS v", "RESET ROLE")
		var original int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'userop_old.#~#(int4,int4)'::regoperator::oid").Scan(&original); err != nil || original == 0 {
			t.Fatal(original, err)
		}
		userOperatorExec(t, p, b, c, "SET ROLE userop_reader")
		for _, tc := range []struct{ q, message string }{
			{"ALTER OPERATOR userop_old.#~# (int4,int4) OWNER TO userop_reader", "must be owner of operator #~#"},
			{"ALTER OPERATOR userop_old.#~# (int4,int4) SET (RESTRICT=NONE)", "must be owner of operator #~#"},
			{"DROP OPERATOR userop_old.#~# (int4,int4)", "must be owner of operator userop_old.#~#"},
		} {
			t.Run(tc.q, func(t *testing.T) { userOperatorError(t, p, b, c, tc.q, "42501", tc.message, "", "", "", false) })
		}
		userOperatorError(t, p, b, c, "ALTER OPERATOR userop_old.#~# (int4,int4) SET SCHEMA userop_moved", "42501", "permission denied for schema userop_moved", "", "", "", false)
		userOperatorError(t, p, b, c, "CREATE OPERATOR userop_old.#?# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl)", "42501", "permission denied for schema userop_old", "", "", "", false)
		userOperatorExec(t, p, b, c, "RESET ROLE", "ALTER SCHEMA userop_old RENAME TO userop_new")
		userOperatorValue(t, p, b, c, "SELECT v FROM userop_new.result", "INT4", int64(5))
		userOperatorValue(t, p, b, c, "SELECT 'userop_new.#~#(int4,int4)'::regoperator::oid::int8 AS v", "INT8", original)
		userOperatorExec(t, p, b, c, "SET ROLE userop_owner", "ALTER OPERATOR userop_new.#~# (int4,int4) SET SCHEMA userop_moved", "RESET ROLE")
		userOperatorValue(t, p, b, c, "SELECT 'userop_moved.#~#(int4,int4)'::regoperator::oid::int8 AS v", "INT8", original)
		userOperatorValue(t, p, b, c, "SELECT v FROM userop_new.result", "INT4", int64(5))
		userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(userop_moved.#~#) 3 AS v", "INT4", int64(5))
		userOperatorExec(t, p, b, c, "SET ROLE userop_owner")
		userOperatorError(t, p, b, c, "ALTER OPERATOR userop_moved.#~# (int4,int4) OWNER TO userop_absent", "42704", `role "userop_absent" does not exist`, "", "", "", false)
		userOperatorError(t, p, b, c, "ALTER OPERATOR userop_moved.#~# (int4,int4) OWNER TO userop_next", "42501", `must be able to SET ROLE "userop_next"`, "", "", "", false)
		userOperatorExec(t, p, b, c, "RESET ROLE", "GRANT userop_next TO userop_owner", "SET ROLE userop_owner")
		userOperatorError(t, p, b, c, "ALTER OPERATOR userop_moved.#~# (int4,int4) OWNER TO userop_next", "42501", "permission denied for schema userop_moved", "", "", "", false)
		userOperatorExec(t, p, b, c, "RESET ROLE", "GRANT CREATE ON SCHEMA userop_moved TO userop_next", "SET ROLE userop_owner", "ALTER OPERATOR userop_moved.#~# (int4,int4) OWNER TO userop_next", "RESET ROLE")
		userOperatorValue(t, p, b, c, "SELECT oprowner=(SELECT oid FROM pg_roles WHERE rolname='userop_next') AS v FROM pg_operator WHERE oid='userop_moved.#~#(int4,int4)'::regoperator", "BOOL", true)
		userOperatorExec(t, p, b, c, "REASSIGN OWNED BY userop_next TO userop_owner")
		userOperatorValue(t, p, b, c, "SELECT oprowner=(SELECT oid FROM pg_roles WHERE rolname='userop_owner') AS v FROM pg_operator WHERE oid='userop_moved.#~#(int4,int4)'::regoperator", "BOOL", true)
		userOperatorExec(t, p, b, c, "DROP OWNED BY userop_owner CASCADE")
		userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_operator WHERE oid=$1::oid", "INT8", int64(0), original)
	})
}

func TestReferenceUserOperatorDependenciesPrepared(t *testing.T) {
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl)",
			"CREATE TABLE userop_sentinel(v integer)", "CREATE VIEW userop_view AS SELECT 2 OPERATOR(public.#~#) 3 AS v")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO userop_sentinel VALUES (42)", 1))
		q := "SELECT 2 OPERATOR(public.#~#) 3 AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		five := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}})
		fixture.RunStep(t, p.Ctx, held, b.Recorder, five)
		userOperatorError(t, p, b, c, "DROP OPERATOR public.#~# (int4,int4)", "2BP01", "cannot drop operator #~#(integer,integer) because other objects depend on it", "view userop_view depends on operator #~#(integer,integer)", "Use DROP ... CASCADE to drop the dependent objects too.", "", false)
		userOperatorValue(t, p, b, c, "SELECT v FROM userop_view", "INT4", int64(5))
		userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_depend WHERE classid='pg_operator'::regclass AND objid='public.#~#(int4,int4)'::regoperator AND refclassid='pg_namespace'::regclass AND refobjid='public'::regnamespace AND deptype='n'", "INT8", int64(1))
		userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_depend WHERE classid='pg_operator'::regclass AND objid='public.#~#(int4,int4)'::regoperator AND refclassid='pg_proc'::regclass", "INT8", int64(0))
		userOperatorExec(t, p, b, c, "DROP VIEW userop_view", "CREATE TABLE userop_default(v integer DEFAULT (2 OPERATOR(public.#~#) 3))")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO userop_default DEFAULT VALUES", 1))
		userOperatorValue(t, p, b, c, "SELECT v FROM userop_default", "INT4", int64(5))
		userOperatorError(t, p, b, c, "DROP OPERATOR public.#~# (int4,int4)", "2BP01", "cannot drop operator #~#(integer,integer) because other objects depend on it", "default value for column v of table userop_default depends on operator #~#(integer,integer)", "Use DROP ... CASCADE to drop the dependent objects too.", "", false)
		userOperatorExec(t, p, b, c, "DROP TABLE userop_default", "CREATE TABLE userop_check(v integer CONSTRAINT userop_valid CHECK ((v OPERATOR(public.#~#) 3)>0))")
		userOperatorError(t, p, b, c, "DROP OPERATOR public.#~# (int4,int4)", "2BP01", "cannot drop operator #~#(integer,integer) because other objects depend on it", "constraint userop_valid on table userop_check depends on operator #~#(integer,integer)", "Use DROP ... CASCADE to drop the dependent objects too.", "", false)
		userOperatorExec(t, p, b, c, "DROP TABLE userop_check", "CREATE VIEW userop_alias AS SELECT 'public.#~#(int4,int4)'::regoperator AS v")
		userOperatorError(t, p, b, c, "DROP OPERATOR public.#~# (int4,int4)", "2BP01", "cannot drop operator #~#(integer,integer) because other objects depend on it", "view userop_alias depends on operator #~#(integer,integer)", "Use DROP ... CASCADE to drop the dependent objects too.", "", false)
		userOperatorExec(t, p, b, c, "DROP OPERATOR public.#~# (int4,int4) CASCADE")
		userOperatorValue(t, p, b, c, "SELECT v FROM userop_sentinel", "INT4", int64(42))
		userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_class WHERE relname='userop_alias' AND relnamespace='public'::regnamespace", "INT8", int64(0))
		userOperatorExec(t, p, b, c, "CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4mi)")
		minus := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(-1)}})
		fixture.RunStep(t, p.Ctx, held, b.Recorder, minus)
	})
}
