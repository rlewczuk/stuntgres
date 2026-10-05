package stuntgres

import (
	"context"
	"database/sql"
	"testing"
)

type settingsLocalExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	PrepareContext(context.Context, string) (*sql.Stmt, error)
}

func settingsLocalExec(t *testing.T, e settingsLocalExecutor, q string, prepared bool, args ...any) {
	t.Helper()
	ctx := context.Background()
	var r sql.Result
	var err error
	if prepared {
		s, x := e.PrepareContext(ctx, q)
		if x != nil {
			t.Fatal(q, x)
		}
		defer s.Close()
		r, err = s.ExecContext(ctx, args...)
	} else {
		r, err = e.ExecContext(ctx, q, args...)
	}
	if err != nil {
		t.Fatal(q, err)
	}
	n, err := r.RowsAffected()
	want := int64(0)
	if len(q) >= 6 && q[:6] == "SELECT" {
		want = 1
	}
	if err != nil || n != want {
		t.Fatal(q, n, err)
	}
}
func settingsLocalRead(t *testing.T, e settingsLocalExecutor, q, want string, prepared bool, args ...any) {
	t.Helper()
	ctx := context.Background()
	var rows *sql.Rows
	var err error
	if prepared {
		s, x := e.PrepareContext(ctx, q)
		if x != nil {
			t.Fatal(q, x)
		}
		defer s.Close()
		rows, err = s.QueryContext(ctx, args...)
	} else {
		rows, err = e.QueryContext(ctx, q, args...)
	}
	if err != nil {
		t.Fatal(q, err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal(q, "no row", rows.Err())
	}
	var got string
	if err = rows.Scan(&got); err != nil || got != want {
		t.Fatalf("%s got %q want %q err %v", q, got, want, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(q, rows.Err())
	}
}
func settingsLocalConn(t *testing.T) (*sql.Conn, *sql.Conn) {
	t.Helper()
	db := sessionPool(t, settingsTestDriver(t), t.Name())
	a, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}
func TestSettingsLocalTransactions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			for _, tc := range []struct {
				name         string
				sql          []string
				visible, end string
			}{
				{"Session", []string{"SET SESSION application_name='s'"}, "s", "s"},
				{"Local", []string{"SET LOCAL application_name='l'"}, "l", "b"},
				{"SessionLocal", []string{"SET application_name='s'", "SET LOCAL application_name='l'"}, "l", "s"},
				{"LocalSession", []string{"SET LOCAL application_name='l'", "SET application_name='s'"}, "s", "s"},
				{"Repeated", []string{"SET application_name='s'", "SET LOCAL application_name='l'", "SET application_name='t'", "SET LOCAL application_name='u'"}, "u", "t"},
				{"DefaultAll", []string{"SET application_name='s'", "SET LOCAL application_name=DEFAULT", "RESET ALL"}, "", "s"},
				{"ExplicitDefaultAll", []string{"SET application_name='s'", "SET LOCAL application_name=''", "RESET ALL"}, "", ""},
				{"LocalReset", []string{"SET LOCAL application_name='l'", "RESET application_name"}, "", ""},
				{"ResetLocal", []string{"SET application_name='s'", "RESET application_name", "SET LOCAL application_name='l'"}, "l", ""},
				{"Function", []string{"SELECT set_config('application_name','s',false)", "SELECT set_config('application_name','l',true)"}, "l", "s"},
				{"FunctionNull", []string{"SET LOCAL application_name='l'", "SELECT set_config('application_name',NULL,NULL)"}, "", ""},
			} {
				t.Run(tc.name+map[bool]string{true: "/commit", false: "/rollback"}[commit]+map[bool]string{true: "/prepared", false: "/direct"}[prepared], func(t *testing.T) {
					a, b := settingsLocalConn(t)
					settingsLocalExec(t, a, "SET application_name='b'", false)
					tx, err := a.BeginTx(context.Background(), nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					for _, q := range tc.sql {
						settingsLocalExec(t, tx, q, prepared)
					}
					settingsLocalRead(t, tx, "SHOW application_name", tc.visible, prepared)
					settingsLocalRead(t, tx, "SELECT current_setting('application_name')", tc.visible, prepared)
					settingsLocalRead(t, b, "SHOW application_name", "", false)
					if commit {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					want := tc.end
					if !commit {
						want = "b"
					}
					settingsRows(t, a, "SHOW application_name", prepared, "application_name", want)
				})
			}
		}
	}
}

func TestSettingsLocalDefaultsAndIsolation(t *testing.T) {
	a, b := settingsLocalConn(t)
	for _, q := range []string{"SET demo.flag=on", "SET demo.text='session'", "SET demo.count=3", "SET demo.ratio='2.5'", "SET demo.mode=fast"} {
		settingsLocalExec(t, a, q, false)
	}
	tx, err := a.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, q := range []string{"SET LOCAL demo.flag=off", "SET LOCAL demo.text=''", "SET LOCAL demo.count=4", "SET LOCAL demo.ratio='3.5'", "SET LOCAL demo.mode=safe", "SET new.placeholder='recognized'"} {
		settingsLocalExec(t, tx, q, true)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, want string }{{"demo.flag", "on"}, {"demo.text", "session"}, {"demo.count", "3"}, {"demo.ratio", "2.5"}, {"demo.mode", "fast"}, {"new.placeholder", ""}} {
		settingsRows(t, a, "SHOW "+tc.name, false, tc.name, tc.want)
	}
	_, err = b.ExecContext(context.Background(), "SHOW new.placeholder")
	settingsDiagnostic(t, err, "42704", `unrecognized configuration parameter "new.placeholder"`, "", "", "")
	settingsLocalExec(t, a, "RESET ALL", true)
	settingsRows(t, a, "SHOW demo.text", false, "demo.text", "seed")
}
