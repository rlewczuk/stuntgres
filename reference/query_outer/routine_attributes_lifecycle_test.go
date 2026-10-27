package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineAttributesAlterLifecycle(t *testing.T) {
	for _, result := range []struct {
		language string
		set      bool
	}{{"sql", false}, {"sql", true}, {"plpgsql", false}} {
		language, set := result.language, result.set
		name := language + "_scalar"
		if set {
			name = language + "_set"
		}
		t.Run(name, func(t *testing.T) {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				returns, body := "integer", "AS $$SELECT coalesce($1,2)$$"
				if set {
					returns = "SETOF integer"
				}
				if language == "plpgsql" {
					body = "AS $$BEGIN RETURN coalesce($1,2); END$$"
				}
				resultsExec(t, p, b, c, "CREATE FUNCTION ra_l_live(integer) RETURNS "+returns+" LANGUAGE "+language+" "+body)
				q := "SELECT ra_l_live($1::integer) AS v"
				if set {
					q = "SELECT * FROM ra_l_live($1::integer) AS v"
				}
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				check := func(value any, empty bool) {
					rows := [][]any{{value}}
					if empty {
						rows = [][]any{}
					}
					proceduresBoth(t, p, b, c, q, []string{"v"}, []string{"INT4"}, rows, nil)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, proceduresStep(b, q, []string{"v"}, []string{"INT4"}, rows, nil))
				}
				check(int64(2), false)
				rows := float64(0)
				if set {
					rows = 1000
				}
				for _, tc := range []struct {
					action, volatility, parallel string
					strict, definer              bool
					cost                         float64
					config                       any
				}{
					{"STRICT IMMUTABLE SECURITY DEFINER PARALLEL SAFE COST 7 SET application_name='local'", "i", "s", true, true, 7, "{application_name=local}"},
					{"STABLE PARALLEL RESTRICTED", "s", "r", true, true, 7, "{application_name=local}"},
					{"CALLED ON NULL INPUT VOLATILE SECURITY INVOKER PARALLEL UNSAFE COST 9 RESET ALL", "v", "u", false, false, 9, nil},
				} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec("ALTER FUNCTION ra_l_live(integer) "+tc.action, 0)))
					attributesDeclarationCatalogBoth(t, p, b, c, "SELECT provolatile,proisstrict,prosecdef,proparallel,procost,prorows,proconfig FROM pg_proc WHERE oid='ra_l_live(integer)'::regprocedure", []string{"provolatile", "proisstrict", "prosecdef", "proparallel", "procost", "prorows", "proconfig"}, []string{"CHAR", "BOOL", "BOOL", "CHAR", "FLOAT4", "FLOAT4", "_TEXT"}, [][]any{{tc.volatility, tc.strict, tc.definer, tc.parallel, tc.cost, rows, tc.config}})
					if tc.strict {
						check(nil, set)
					} else {
						check(int64(2), false)
					}
				}
				if set {
					resultsExec(t, p, b, c, "ALTER FUNCTION ra_l_live(integer) ROWS 12")
					attributesDeclarationCatalogBoth(t, p, b, c, "SELECT prorows,procost FROM pg_proc WHERE oid='ra_l_live(integer)'::regprocedure", []string{"prorows", "procost"}, []string{"FLOAT4", "FLOAT4"}, [][]any{{float64(12), float64(9)}})
					check(int64(2), false)
				}
				for _, tc := range []struct{ action, code, message string }{
					{"STRICT COST 0", "22023", "COST must be positive"},
					{"IMMUTABLE ROWS -1", "22023", "ROWS must be positive"},
					{"STRICT SET definitely_missing_setting='x'", "42704", `unrecognized configuration parameter "definitely_missing_setting"`},
				} {
					resultsError(t, p, c, "ALTER FUNCTION ra_l_live(integer) "+tc.action, false, "execute", proceduresDiagnostic(tc.code, tc.message, 0))
					check(int64(2), false)
					wantRows := rows
					if set {
						wantRows = 12
					}
					attributesDeclarationCatalogBoth(t, p, b, c, "SELECT provolatile,proisstrict,prosecdef,proparallel,procost,prorows,proconfig FROM pg_proc WHERE oid='ra_l_live(integer)'::regprocedure", []string{"provolatile", "proisstrict", "prosecdef", "proparallel", "procost", "prorows", "proconfig"}, []string{"CHAR", "BOOL", "BOOL", "CHAR", "FLOAT4", "FLOAT4", "_TEXT"}, [][]any{{"v", false, false, "u", float64(9), wantRows, nil}})
				}
			})
		})
	}
	t.Run("replace_identity_owner_acl_and_retained_rows", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ra_l_owner", "ra_l_reader"})
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c, "CREATE ROLE ra_l_owner", "CREATE ROLE ra_l_reader", "GRANT USAGE,CREATE ON SCHEMA public TO ra_l_owner", "CREATE FUNCTION ra_l_replace(integer) RETURNS integer LANGUAGE SQL IMMUTABLE STRICT SECURITY DEFINER PARALLEL SAFE COST 7 SET application_name='inside' AS 'SELECT $1+1'", "ALTER FUNCTION ra_l_replace(integer) OWNER TO ra_l_owner", "REVOKE ALL ON FUNCTION ra_l_replace(integer) FROM PUBLIC", "GRANT EXECUTE ON FUNCTION ra_l_replace(integer) TO ra_l_reader")
			q := "SELECT ra_l_replace($1::integer) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			var oid, owner int64
			var acl string
			if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,proowner::bigint,proacl::text FROM pg_proc WHERE oid='ra_l_replace(integer)'::regprocedure").Scan(&oid, &owner, &acl); err != nil || oid <= 0 || owner <= 0 || acl == "" {
				t.Fatal(oid, owner, acl, err)
			}
			// pq streams and owns its physical connection; native rows are already
			// detached. Keep the native artifact open during replacement only.
			var old *sql.Rows
			if !b.PG {
				old, err = stmt.QueryContext(p.Ctx, 4)
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				proceduresMetadata(b, []string{"v"}, []string{"INT4"})(t, old)
			}
			resultsExec(t, p, b, c, "CREATE OR REPLACE FUNCTION ra_l_replace(integer) RETURNS integer LANGUAGE SQL AS 'SELECT coalesce($1,20)+2'")
			attributesDeclarationCatalogBoth(t, p, b, c, "SELECT provolatile,proisstrict,prosecdef,proparallel,procost,prorows,proconfig FROM pg_proc WHERE oid='ra_l_replace(integer)'::regprocedure", []string{"provolatile", "proisstrict", "prosecdef", "proparallel", "procost", "prorows", "proconfig"}, []string{"CHAR", "BOOL", "BOOL", "CHAR", "FLOAT4", "FLOAT4", "_TEXT"}, [][]any{{"v", false, false, "u", float64(100), float64(0), nil}})
			attributesBoth(t, p, b, c, "SELECT oid::bigint AS id,proowner::bigint AS owner,proacl::text AS acl FROM pg_proc WHERE oid='ra_l_replace(integer)'::regprocedure", []string{"id", "owner", "acl"}, []string{"INT8", "INT8", "TEXT"}, [][]any{{oid, owner, acl}})
			for _, tc := range []struct{ arg, want any }{{nil, int64(22)}, {4, int64(6)}} {
				proceduresBoth(t, p, b, c, q, []string{"v"}, []string{"INT4"}, [][]any{{tc.want}}, tc.arg)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, proceduresStep(b, q, []string{"v"}, []string{"INT4"}, [][]any{{tc.want}}, tc.arg))
			}
			resultsError(t, p, c, "CREATE OR REPLACE FUNCTION ra_l_replace(integer) RETURNS integer LANGUAGE SQL STRICT SECURITY DEFINER COST 0 AS 'SELECT 999'", false, "execute", proceduresDiagnostic("22023", "COST must be positive", 0))
			attributesDeclarationCatalogBoth(t, p, b, c, "SELECT provolatile,proisstrict,prosecdef,proparallel,procost,prorows,proconfig FROM pg_proc WHERE oid='ra_l_replace(integer)'::regprocedure", []string{"provolatile", "proisstrict", "prosecdef", "proparallel", "procost", "prorows", "proconfig"}, []string{"CHAR", "BOOL", "BOOL", "CHAR", "FLOAT4", "FLOAT4", "_TEXT"}, [][]any{{"v", false, false, "u", float64(100), float64(0), nil}})
			attributesBoth(t, p, b, c, "SELECT oid::bigint AS id,proowner::bigint AS owner,proacl::text AS acl FROM pg_proc WHERE oid='ra_l_replace(integer)'::regprocedure", []string{"id", "owner", "acl"}, []string{"INT8", "INT8", "TEXT"}, [][]any{{oid, owner, acl}})
			fixture.RunStep(t, p.Ctx, held, b.Recorder, proceduresStep(b, q, []string{"v"}, []string{"INT4"}, [][]any{{int64(22)}}, nil))
			if old != nil {
				proceduresMetadata(b, []string{"v"}, []string{"INT4"})(t, old)
				if !old.Next() {
					t.Fatal("old row missing", old.Err())
				}
				var value any
				if err := old.Scan(&value); err != nil || value != int64(5) {
					t.Fatal(value, err)
				}
				if old.Next() || old.Err() != nil {
					t.Fatal("retained rows EOF", old.Err())
				}
			}
			// Owner transfer is looked up at each invocation of the same statement.
			resultsExec(t, p, b, c, "CREATE FUNCTION ra_l_actor() RETURNS text LANGUAGE SQL SECURITY DEFINER AS 'SELECT current_user::text'")
			actorQ := "SELECT ra_l_actor() AS v"
			actor, err := c.PrepareContext(p.Ctx, actorQ)
			if err != nil {
				t.Fatal(err)
			}
			defer actor.Close()
			var original string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_user::text").Scan(&original); err != nil {
				t.Fatal(err)
			}
			actorHeld := fixture.HeldStatement{Executor: c, Statement: actor, SQL: actorQ}
			fixture.RunStep(t, p.Ctx, actorHeld, b.Recorder, proceduresStep(b, actorQ, []string{"v"}, []string{"TEXT"}, [][]any{{original}}))
			resultsExec(t, p, b, c, "ALTER FUNCTION ra_l_actor() OWNER TO ra_l_owner")
			fixture.RunStep(t, p.Ctx, actorHeld, b.Recorder, proceduresStep(b, actorQ, []string{"v"}, []string{"TEXT"}, [][]any{{"ra_l_owner"}}))
			resultsExec(t, p, b, c, "ALTER FUNCTION ra_l_actor() SECURITY INVOKER")
			fixture.RunStep(t, p.Ctx, actorHeld, b.Recorder, proceduresStep(b, actorQ, []string{"v"}, []string{"TEXT"}, [][]any{{original}}))
			resultsExec(t, p, b, c, "ALTER FUNCTION ra_l_actor() SECURITY DEFINER")
			fixture.RunStep(t, p.Ctx, actorHeld, b.Recorder, proceduresStep(b, actorQ, []string{"v"}, []string{"TEXT"}, [][]any{{"ra_l_owner"}}))
			resultsExec(t, p, b, c, "REVOKE ALL ON FUNCTION ra_l_replace(integer) FROM ra_l_owner,ra_l_reader", "CREATE OR REPLACE FUNCTION ra_l_replace(integer) RETURNS integer LANGUAGE SQL AS 'SELECT 30'")
			attributesBoth(t, p, b, c, "SELECT proacl::text AS acl FROM pg_proc WHERE oid='ra_l_replace(integer)'::regprocedure", []string{"acl"}, []string{"TEXT"}, [][]any{{"{}"}})
		})
	})
	t.Run("held_configuration_effect", func(t *testing.T) {
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c, "SET application_name='outside'", "CREATE FUNCTION ra_l_setting() RETURNS text LANGUAGE SQL AS $$SELECT current_setting('application_name')$$")
			q := "SELECT ra_l_setting() AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct{ action, want string }{{"SET application_name='inside'", "inside"}, {"RESET application_name", "outside"}} {
				resultsExec(t, p, b, c, "ALTER FUNCTION ra_l_setting() "+tc.action)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, proceduresStep(b, q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}))
				resultsBothMetadata(t, p, b, c, "SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"outside"}}, []resultsNativeColumn{{Length: 1<<63 - 1, LengthKnown: true, NullableKnown: true}})
			}
		})
	})
	for _, language := range []string{"sql", "plpgsql"} {
		t.Run(language+"_procedure_alter", func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"ra_l_procowner"})
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				body := "AS $$SELECT current_user::text,current_setting('application_name')$$"
				if language == "plpgsql" {
					body = "AS $$BEGIN actor := current_user::text; setting := current_setting('application_name'); END$$"
				}
				resultsExec(t, p, b, c, "CREATE ROLE ra_l_procowner", "GRANT USAGE,CREATE ON SCHEMA public TO ra_l_procowner", "SET application_name='outside'", "CREATE PROCEDURE ra_l_proc(OUT actor text,OUT setting text) LANGUAGE "+language+" "+body)
				var original string
				if err := c.QueryRowContext(p.Ctx, "SELECT current_user::text").Scan(&original); err != nil {
					t.Fatal(err)
				}
				q := "CALL ra_l_proc(NULL,NULL)"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				check := func(actor, setting string) {
					proceduresBoth(t, p, b, c, q, []string{"actor", "setting"}, []string{"TEXT", "TEXT"}, [][]any{{actor, setting}})
					fixture.RunStep(t, p.Ctx, held, b.Recorder, proceduresStep(b, q, []string{"actor", "setting"}, []string{"TEXT", "TEXT"}, [][]any{{actor, setting}}))
					resultsBothMetadata(t, p, b, c, "SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"outside"}}, []resultsNativeColumn{{Length: 1<<63 - 1, LengthKnown: true, NullableKnown: true}})
				}
				check(original, "outside")
				resultsExec(t, p, b, c, "ALTER PROCEDURE ra_l_proc() OWNER TO ra_l_procowner", "ALTER PROCEDURE ra_l_proc() SECURITY DEFINER SET application_name='inside'")
				check("ra_l_procowner", "inside")
				attributesDeclarationCatalogBoth(t, p, b, c, "SELECT prosecdef,proconfig FROM pg_proc WHERE proname='ra_l_proc'", []string{"prosecdef", "proconfig"}, []string{"BOOL", "_TEXT"}, [][]any{{true, "{application_name=inside}"}})
				resultsExec(t, p, b, c, "ALTER PROCEDURE ra_l_proc() SECURITY INVOKER RESET ALL")
				check(original, "outside")
				attributesDeclarationCatalogBoth(t, p, b, c, "SELECT prosecdef,proconfig FROM pg_proc WHERE proname='ra_l_proc'", []string{"prosecdef", "proconfig"}, []string{"BOOL", "_TEXT"}, [][]any{{false, nil}})
			})
		})
	}
}
