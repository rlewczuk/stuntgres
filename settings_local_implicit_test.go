package stuntgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func settingsLocalNotices(t *testing.T) (*sql.Conn, *[]Error) {
	t.Helper()
	raw, err := NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	notices := new([]Error)
	db := sql.OpenDB(raw.(*Connector).WithNoticeHandler(func(n Error) { *notices = append(*notices, n) }))
	t.Cleanup(func() { db.Close() })
	c, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, notices
}
func TestSettingsLocalImplicit(t *testing.T) {
	c, notices := settingsLocalNotices(t)
	ctx := context.Background()
	settingsLocalExec(t, c, "SET application_name='b'", false)
	s, err := c.PrepareContext(ctx, "SET LOCAL new.implicit='x'")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if len(*notices) != 0 {
		t.Fatal("Prepare warned", *notices)
	}
	_, err = c.ExecContext(ctx, "SHOW new.implicit")
	settingsDiagnostic(t, err, "42704", `unrecognized configuration parameter "new.implicit"`, "", "", "")
	if _, err = s.ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	want := Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"}
	if len(*notices) != 1 || (*notices)[0] != want {
		t.Fatal(*notices)
	}
	settingsRows(t, c, "SHOW new.implicit", false, "new.implicit", "")
	for _, prepared := range []bool{false, true} {
		settingsLocalExec(t, c, "SET LOCAL application_name='l'", prepared)
		settingsRows(t, c, "SHOW application_name", prepared, "application_name", "b")
		settingsRows(t, c, "SELECT set_config('application_name','transient',true)", prepared, "set_config", "transient")
		settingsRows(t, c, "SHOW application_name", prepared, "application_name", "b")
	}
	before := len(*notices)
	settingsRows(t, c, "SELECT set_config('new.function','transient',true)", true, "set_config", "transient")
	if len(*notices) != before {
		t.Fatal("function SQL warning")
	}
	settingsRows(t, c, "SHOW new.function", false, "new.function", "")
	settingsLocalExec(t, c, "SET client_min_messages=error", false)
	before = len(*notices)
	settingsLocalExec(t, c, "SET LOCAL client_min_messages=notice", false)
	if len(*notices) != before {
		t.Fatal("warning ignored prior threshold")
	}
	settingsRows(t, c, "SHOW client_min_messages", false, "client_min_messages", "error")
	settingsLocalExec(t, c, "RESET client_min_messages", false)
	original := strings.Repeat("a", 64)
	clipped := strings.Repeat("a", 63)
	before = len(*notices)
	settingsLocalExec(t, c, "SET LOCAL application_name='"+original+"'", true)
	truncation := Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + original + `" will be truncated to "` + clipped + `"`}
	if len(*notices) != before+2 || (*notices)[before] != want || (*notices)[before+1] != truncation {
		t.Fatal("normalization order", *notices)
	}
	settingsRows(t, c, "SHOW application_name", false, "application_name", "b")
	before = len(*notices)
	settingsRows(t, c, "SELECT set_config('application_name',$1,true)", true, "set_config", clipped, original)
	if len(*notices) != before+1 || (*notices)[before] != truncation {
		t.Fatal("transient function normalization", *notices)
	}
	settingsRows(t, c, "SHOW application_name", false, "application_name", "b")
}

func TestSettingsLocalDiagnostics(t *testing.T) {
	c, notices := settingsLocalNotices(t)
	ctx := context.Background()
	for _, tc := range []struct{ sql, code, message, hint, detail string }{
		{"SET LOCAL absent_setting='x'", "42704", `unrecognized configuration parameter "absent_setting"`, "", ""},
		{"SET LOCAL application_name TO 'a','b'", "22023", "SET application_name takes only one argument", "", ""},
		{"SET LOCAL absent_setting TO 'a','b'", "22023", "SET absent_setting takes only one argument", "", ""},
		{"SET LOCAL server_encoding='UTF8'", "55P02", `parameter "server_encoding" cannot be changed`, "", ""},
		{"SET LOCAL log_statement_stats='bogus'", "42501", `permission denied to set parameter "log_statement_stats"`, "", ""},
		{"SET LOCAL standard_conforming_strings='bogus'", "22023", `parameter "standard_conforming_strings" requires a Boolean value`, "", ""},
		{"SET LOCAL client_min_messages='bogus'", "22023", `invalid value for parameter "client_min_messages": "bogus"`, "Available values: debug5, debug4, debug3, debug2, debug1, log, notice, warning, error.", ""},
		{"SET LOCAL statement_timeout='-1'", "22023", `-1 ms is outside the valid range for parameter "statement_timeout" (0 ms .. 2147483647 ms)`, "", ""},
	} {
		for _, prepared := range []bool{false, true} {
			before := len(*notices)
			var err error
			if prepared {
				s, e := c.PrepareContext(ctx, tc.sql)
				if e != nil {
					t.Fatal("semantic Prepare failed", e)
				}
				if len(*notices) != before {
					t.Fatal("Prepare warned")
				}
				_, err = s.ExecContext(ctx)
				s.Close()
			} else {
				_, err = c.ExecContext(ctx, tc.sql)
			}
			settingsDiagnostic(t, err, tc.code, tc.message, tc.hint, tc.detail, "")
			want := Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"}
			if len(*notices) != before+1 || (*notices)[before] != want {
				t.Fatal("warning missing before error", *notices)
			}
		}
	}
	before := len(*notices)
	_, err := c.ExecContext(ctx, "SET LOCAL application_name TO")
	settingsDiagnostic(t, err, "42601", "syntax error at end of input", "", "", "30")
	if len(*notices) != before {
		t.Fatal("syntax warning")
	}
	settingsLocalExec(t, c, "SET application_name='b'", false)
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	settingsLocalExec(t, tx, "SET application_name='s'", false)
	settingsLocalExec(t, tx, "SET LOCAL application_name='l'", false)
	_, err = tx.ExecContext(ctx, "SET absent_setting='x'")
	settingsDiagnostic(t, err, "42704", `unrecognized configuration parameter "absent_setting"`, "", "", "")
	_, err = tx.ExecContext(ctx, "SHOW application_name")
	settingsDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "", "", "")
	err = tx.Commit()
	if err == nil || err.Error() != "pq: could not complete operation in a failed transaction" {
		t.Fatal("aborted Commit must report the ordinary failed-transaction error", err)
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		t.Fatal("aborted Commit fabricated a SQLSTATE", err)
	}
	settingsRows(t, c, "SHOW application_name", false, "application_name", "b")
}

