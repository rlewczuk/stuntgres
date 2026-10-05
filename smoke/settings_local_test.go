package smoke_test

import (
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestSettingsLocalSmoke(t *testing.T) {
	ctx := context.Background()
	raw, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	var notices []stuntgres.Error
	db := sql.OpenDB(raw.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) { notices = append(notices, n) }))
	defer db.Close()
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	exec := func(t *testing.T, e interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	}, q string) {
		t.Helper()
		r, eerr := e.ExecContext(ctx, q)
		if eerr != nil {
			t.Fatal(q, eerr)
		}
		n, eerr := r.RowsAffected()
		if eerr != nil || n != 0 {
			t.Fatal(q, n, eerr)
		}
	}
	read := func(t *testing.T, e interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	}, q, want string) {
		t.Helper()
		var got string
		if eerr := e.QueryRowContext(ctx, q).Scan(&got); eerr != nil || got != want {
			t.Fatal(q, got, want, eerr)
		}
	}
	begin := func(t *testing.T) *sql.Tx {
		t.Helper()
		tx, eerr := a.BeginTx(ctx, nil)
		if eerr != nil {
			t.Fatal(eerr)
		}
		return tx
	}
	t.Run("Session", func(t *testing.T) {
		tx := begin(t)
		defer tx.Rollback()
		exec(t, tx, "SET SESSION application_name='s'")
		read(t, tx, "SHOW application_name", "s")
		if e := tx.Commit(); e != nil {
			t.Fatal(e)
		}
		read(t, a, "SHOW application_name", "s")
	})
	t.Run("LocalCommitRollback", func(t *testing.T) {
		for _, commit := range []bool{false, true} {
			tx := begin(t)
			defer tx.Rollback()
			exec(t, tx, "SET application_name='candidate'")
			exec(t, tx, "SET LOCAL application_name='local'")
			read(t, tx, "SHOW application_name", "local")
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			want := "s"
			if commit {
				want = "candidate"
			}
			read(t, a, "SHOW application_name", want)
		}
	})
	t.Run("ResetAllOverlay", func(t *testing.T) {
		for _, local := range []string{"DEFAULT", "''"} {
			tx := begin(t)
			defer tx.Rollback()
			exec(t, tx, "SET application_name='s'")
			exec(t, tx, "SET LOCAL application_name="+local)
			exec(t, tx, "RESET ALL")
			read(t, tx, "SHOW application_name", "")
			if e := tx.Commit(); e != nil {
				t.Fatal(e)
			}
			want := "s"
			if local == "''" {
				want = ""
			}
			read(t, a, "SHOW application_name", want)
		}
	})
	t.Run("FunctionLocal", func(t *testing.T) {
		tx := begin(t)
		defer tx.Rollback()
		read(t, tx, "SELECT set_config('application_name','transient',true)", "transient")
		read(t, tx, "SELECT current_setting('application_name')", "transient")
		if e := tx.Commit(); e != nil {
			t.Fatal(e)
		}
		read(t, a, "SHOW application_name", "")
		before := len(notices)
		read(t, a, "SELECT set_config('application_name','outside',true)", "outside")
		read(t, a, "SHOW application_name", "")
		if len(notices) != before {
			t.Fatal("function warned")
		}
	})
	t.Run("Schema", func(t *testing.T) {
		exec(t, a, "CREATE SCHEMA local_path")
		exec(t, a, "CREATE TABLE local_path.t(v TEXT)")
		if _, e := a.ExecContext(ctx, "INSERT INTO local_path.t VALUES('local')"); e != nil {
			t.Fatal(e)
		}
		tx := begin(t)
		defer tx.Rollback()
		exec(t, tx, "SET LOCAL SCHEMA 'local_path'")
		read(t, tx, "SELECT current_schema", "local_path")
		read(t, tx, "SELECT v FROM t", "local")
		if e := tx.Rollback(); e != nil {
			t.Fatal(e)
		}
		read(t, a, "SELECT current_schema", "public")
	})
	t.Run("ImplicitWarning", func(t *testing.T) {
		before := len(notices)
		exec(t, a, "SET LOCAL application_name='ignored'")
		read(t, a, "SHOW application_name", "")
		want := stuntgres.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET LOCAL can only be used in transaction blocks"}
		if len(notices) != before+1 || notices[before] != want {
			t.Fatal(notices)
		}
		_, e := a.ExecContext(ctx, "SET LOCAL client_min_messages='bogus'")
		if check := settingsDiagnostic(e, "22023", `invalid value for parameter "client_min_messages": "bogus"`, "Available values: debug5, debug4, debug3, debug2, debug1, log, notice, warning, error."); check != nil {
			t.Fatal(check)
		}
		if len(notices) != before+2 {
			t.Fatal("warning missing before error")
		}
	})
	t.Run("ImplicitPlaceholder", func(t *testing.T) {
		s, e := a.PrepareContext(ctx, "SET LOCAL smoke.implicit='x'")
		if e != nil {
			t.Fatal(e)
		}
		before := len(notices)
		_, e = a.ExecContext(ctx, "SHOW smoke.implicit")
		if check := settingsDiagnostic(e, "42704", `unrecognized configuration parameter "smoke.implicit"`, ""); check != nil {
			t.Fatal(check)
		}
		if len(notices) != before {
			t.Fatal("Prepare warned")
		}
		if _, e = s.ExecContext(ctx); e != nil {
			t.Fatal(e)
		}
		s.Close()
		read(t, a, "SHOW smoke.implicit", "")
		_, e = b.ExecContext(ctx, "SHOW smoke.implicit")
		if check := settingsDiagnostic(e, "42704", `unrecognized configuration parameter "smoke.implicit"`, ""); check != nil {
			t.Fatal(check)
		}
	})
	t.Run("AbortRecovery", func(t *testing.T) {
		tx := begin(t)
		defer tx.Rollback()
		exec(t, tx, "SET application_name='s'")
		exec(t, tx, "SET LOCAL application_name='l'")
		_, e := tx.ExecContext(ctx, "SET absent_setting='x'")
		if check := settingsDiagnostic(e, "42704", `unrecognized configuration parameter "absent_setting"`, ""); check != nil {
			t.Fatal(check)
		}
		_, e = tx.ExecContext(ctx, "SHOW application_name")
		if check := settingsDiagnostic(e, "25P02", "current transaction is aborted, commands ignored until end of transaction block", ""); check != nil {
			t.Fatal(check)
		}
		if e = tx.Rollback(); e != nil {
			t.Fatal(e)
		}
		read(t, a, "SHOW application_name", "")
	})
	t.Run("Isolation", func(t *testing.T) {
		exec(t, a, "SET application_name='a'")
		read(t, b, "SHOW application_name", "")
		if e := settingsScalar(ctx, a, "SHOW application_name", "application_name", "a", true); e != nil {
			t.Fatal(e)
		}
	})
}
