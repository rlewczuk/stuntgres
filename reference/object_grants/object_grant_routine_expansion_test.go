package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantRoutineSchemaExpansion(t *testing.T) {
	objectGrantScenario(t, []string{"og_routine_scope", "og_routine_empty"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE SCHEMA og_routine_scope", 0))
		run(fixture.Exec("CREATE SCHEMA og_routine_empty", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA og_routine_scope,og_routine_empty TO og_object_reader", 0))
		run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA og_routine_scope", 0))
		if b.PG {
			// PostgreSQL supplies the actual procedure creator/runtime. Native
			// has no such provider; its ALL PROCEDURES expansion is genuinely empty.
			run(fixture.Exec("CREATE PROCEDURE og_routine_scope.p() LANGUAGE SQL AS 'SELECT 1'", 0))
		}
		run(fixture.Exec("REVOKE ALL PRIVILEGES ON ALL ROUTINES IN SCHEMA og_routine_scope FROM PUBLIC", 0))
		call := "SELECT og_routine_scope.unaccent('Hôtel') AS v"
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, call, "function", "unaccent")
			if b.PG {
				fixture.RoleGrantPermissionDenied(run, "CALL og_routine_scope.p()", "procedure", "p")
			}
		})
		fixture.RoleGrantError(run, "GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA og_routine_scope,og_routine_missing TO og_object_reader", "3F000", `schema "og_routine_missing" does not exist`)
		fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RoleGrantPermissionDenied(run, call, "function", "unaccent") })
		run(fixture.Exec("GRANT ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA og_routine_scope TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select(call, []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
			if b.PG {
				fixture.RoleGrantPermissionDenied(run, "CALL og_routine_scope.p()", "procedure", "p")
			}
		})
		run(fixture.Exec("REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA og_routine_scope FROM og_object_reader", 0))
		run(fixture.Exec("GRANT EXECUTE ON ALL PROCEDURES IN SCHEMA og_routine_scope TO og_object_reader", 0))
		run(fixture.Exec("GRANT EXECUTE ON ALL PROCEDURES IN SCHEMA og_routine_empty TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, call, "function", "unaccent")
			if b.PG {
				run(fixture.Exec("CALL og_routine_scope.p()", 0))
			}
		})
		run(fixture.Exec("GRANT EXECUTE ON ALL ROUTINES IN SCHEMA og_routine_scope TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select(call, []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
			if b.PG {
				run(fixture.Exec("CALL og_routine_scope.p()", 0))
			}
		})
		var revokeOwner string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&revokeOwner); err != nil {
			t.Fatal(err)
		}
		revokeACL := func() {
			// Native pg_proc does not expose proacl. The shared successful
			// consumer below proves that failed REVOKE published no prefix.
			if !b.PG {
				return
			}
			s := fixture.Select("SELECT unnest(proacl) AS acl FROM pg_proc WHERE oid='og_routine_scope.unaccent(text)'::regprocedure", []string{"acl"}, []string{"ACLITEM"}, [][]any{{revokeOwner + "=X/" + revokeOwner}, {"og_object_reader=X/" + revokeOwner}})
			s.Bag = true
			run(s)
		}
		revokeACL()
		fixture.RoleGrantError(run, "REVOKE EXECUTE ON ALL ROUTINES IN SCHEMA og_routine_scope,og_routine_missing FROM og_object_reader", "3F000", `schema "og_routine_missing" does not exist`)
		revokeACL()
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select(call, []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
			if b.PG {
				run(fixture.Exec("CALL og_routine_scope.p()", 0))
			}
		})
		run(fixture.Exec("REVOKE EXECUTE ON ALL PROCEDURES IN SCHEMA og_routine_scope FROM og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select(call, []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
			if b.PG {
				fixture.RoleGrantPermissionDenied(run, "CALL og_routine_scope.p()", "procedure", "p")
			}
		})
		run(fixture.Exec("GRANT EXECUTE ON ALL PROCEDURES IN SCHEMA og_routine_scope TO og_object_reader", 0))
		run(fixture.Exec("REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA og_routine_scope FROM og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, call, "function", "unaccent")
			if b.PG {
				run(fixture.Exec("CALL og_routine_scope.p()", 0))
			}
		})
		run(fixture.Exec("GRANT EXECUTE ON ALL ROUTINES IN SCHEMA og_routine_scope TO og_object_reader", 0))
		run(fixture.Exec("REVOKE EXECUTE ON ALL ROUTINES IN SCHEMA og_routine_scope FROM og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, call, "function", "unaccent")
			if b.PG {
				fixture.RoleGrantPermissionDenied(run, "CALL og_routine_scope.p()", "procedure", "p")
			}
		})
		run(fixture.Exec("GRANT EXECUTE ON ALL ROUTINES IN SCHEMA og_routine_scope TO og_object_reader", 0))
		if b.PG {
			fixture.RoleGrantError(run, "GRANT EXECUTE ON FUNCTION og_routine_scope.p() TO og_object_reader", "42809", "og_routine_scope.p() is not a function")
			run(fixture.Exec("GRANT EXECUTE ON PROCEDURE og_routine_scope.p TO og_object_reader", 0))
			// Expansion covers current objects only, never future function defaults.
			run(fixture.Exec("CREATE FUNCTION og_routine_scope.later() RETURNS INTEGER LANGUAGE SQL AS 'SELECT 9'", 0))
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION og_routine_scope.later() FROM PUBLIC", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RoleGrantPermissionDenied(run, "SELECT og_routine_scope.later()", "function", "later") })
		}
		run(fixture.Exec("REVOKE USAGE ON SCHEMA og_routine_scope FROM og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, call, "schema", "og_routine_scope", prepared)
			fixture.RoleGrantError(run, "GRANT EXECUTE ON ALL ROUTINES IN SCHEMA og_routine_scope TO og_object_other", "42501", "permission denied for schema og_routine_scope")
		})
	})
}

