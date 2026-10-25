package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAggregateLifecycleDependencies(t *testing.T) {
	createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var notices []fixture.Diagnostic
		_, noticeConn, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		c = noticeConn
		b.Recorder = rec
		lifecycleSetup(t, p, b, c)
		lifecycleExec(t, p, b, c, "CREATE VIEW al_av AS SELECT al_sum(x) AS v FROM al_input", "CREATE VIEW al_av2 AS SELECT v FROM al_av", "CREATE VIEW al_unrelated AS SELECT x FROM al_input", "CREATE FUNCTION al_soft() RETURNS integer LANGUAGE SQL AS $$SELECT al_sum(x)::integer FROM al_input$$")
		id := lifecycleOID(t, p, c, "al_sum(integer)")
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE refclassid=1255::oid AND refobjid=$1::oid AND classid=2618::oid AND deptype='n'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, id))
		for _, suffix := range []string{"", " RESTRICT"} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("DROP AGGREGATE al_sum(integer)"+suffix, fixture.Diagnostic{Code: "2BP01", Message: "cannot drop function al_sum(integer) because other objects depend on it", Detail: "view al_av depends on function al_sum(integer)\nview al_av2 depends on view al_av", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				createAggregateRun(t, p, b, c, s)
			}
		}
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT v FROM al_av2", int64(6)))
		notices = nil
		lifecycleExec(t, p, b, c, "DROP AGGREGATE al_sum(integer),al_sum(integer) CASCADE")
		wantNotice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to 2 other objects", Detail: "drop cascades to view al_av\ndrop cascades to view al_av2"}
		if len(notices) != 1 || notices[0] != wantNotice {
			t.Fatalf("cascade notices=%#v want=%#v", notices, wantNotice)
		}
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT relname FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN('al_av','al_av2','al_input','al_unrelated') ORDER BY relname", []string{"relname"}, []string{"NAME"}, [][]any{{"al_input"}, {"al_unrelated"}}))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN('al_add','al_add8','al_soft','al_sum') ORDER BY proname", []string{"proname"}, []string{"NAME"}, [][]any{{"al_add"}, {"al_add8"}, {"al_soft"}, {"al_sum"}}))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT al_sum(x::bigint) AS v FROM al_input", []string{"v"}, []string{"INT8"}, [][]any{{int64(106)}}))
		createAggregateBoth(t, p, b, c, createAggregateValue("SELECT al_soft() AS v", int64(106)))
	})
	t.Run("hard_and_soft", func(t *testing.T) {
		createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			lifecycleSetup(t, p, b, c)
			lifecycleExec(t, p, b, c, "ALTER AGGREGATE al_sum(integer) RENAME TO al_unique",
				"CREATE FUNCTION al_hard() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT al_unique(x) FROM al_input; END",
				"CREATE FUNCTION al_soft_missing() RETURNS integer LANGUAGE SQL AS $$SELECT al_unique(x) FROM al_input$$")
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT al_hard() AS v", int64(6)))
			restrict := fixture.ImplicitError("DROP AGGREGATE al_unique(integer)", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop function al_unique(integer) because other objects depend on it", Detail: "function al_hard() depends on function al_unique(integer)", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
			restrict.ErrorStage = "execute"
			createAggregateRun(t, p, b, c, restrict, fixture.Prepared(restrict))
			lifecycleExec(t, p, b, c, "DROP AGGREGATE al_unique(integer) CASCADE")
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN('al_hard','al_soft_missing')", []string{"proname"}, []string{"NAME"}, [][]any{{"al_soft_missing"}}))
			for _, prepared := range []bool{false, true} {
				native := fixture.ControlsReferenceDiagnostic("42883", "function al_unique(integer) does not exist")
				native.Position = "8"
				native.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				native.Context = "SQL function \"al_soft_missing\" statement 1"
				pg := native
				pg.Position = ""
				pg.InternalPosition = "8"
				pg.InternalQuery = "SELECT al_unique(x) FROM al_input"
				pg.Context = "SQL function \"al_soft_missing\" during inlining"
				s := fixture.ErrorDiagnostic("SELECT al_soft_missing() AS v", native)
				s.PostgresDiagnostic = &pg
				s.Prepared = prepared
				s.SelectRows = true
				s.ErrorStage = "execute"
				createAggregateRun(t, p, b, c, s)
			}
		})
	})
}

func TestReferenceAggregateLifecycleAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "al_atomic")
			fixture.RoleReferenceCleanup(t, []string{"al_other", "al_actor"})
			createAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				lifecycleSetup(t, p, b, c)
				lifecycleExec(t, p, b, c, "CREATE VIEW al_av AS SELECT al_sum(x::bigint) AS v FROM al_input", "CREATE FUNCTION al_function(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$", "CREATE AGGREGATE al_taken(integer) (SFUNC=al_add,STYPE=int4,INITCOND='9')", "CREATE SCHEMA al_atomic", "CREATE AGGREGATE al_atomic.al_sum(integer) (SFUNC=al_add,STYPE=int4,INITCOND='9')")
				id := lifecycleOID(t, p, c, "al_sum(integer)")
				for _, tc := range []struct{ q, code, message, detail, hint string }{
					{"DROP AGGREGATE al_sum(integer),al_missing(integer)", "42883", "aggregate al_missing(integer) does not exist", "", ""},
					{"DROP AGGREGATE IF EXISTS al_sum(integer),al_function(integer)", "42809", "function al_function(integer) is not an aggregate", "", ""},
					{"DROP AGGREGATE al_sum(integer),al_sum(bigint)", "2BP01", "cannot drop desired object(s) because other objects depend on them", "view al_av depends on function al_sum(bigint)", "Use DROP ... CASCADE to drop the dependent objects too."},
					{"ALTER AGGREGATE al_sum(integer) RENAME TO al_taken", "42723", `function al_taken(integer) already exists in schema "public"`, "", ""},
					{"ALTER AGGREGATE al_sum(integer) SET SCHEMA al_atomic", "42723", `function al_sum(integer) already exists in schema "al_atomic"`, "", ""},
				} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail, Hint: tc.hint})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					createAggregateRun(t, p, b, c, s)
					lifecycleFacts(t, p, b, c, "al_sum", id)
					lifecycleValues(t, p, b, c, "al_sum")
				}
				defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				lifecycleExec(t, p, b, c, "CREATE ROLE al_actor", "CREATE ROLE al_other", "GRANT USAGE,CREATE ON SCHEMA public TO al_actor,al_other", "ALTER AGGREGATE al_sum(integer) OWNER TO al_actor", "ALTER AGGREGATE al_sum(bigint) OWNER TO al_other", "SET SESSION AUTHORIZATION al_actor")
				denied := fixture.ImplicitError("DROP AGGREGATE al_sum(integer),al_sum(bigint)", fixture.Diagnostic{Code: "42501", Message: "must be owner of aggregate al_sum"})
				denied.Prepared = prepared
				denied.ErrorStage = "execute"
				createAggregateRun(t, p, b, c, denied)
				lifecycleExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
				lifecycleFacts(t, p, b, c, "al_sum", id)
				lifecycleValues(t, p, b, c, "al_sum")
				lifecycleExec(t, p, b, c, "BEGIN", "SAVEPOINT al_save")
				for _, q := range []string{"ALTER AGGREGATE al_sum(integer) RENAME TO al_tx", "ALTER AGGREGATE al_tx(integer) SET SCHEMA al_atomic", "ALTER AGGREGATE al_atomic.al_tx(integer) OWNER TO CURRENT_USER", "DROP AGGREGATE al_atomic.al_tx(integer)"} {
					s := fixture.Exec(q, 0)
					s.Prepared = prepared
					createAggregateRun(t, p, b, c, s)
				}
				lifecycleExec(t, p, b, c, "ROLLBACK TO al_save")
				lifecycleFacts(t, p, b, c, "al_sum", id)
				lifecycleValues(t, p, b, c, "al_sum")
				lifecycleExec(t, p, b, c, "ALTER AGGREGATE al_sum(integer) RENAME TO al_tx")
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT proname FROM pg_proc WHERE oid=$1::oid", []string{"proname"}, []string{"NAME"}, [][]any{{"al_sum"}}, id))
				createAggregateBoth(t, p, b, c, fixture.Select("SELECT proname FROM pg_proc WHERE oid=$1::oid", []string{"proname"}, []string{"NAME"}, [][]any{{"al_tx"}}, id))
				lifecycleExec(t, p, b, c, "ROLLBACK")
				lifecycleFacts(t, p, b, c, "al_sum", id)
				lifecycleExec(t, p, b, c, "BEGIN", "ALTER AGGREGATE al_sum(integer) RENAME TO al_committed", "COMMIT")
				lifecycleFacts(t, p, b, c, "al_committed", id)
				lifecycleExec(t, p, b, c, "DROP AGGREGATE IF EXISTS al_missing(integer),al_committed(integer)")
			})
		})
	}
}
