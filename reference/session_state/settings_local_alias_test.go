package reference_test

import (
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSettingsLocalDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		postgres := backend.PG
		var notices []fixture.Diagnostic
		_, c, record := fixture.NoticeReferencePool(t, p, postgres, &notices)
		execute := func(step fixture.Step, prepared bool) {
			t.Helper()
			if !prepared {
				fixture.RunStep(t, p.Ctx, c, record, step)
				return
			}
			before := len(notices)
			statement, err := c.PrepareContext(p.Ctx, step.Query)
			if err != nil {
				t.Fatal("semantic Prepare failed", err)
			}
			defer statement.Close()
			if len(notices) != before {
				t.Fatal("Prepare warned", notices[before:])
			}
			held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: step.Query}
			fixture.RunStep(t, p.Ctx, held, record, step)
			if err := statement.Close(); err != nil {
				t.Fatal(err)
			}
		}
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ sql, code, message, hint string }{
				{"SET LOCAL absent_setting='x'", "42704", `unrecognized configuration parameter "absent_setting"`, ""},
				{"SET LOCAL application_name TO 'a','b'", "22023", "SET application_name takes only one argument", ""},
				{"SET LOCAL absent_setting TO 'a','b'", "22023", "SET absent_setting takes only one argument", ""},
				{"SET LOCAL server_encoding='UTF8'", "55P02", `parameter "server_encoding" cannot be changed`, ""},
				{"SET LOCAL standard_conforming_strings='bogus'", "22023", `parameter "standard_conforming_strings" requires a Boolean value`, ""},
				{"SET LOCAL client_min_messages='bogus'", "22023", `invalid value for parameter "client_min_messages": "bogus"`, "Available values: debug5, debug4, debug3, debug2, debug1, log, notice, warning, error."},
				{"SET LOCAL statement_timeout='-1'", "22023", `-1 ms is outside the valid range for parameter "statement_timeout" (0 ms .. 2147483647 ms)`, ""},
				{"SET LOCAL statement_timeout='1 MS'", "22023", `invalid value for parameter "statement_timeout": "1 MS"`, `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`},
				{"SET LOCAL statement_timeout='2147483648'", "22023", `invalid value for parameter "statement_timeout": "2147483648"`, "Value exceeds integer range."},
			} {
				before := len(notices)
				step := settingsReferenceError(tc.sql, tc.code, tc.message, tc.hint, "")
				execute(step, prepared)
				want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"}
				if len(notices) != before+1 || notices[before] != want {
					t.Fatal("warning ordering", notices)
				}
			}
		}
		if !postgres {
			for _, prepared := range []bool{false, true} {
				before := len(notices)
				step := settingsReferenceError("SET LOCAL log_statement_stats='bogus'", "42501", `permission denied to set parameter "log_statement_stats"`, "", "")
				execute(step, prepared)
				want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"}
				if len(notices) != before+1 || notices[before] != want {
					t.Fatal(notices[before:])
				}
			}
		}
		before := len(notices)
		for _, prepared := range []bool{false, true} {
			step := fixture.ErrorDiagnostic("SET LOCAL application_name TO", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "syntax error at end of input", Position: "30"})
			step.Prepared = prepared
			step.ErrorStage = "execute"
			if prepared {
				step.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, record, step)
			if len(notices) != before {
				t.Fatal("syntax emitted warning", notices[before:])
			}
		}
		fixture.RunStep(t, p.Ctx, c, record, fixture.Exec("SET application_name='b'", 0))
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, record, fixture.Exec("SET application_name='s'", 0))
		fixture.RunStep(t, p.Ctx, tx, record, fixture.Exec("SET LOCAL application_name='l'", 0))
		fixture.RunStep(t, p.Ctx, tx, record, settingsReferenceError("SET absent_setting='x'", "42704", `unrecognized configuration parameter "absent_setting"`, "", ""))
		fixture.RunStep(t, p.Ctx, tx, record, settingsReferenceError("SHOW application_name", "25P02", "current transaction is aborted, commands ignored until end of transaction block", "", ""))
		fixture.RunStep(t, p.Ctx, tx, record, settingsReferenceError("SHOW application_name", "25P02", "current transaction is aborted, commands ignored until end of transaction block", "", ""))
		err = tx.Commit()
		if err == nil || err.Error() != "pq: could not complete operation in a failed transaction" {
			t.Fatal("aborted Commit must report the ordinary failed-transaction error", err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("aborted Commit fabricated a SQLSTATE", err)
		}
		fixture.RunStep(t, p.Ctx, c, record, localReferenceSelect(backend.PG, "SHOW application_name", "application_name", "b"))
		fixture.RunStep(t, p.Ctx, c, record, fixture.Exec("RESET application_name", 0))
	}
}

