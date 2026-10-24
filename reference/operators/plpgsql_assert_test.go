package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLAssert(t *testing.T) {
	for _, body := range []string{
		"ASSERT true;", "ASSERT true,1/0;", "ASSERT true,missing_raise_assert_message();", "ASSERT 'yes'::text;",
	} {
		t.Run(body, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for _, prepared := range []bool{false, true} {
					s := fixture.Exec(raiseAssertDO(body), 0)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		})
	}
	for _, tc := range []struct{ name, body, code, message string }{
		{"false", "ASSERT false;", "P0004", "assertion failed"},
		{"null", "ASSERT NULL;", "P0004", "assertion failed"},
		{"custom", "ASSERT false,'custom';", "P0004", "custom"},
		{"numeric", "ASSERT false,7;", "P0004", "7"},
		{"empty", "ASSERT false,'';", "P0004", ""},
		{"null_message", "ASSERT false,NULL;", "P0004", "assertion failed"},
		{"condition_error", "ASSERT 1/0=0;", "22012", "division by zero"},
		{"message_error", "ASSERT false,1/0;", "22012", "division by zero"},
		{"missing_message", "ASSERT false,missing_raise_assert_message();", "42883", "function missing_raise_assert_message() does not exist"},
		{"others_excluded", "BEGIN ASSERT false; EXCEPTION WHEN OTHERS THEN RETURN; END;", "P0004", "assertion failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn) {
				want := plpgsqlRuntimeDiagnostic(tc.code, tc.message, "inline_code_block", "ASSERT")
				switch tc.name {
				case "condition_error":
					want.Context = "PL/pgSQL expression \"1/0=0\"\n" + want.Context
				case "message_error":
					want.Context = "PL/pgSQL expression \"1/0\"\n" + want.Context
				case "missing_message":
					want.Hint = parametersNoMatchHint
					want.InternalPosition = fixture.Position(1)
					want.InternalQuery = "missing_raise_assert_message()"
				}
				plpgsqlError(t, p, c, raiseAssertDO(tc.body), false, want)
			})
		})
	}
	t.Run("once_only_and_named_handler", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE SEQUENCE assert_seq",
				"CREATE FUNCTION assert_once(ok boolean) RETURNS integer LANGUAGE plpgsql AS $$BEGIN ASSERT nextval('assert_seq')>0 AND ok,nextval('assert_seq'); RETURN 7; EXCEPTION WHEN assert_failure THEN RETURN 9; END$$")
			for _, prepared := range []bool{false, true} {
				userOperatorExec(t, p, b, c, "ALTER SEQUENCE assert_seq RESTART WITH 1")
				for _, tc := range []struct{ ok bool; value, count int64 }{{true, 7, 1}, {false, 9, 3}} {
					s := fixture.Select("SELECT assert_once($1) AS v", []string{"v"}, []string{"INT4"}, [][]any{{tc.value}}, tc.ok)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT last_value AS v FROM assert_seq", []string{"v"}, []string{"INT8"}, [][]any{{tc.count}}))
				}
			}
		})
	})
	t.Run("effective_setting", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, raiseAssertDO("ASSERT true;"),
				"CREATE FUNCTION assert_live() RETURNS integer LANGUAGE plpgsql AS $$BEGIN ASSERT false,'live'; RETURN 7; END$$")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT name,setting,reset_val,boot_val,vartype,context,category,short_desc,unit,min_val,max_val,extra_desc,enumvals,pending_restart FROM pg_settings WHERE name='plpgsql.check_asserts'", []string{"name", "setting", "reset_val", "boot_val", "vartype", "context", "category", "short_desc", "unit", "min_val", "max_val", "extra_desc", "enumvals", "pending_restart"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "_TEXT", "BOOL"}, [][]any{{"plpgsql.check_asserts", "on", "on", "on", "bool", "user", "Customized Options", "Perform checks given in ASSERT statements.", nil, nil, nil, nil, nil, false}}))
			q := "SELECT assert_live() AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal(err) }
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			want := plpgsqlRuntimeDiagnostic("P0004", "live", "assert_live()", "ASSERT")
			checkFailure := func() {
				rows, err := stmt.QueryContext(p.Ctx)
				if rows != nil { rows.Close(); t.Fatal("assert failure exposed rows") }
				fixture.AssertDiagnostic(t, err, &want)
			}
			checkFailure()
			userOperatorExec(t, p, b, c, "SET plpgsql.check_asserts TO off",
				raiseAssertDO("ASSERT missing_raise_assert_condition(),missing_raise_assert_message();"),
				raiseAssertDO("ASSERT 1/0=0,1/0;"))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer peer.Close()
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec(raiseAssertDO("ASSERT true;"), 0))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SHOW plpgsql.check_asserts", []string{"plpgsql.check_asserts"}, []string{"TEXT"}, [][]any{{"on"}}))
			userOperatorExec(t, p, b, c, "RESET plpgsql.check_asserts")
			checkFailure()
			for _, end := range []string{"ROLLBACK", "COMMIT"} {
				userOperatorExec(t, p, b, c, "BEGIN", "SET LOCAL plpgsql.check_asserts TO off")
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
				userOperatorExec(t, p, b, c, end)
				checkFailure()
			}
			userOperatorExec(t, p, b, c, "BEGIN", "SAVEPOINT assertion_setting", "SET LOCAL plpgsql.check_asserts TO off", "ROLLBACK TO assertion_setting")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SHOW plpgsql.check_asserts", []string{"plpgsql.check_asserts"}, []string{"TEXT"}, [][]any{{"on"}}))
			userOperatorExec(t, p, b, c, "COMMIT", "SET plpgsql.check_asserts TO off", "RESET ALL")
			checkFailure()
			plpgsqlError(t, p, c, "SET plpgsql.check_asserts TO 'invalid'", false, fixture.ControlsReferenceDiagnostic("22023", "parameter \"plpgsql.check_asserts\" requires a Boolean value"))
		})
	})
	t.Run("multiline_utf8", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn) {
			want := fixture.ControlsReferenceDiagnostic("P0004", "échec")
			want.Context = "PL/pgSQL function inline_code_block line 4 at ASSERT"
			plpgsqlError(t, p, c, "DO $$\nBEGIN\n-- é\nASSERT false,'échec';\nEND$$", false, want)
		})
	})
}