func TestReferenceObjectGrantRoutineSchemaKinds(t *testing.T) {
	objectGrantScenario(t, []string{"og_kind_scope", "og_kind_other"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.sum(integer)", "pg_catalog.row_number()"})
		defer func() {
			for _, query := range []string{"RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON ALL ROUTINES IN SCHEMA pg_catalog FROM og_object_reader,og_object_other CASCADE"} {
				if _, err := c.ExecContext(p.Ctx, query); err != nil {
					t.Error("catalog ALL routine ACL cleanup", query, err)
				}
			}
		}()
		run(fixture.Exec("CREATE SCHEMA og_kind_scope", 0))
		run(fixture.Exec("CREATE SCHEMA og_kind_other", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA og_kind_scope,og_kind_other TO og_object_reader,og_object_other", 0))
		run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA og_kind_scope", 0))
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA og_kind_other", 0))
		run(fixture.Exec("REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA og_kind_scope,og_kind_other FROM PUBLIC", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.sum(integer),pg_catalog.row_number() FROM PUBLIC", 0))
		sum := "SELECT sum(x) AS v FROM (VALUES(1),(2)) AS s(x)"
		window := "SELECT row_number() OVER(ORDER BY x) AS v FROM (VALUES(1),(2)) AS s(x)"
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, sum, "aggregate", "sum")
			fixture.RoleGrantPermissionDenied(run, window, "function", "row_number")
		})
		run(fixture.Exec("GRANT EXECUTE ON ALL PROCEDURES IN SCHEMA pg_catalog TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, sum, "aggregate", "sum")
			fixture.RoleGrantPermissionDenied(run, window, "function", "row_number")
		})
		run(fixture.Exec("GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA pg_catalog,og_kind_scope TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select(sum, []string{"v"}, []string{"INT8"}, [][]any{{int64(3)}}))
			run(fixture.Select(window, []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}}))
			run(fixture.Select("SELECT og_kind_scope.unaccent('Hôtel') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
			fixture.RoleGrantPermissionDenied(run, "SELECT og_kind_other.daitch_mokotoff('Biersack')", "function", "daitch_mokotoff")
			run(fixture.Select("SELECT generate_series(1::integer,2::integer) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.generate_series(numeric,numeric) TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT generate_series(0.1::numeric,1.1::numeric) AS v", []string{"v"}, []string{"NUMERIC"}, [][]any{{"0.1"}, {"1.1"}}))
		})
		lookup := "GRANT EXECUTE ON FUNCTION pg_catalog.generate_series(timestamp without time zone,timestamp without time zone,interval) TO og_object_reader"
		if b.PG {
			run(fixture.Exec(lookup, 0))
		} else {
			fixture.RoleGrantError(run, lookup, "0A000", "routine privileges are unsupported for unimplemented routines in embedded mode")
		}
		// Existing expansion is not a future-object default. This is a genuine
		// newly installed native provider, not a native function-creator shim.
		run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA og_kind_scope", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION og_kind_scope.digest(text,text) FROM PUBLIC", 0))
		digest := "SELECT encode(og_kind_scope.digest('abc','md5'),'hex') AS v"
		fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RoleGrantPermissionDenied(run, digest, "function", "digest") })
		run(fixture.Exec("GRANT EXECUTE ON ALL ROUTINES IN SCHEMA og_kind_scope TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select(digest, []string{"v"}, []string{"TEXT"}, [][]any{{"900150983cd24fb0d6963f7d28e17f72"}}))
		})
		run(fixture.Exec("REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA og_kind_scope FROM og_object_reader", 0))
		fixture.RoleGrantError(run, "GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA og_kind_scope,og_kind_missing TO og_object_reader WITH GRANT OPTION", "3F000", `schema "og_kind_missing" does not exist`)
		fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RoleGrantPermissionDenied(run, digest, "function", "digest") })
		run(fixture.Exec("GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA og_kind_scope TO og_object_reader WITH GRANT OPTION", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION og_kind_scope.digest(text,text) TO og_object_other", 0))
		})
		fixture.RoleGrantActor(run, "og_object_other", func() {
			run(fixture.Select(digest, []string{"v"}, []string{"TEXT"}, [][]any{{"900150983cd24fb0d6963f7d28e17f72"}}))
		})
		run(fixture.Exec("REVOKE GRANT OPTION FOR EXECUTE ON ALL FUNCTIONS IN SCHEMA og_kind_scope FROM og_object_reader CASCADE", 0))
		fixture.RoleGrantActor(run, "og_object_other", func() { fixture.RoleGrantPermissionDenied(run, digest, "function", "digest") })
	})
}
