//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"testing"
)

func TestReferenceSettingsLocalDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	for _, postgres := range []bool{false, true} {
		var notices []referenceDiagnostic
		_, c, record := noticeReferencePool(t, p, postgres, &notices)
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
				step.prepared = prepared
				referenceRunStep(t, p.ctx, c, record, step)
				want := referenceDiagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"}
				if len(notices) != before+1 || notices[before] != want {
					t.Fatal("warning ordering", notices)
				}
			}
		}
		referenceRunStep(t, p.ctx, c, record, referenceExec("SET application_name='b'", 0))
		tx, err := c.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		referenceRunStep(t, p.ctx, tx, record, referenceExec("SET application_name='s'", 0))
		referenceRunStep(t, p.ctx, tx, record, referenceExec("SET LOCAL application_name='l'", 0))
		referenceRunStep(t, p.ctx, tx, record, settingsReferenceError("SET absent_setting='x'", "42704", `unrecognized configuration parameter "absent_setting"`, "", ""))
		referenceRunStep(t, p.ctx, tx, record, settingsReferenceError("SHOW application_name", "25P02", "current transaction is aborted, commands ignored until end of transaction block", "", ""))
		err = tx.Commit()
		if err == nil || err.Error() != "pq: could not complete operation in a failed transaction" {
			t.Fatal("aborted Commit must report the ordinary failed-transaction error", err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("aborted Commit fabricated a SQLSTATE", err)
		}
		referenceRunStep(t, p.ctx, c, record, localReferenceSelect("SHOW application_name", "application_name", "b"))
		referenceRunStep(t, p.ctx, c, record, referenceExec("RESET application_name", 0))
	}
	// Access precedes parsing under an ordinary PostgreSQL role.
	if _, err := referenceAdmin.ExecContext(p.ctx, "CREATE ROLE local_settings_reader LOGIN PASSWORD 'local_settings_reader'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := referenceAdmin.ExecContext(context.Background(), "DROP ROLE local_settings_reader"); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(referenceDSN)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("local_settings_reader", "local_settings_reader")
	db, err := sql.Open("stuntgres_reference_postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, prepared := range []bool{false, true} {
		s := settingsReferenceError("SET LOCAL log_statement_stats='bogus'", "42501", `permission denied to set parameter "log_statement_stats"`, "", "")
		s.prepared = prepared
		referenceRunStep(t, p.ctx, db, p.postgresRecord, s)
	}
}

func TestReferenceSettingsLocalAliases(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		db     *sql.DB
		record *referenceRecorder
	}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
		c, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		run := func(e referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, e, backend.record, s) }
		for _, q := range []string{"CREATE SCHEMA local_path", "CREATE TABLE local_path.t(v TEXT)", "CREATE TABLE public.t(v TEXT)"} {
			run(c, referenceExec(q, 0))
		}
		run(c, referenceExec("INSERT INTO local_path.t VALUES('local')", 1))
		run(c, referenceExec("INSERT INTO public.t VALUES('public')", 1))
		for _, commit := range []bool{false, true} {
			tx, err := c.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			run(tx, referencePrepared(referenceExec("SET LOCAL SCHEMA 'local_path'", 0)))
			run(tx, referenceSelect("SELECT current_schema", []string{"current_schema"}, []string{"NAME"}, [][]any{{"local_path"}}))
			run(tx, localReferenceSelect("SELECT v FROM t", "v", "local"))
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			run(c, localReferenceSelect("SELECT v FROM t", "v", "public"))
		}
		for _, tc := range []struct{ q, want string }{{"SET SCHEMA 'Mixed Name'", `"Mixed Name"`}, {"SET SESSION SCHEMA 'a,b'", `"a,b"`}, {"SET SCHEMA 'a\"b'", `"a""b"`}, {"SET SCHEMA ''", `""`}} {
			run(c, referenceExec(tc.q, 0))
			run(c, localReferenceSelect("SHOW search_path", "search_path", tc.want))
		}
		for _, tc := range []struct{ q, token string }{{"SET SCHEMA public", "public"}, {"SET SCHEMA DEFAULT", "DEFAULT"}} {
			s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "42601", Message: `syntax error at or near "` + tc.token + `"`, Position: "12"})
			run(c, s)
		}
		run(c, referenceExec("RESET search_path", 0))
		run(c, referenceExec("DROP SCHEMA local_path CASCADE", 0))
	}
}

func TestReferenceSettingsLocalConsumers(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		db     *sql.DB
		record *referenceRecorder
	}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
		c, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		run := func(e referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, e, backend.record, s) }
		run(c, referenceExec("SET application_name='b'", 0))
		run(c, referenceExec("SET demo.literal='b'", 0))
		tx, err := c.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		// Keep the handle on its preparing Tx; Tx.Stmt may reprepare a Conn handle.
		captured, err := tx.PrepareContext(p.ctx, `SELECT set_config('demo.literal','a\nb',false)`)
		if err != nil {
			t.Fatal(err)
		}
		defer captured.Close()
		run(tx, referenceExec("SET LOCAL standard_conforming_strings=off", 0))
		run(tx, localReferenceSelect(`SELECT set_config('application_name','a\nb',false)`, "set_config", `a\x0ab`))
		// Unlike application_name, this string does not normalize control bytes.
		run(tx, localReferenceSelect(`SELECT set_config('demo.literal','a\nb',false)`, "set_config", "a\nb"))
		rows, err := captured.QueryContext(p.ctx)
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
		rows.Close()
		captured.Close()
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(c, localReferenceSelect("SHOW standard_conforming_strings", "standard_conforming_strings", "on"))
		run(c, localReferenceSelect("SHOW application_name", "application_name", "b"))
		run(c, localReferenceSelect("SHOW demo.literal", "demo.literal", "b"))
		run(c, localReferenceSelect(`SELECT set_config('demo.literal','a\nb',true)`, "set_config", `a\nb`))
		run(c, localReferenceSelect("SHOW demo.literal", "demo.literal", "b"))
		run(c, referenceExec("CREATE TABLE timeout_local(v TEXT)", 0))
		peer, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		blocker, err := peer.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer blocker.Rollback()
		run(blocker, referenceExec("LOCK TABLE timeout_local IN ACCESS EXCLUSIVE MODE", 0))
		tx, err = c.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		run(tx, referenceExec("SET LOCAL statement_timeout='1ms'", 0))
		run(tx, settingsReferenceError("SELECT v FROM timeout_local", "57014", "canceling statement due to statement timeout", "", ""))
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if err = blocker.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(c, localReferenceSelect("SHOW statement_timeout", "statement_timeout", "0"))
		run(c, localReferenceSelect("SHOW application_name", "application_name", "b"))
	}
}
