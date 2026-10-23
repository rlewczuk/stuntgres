package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCoreLanguageAuthority(t *testing.T) {
	plpgsqlLanguageDump(t)
	fixture.RoleReferenceCleanup(t, []string{"pl_core_reader"})
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		defer func() {
			for _, q := range []string{"RESET SESSION AUTHORIZATION", "GRANT USAGE ON LANGUAGE plpgsql TO PUBLIC", "REVOKE ALL ON LANGUAGE plpgsql FROM pl_core_reader CASCADE"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error(q, err)
				}
			}
		}()
		userOperatorExec(t, p, b, c,
			"CREATE ROLE pl_core_reader",
			"GRANT USAGE, CREATE ON SCHEMA public TO pl_core_reader",
			"CREATE FUNCTION pl_allowed(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN x+1; END$$")
		// Installation OIDs are deliberately not assumed. These joins must expose
		// the actual descriptors used by the three distinct handler routes.
		run(fixture.Select("SELECT l.lanispl,l.lanpltrusted,h.proname,i.proname,v.proname,h.prorettype::regtype::text,i.prorettype::regtype::text,v.prorettype::regtype::text FROM pg_language l JOIN pg_proc h ON h.oid=l.lanplcallfoid JOIN pg_proc i ON i.oid=l.laninline JOIN pg_proc v ON v.oid=l.lanvalidator WHERE l.lanname='plpgsql'",
			[]string{"lanispl", "lanpltrusted", "proname", "proname", "proname", "prorettype", "prorettype", "prorettype"},
			[]string{"BOOL", "BOOL", "NAME", "NAME", "NAME", "TEXT", "TEXT", "TEXT"},
			[][]any{{true, true, "plpgsql_call_handler", "plpgsql_inline_handler", "plpgsql_validator", "language_handler", "void", "void"}}))
		userOperatorExec(t, p, b, c, "SET SESSION AUTHORIZATION pl_core_reader", "DO $$BEGIN END$$",
			"CREATE FUNCTION pl_reader_created() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$")
		q := "SELECT pl_allowed($1) AS v"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		check := func() {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(5)}}, 4))
		}
		check()
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE USAGE ON LANGUAGE plpgsql FROM PUBLIC,pl_core_reader", "SET SESSION AUTHORIZATION pl_core_reader")
		denied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for language plpgsql")
		plpgsqlError(t, p, c, "DO $$BEGIN END$$", false, denied)
		plpgsqlError(t, p, c, "CREATE FUNCTION pl_denied() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 1; END$$", false, denied)
		check()
		plpgsqlValue(t, p, b, c, "SELECT pl_allowed(4) AS v", "INT4", int64(5))
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION pl_allowed(integer) FROM PUBLIC,pl_core_reader", "SET SESSION AUTHORIZATION pl_core_reader")
		rows, err := held.QueryContext(p.Ctx, 4)
		if rows != nil {
			rows.Close()
			t.Fatal("denied held call published rows")
		}
		fixture.AssertDiagnostic(t, err, new(fixture.ControlsReferenceDiagnostic("42501", "permission denied for function pl_allowed")))
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION pl_allowed(integer) TO pl_core_reader", "SET SESSION AUTHORIZATION pl_core_reader")
		check()
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT USAGE ON LANGUAGE plpgsql TO pl_core_reader", "SET SESSION AUTHORIZATION pl_core_reader", "DO $$BEGIN END$$")
	})
}