func TestReferenceSettingsLocalAliases(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		c, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, backend.Recorder, s) }
		for _, q := range []string{"CREATE SCHEMA local_path", "CREATE TABLE local_path.t(v TEXT)", "CREATE TABLE public.t(v TEXT)"} {
			run(c, fixture.Exec(q, 0))
		}
		run(c, fixture.Exec("INSERT INTO local_path.t VALUES('local')", 1))
		run(c, fixture.Exec("INSERT INTO public.t VALUES('public')", 1))
		for _, commit := range []bool{false, true} {
			tx, err := c.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			run(tx, fixture.Prepared(fixture.Exec("SET LOCAL SCHEMA 'local_path'", 0)))
			run(tx, fixture.Select("SELECT current_schema", []string{"current_schema"}, []string{"NAME"}, [][]any{{"local_path"}}))
			run(tx, localReferenceSelect(backend.PG, "SELECT v FROM t", "v", "local"))
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			run(c, localReferenceSelect(backend.PG, "SELECT v FROM t", "v", "public"))
		}
		for _, tc := range []struct{ q, want string }{{"SET SCHEMA 'Mixed Name'", `"Mixed Name"`}, {"SET SESSION SCHEMA 'a,b'", `"a,b"`}, {"SET SCHEMA 'a\"b'", `"a""b"`}, {"SET SCHEMA ''", `""`}} {
			run(c, fixture.Exec(tc.q, 0))
			run(c, localReferenceSelect(backend.PG, "SHOW search_path", "search_path", tc.want))
		}
		for _, tc := range []struct{ q, token string }{{"SET SCHEMA public", "public"}, {"SET SCHEMA DEFAULT", "DEFAULT"}} {
			s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "` + tc.token + `"`, Position: "12"})
			run(c, s)
		}
		run(c, fixture.Exec("RESET search_path", 0))
		run(c, fixture.Exec("DROP SCHEMA local_path CASCADE", 0))
	}
}

func TestReferenceSettingsLocalConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		for _, baseline := range []string{"", "b"} {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, backend.Recorder, s) }
			run(c, fixture.Exec("SET application_name='"+baseline+"'", 0))
			run(c, fixture.Exec("SET demo.literal='b'", 0))
			tx, err := c.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			// Keep the handle on its preparing Tx; Tx.Stmt may reprepare a Conn handle.
			captured, err := tx.PrepareContext(p.Ctx, `SELECT set_config('demo.literal','a\nb',false)`)
			if err != nil {
				t.Fatal(err)
			}
			defer captured.Close()
			run(tx, fixture.Exec("SET LOCAL standard_conforming_strings=off", 0))
			run(tx, localReferenceSelect(backend.PG, `SELECT set_config('application_name','a\nb',false)`, "set_config", `a\x0ab`))
			// Unlike application_name, this string does not normalize control bytes.
			run(tx, localReferenceSelect(backend.PG, `SELECT set_config('demo.literal','a\nb',false)`, "set_config", "a\nb"))
			rows, err := captured.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var value string
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			if err = rows.Scan(&value); err != nil || value != `a\nb` {
				t.Fatal(value, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("captured EOF", rows.Err())
			}
			rows.Close()
			captured.Close()
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(c, localReferenceSelect(backend.PG, "SHOW standard_conforming_strings", "standard_conforming_strings", "on"))
			run(c, localReferenceSelect(backend.PG, "SHOW application_name", "application_name", baseline))
			run(c, localReferenceSelect(backend.PG, "SHOW demo.literal", "demo.literal", "b"))
			run(c, localReferenceSelect(backend.PG, `SELECT set_config('demo.literal','a\nb',true)`, "set_config", `a\nb`))
			run(c, localReferenceSelect(backend.PG, "SHOW demo.literal", "demo.literal", "b"))
			run(c, fixture.Exec("CREATE TABLE timeout_local(v TEXT)", 0))
			peer, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			blocker, err := peer.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback()
			run(blocker, fixture.Exec("LOCK TABLE timeout_local IN ACCESS EXCLUSIVE MODE", 0))
			tx, err = c.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			run(tx, fixture.Exec("SET LOCAL statement_timeout='1ms'", 0))
			run(tx, settingsReferenceError("SELECT v FROM timeout_local", "57014", "canceling statement due to statement timeout", "", ""))
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err = blocker.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(c, localReferenceSelect(backend.PG, "SHOW statement_timeout", "statement_timeout", "0"))
			run(c, localReferenceSelect(backend.PG, "SHOW application_name", "application_name", baseline))
			run(c, fixture.Exec("DROP TABLE timeout_local", 0))
		}
	}
}