func TestSettingsLocalAliases(t *testing.T) {
	a, _ := settingsLocalConn(t)
	ctx := context.Background()
	for _, q := range []string{"CREATE SCHEMA local_path", "CREATE TABLE local_path.t(v TEXT)", "INSERT INTO local_path.t VALUES('local')", "CREATE TABLE public.t(v TEXT)", "INSERT INTO public.t VALUES('public')"} {
		_, err := a.ExecContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
	}
	for _, commit := range []bool{false, true} {
		tx, err := a.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		settingsLocalExec(t, tx, "SET LOCAL SCHEMA 'local_path'", true)
		settingsLocalRead(t, tx, "SELECT current_schema", "local_path", false)
		settingsLocalRead(t, tx, "SELECT v FROM t", "local", false)
		if commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
		settingsLocalRead(t, a, "SELECT v FROM t", "public", false)
	}
	for _, tc := range []struct{ q, want string }{{"SET SCHEMA 'Mixed Name'", `"Mixed Name"`}, {"SET SESSION SCHEMA 'a,b'", `"a,b"`}, {"SET SCHEMA 'a\"b'", `"a""b"`}, {"SET SCHEMA ''", `""`}} {
		settingsLocalExec(t, a, tc.q, false)
		settingsRows(t, a, "SHOW search_path", false, "search_path", tc.want)
	}
}

func TestSettingsLocalConsumers(t *testing.T) {
	a, b := settingsLocalConn(t)
	ctx := context.Background()
	settingsLocalExec(t, a, "SET demo.literal='b'", false)
	tx, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Prepare on this Tx before LOCAL: Tx.Stmt can reprepare a Conn handle.
	prepared, err := tx.PrepareContext(ctx, `SELECT set_config('demo.literal','a\nb',false)`)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	settingsLocalExec(t, tx, "SET LOCAL standard_conforming_strings=off", false)
	settingsLocalRead(t, tx, `SELECT set_config('application_name','a\nb',false)`, `a\x0ab`, false)
	// A dotted string has no application_name control-byte normalization.
	settingsLocalRead(t, tx, `SELECT set_config('demo.literal','a\nb',false)`, "a\nb", false)
	rows, err := prepared.QueryContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got string
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	if err = rows.Scan(&got); err != nil || got != `a\nb` {
		t.Fatal(got, err)
	}
	rows.Close()
	prepared.Close()
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	settingsRows(t, a, "SHOW standard_conforming_strings", false, "standard_conforming_strings", "on")
	settingsRows(t, a, "SHOW application_name", false, "application_name", "")
	settingsRows(t, a, "SHOW demo.literal", false, "demo.literal", "b")
	settingsLocalRead(t, a, `SELECT set_config('demo.literal','a\nb',true)`, `a\nb`, false)
	settingsRows(t, a, "SHOW demo.literal", false, "demo.literal", "b")
	settingsLocalExec(t, a, "CREATE TABLE timeout_local(v TEXT)", false)
	blocker, err := b.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	settingsLocalExec(t, blocker, "LOCK TABLE timeout_local IN ACCESS EXCLUSIVE MODE", false)
	tx, err = a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	settingsLocalExec(t, tx, "SET LOCAL statement_timeout='1ms'", false)
	_, err = tx.ExecContext(ctx, "SELECT v FROM timeout_local")
	settingsDiagnostic(t, err, "57014", "canceling statement due to statement timeout", "", "", "")
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	settingsRows(t, a, "SHOW statement_timeout", false, "statement_timeout", "0")
	settingsRows(t, a, "SHOW application_name", false, "application_name", "")
}
