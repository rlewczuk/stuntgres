package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSmokeAlterRenameSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			m, db, ctx, notices := fixture.MigrationAlterFixture(t, p, backend)
			exec := func(q string, count int64, args ...any) {
				t.Helper()
				s := fixture.Exec(q, count, args...)
				s.NonContext = true
				fixture.RunStep(t, ctx, db, m.Recorder, s)
			}
			prepare := func(q string) *sql.Stmt {
				s, err := db.Prepare(q)
				if err != nil {
					t.Fatal(q, err)
				}
				t.Cleanup(func() { s.Close() })
				return s
			}
			exec("CREATE TABLE t(a INT CONSTRAINT nn NOT NULL,b TEXT,c BIGINT)", 0)
			exec("INSERT INTO t VALUES(7,'O''Brien',9),(8,NULL,10)", 2)
			exec("COMMENT ON TABLE t IS 'table comment'", 0)
			exec("COMMENT ON COLUMN t.a IS 'column comment'", 0)
			star, named, other := prepare("SELECT * FROM t"), prepare("SELECT a FROM t"), prepare("SELECT b FROM t")
			ordered := prepare("INSERT INTO t VALUES($1,$2,$3)")
			var held *sql.Rows
			var err error
			if !backend.PG {
				held, err = db.Query("SELECT * FROM t ORDER BY a")
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
			}
			utility := prepare("ALTER TABLE t RENAME COLUMN a TO renamed")
			r, err := utility.Exec()
			if err != nil {
				t.Fatal(err)
			}
			if n, _ := r.RowsAffected(); n != 0 {
				t.Fatal(n)
			}
			initial := [][]any{{int64(7), "O'Brien", int64(9)}, {int64(8), nil, int64(10)}}
			if held != nil {
				m.NullRows(held, []string{"a", "b", "c"}, []string{"INT4", "TEXT", "INT8"}, []bool{false, true, true}, initial)
			}
			check := func(table string, want [][]any) {
				t.Helper()
				q := "SELECT * FROM " + table + " ORDER BY renamed"
				rows, err := db.Query(q)
				if err != nil {
					t.Fatal(err)
				}
				m.NullRows(rows, []string{"renamed", "b", "c"}, []string{"INT4", "TEXT", "INT8"}, []bool{false, true, true}, want)
				s := prepare(q)
				rows, err = s.Query()
				if err != nil {
					t.Fatal(err)
				}
				m.NullRows(rows, []string{"renamed", "b", "c"}, []string{"INT4", "TEXT", "INT8"}, []bool{false, true, true}, want)
			}
			check("t", initial)
			_, err = star.Query()
			m.AlterError(err, "0A000", "cached plan must not change result type", "")
			_, err = named.Query()
			m.AlterError(err, "42703", `column "a" does not exist`, "8")
			rows, err := other.Query()
			if err != nil {
				t.Fatal(err)
			}
			values := []any{}
			for rows.Next() {
				var value any
				if err = rows.Scan(&value); err != nil {
					t.Fatal(err)
				}
				values = append(values, value)
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			if !reflect.DeepEqual(values, []any{"O'Brien", nil}) && !reflect.DeepEqual(values, []any{nil, "O'Brien"}) {
				t.Fatal(values)
			}
			r, err = ordered.Exec(9, "tail", 11)
			if err != nil {
				t.Fatal(err)
			}
			if n, _ := r.RowsAffected(); n != 1 {
				t.Fatal(n)
			}
			fresh := prepare("INSERT INTO t(renamed,b,c) VALUES($1,$2,$3)")
			r, err = fresh.Exec(10, "fresh", 12)
			if err != nil {
				t.Fatal(err)
			}
			if n, _ := r.RowsAffected(); n != 1 {
				t.Fatal(n)
			}
			want := append(initial, []any{int64(9), "tail", int64(11)}, []any{int64(10), "fresh", int64(12)})
			var before bytes.Buffer
			if !backend.PG {
				if err = stuntgres.Dump(ctx, p.Native, &before); err != nil {
					t.Fatal(err)
				}
			}
			for _, tc := range []struct{ q, code, message string }{{"ALTER TABLE t RENAME renamed TO b", "42701", `column "b" of relation "t" already exists`}, {"ALTER TABLE t RENAME renamed TO renamed", "42701", `column "renamed" of relation "t" already exists`}, {"ALTER TABLE t RENAME TO t", "42P07", `relation "t" already exists`}} {
				_, err = db.Exec(tc.q)
				m.AlterError(err, tc.code, tc.message, "")
				check("t", want)
				if !backend.PG {
					var after bytes.Buffer
					if err = stuntgres.Dump(ctx, p.Native, &after); err != nil || after.String() != before.String() {
						t.Fatal("collision mutated dump", err)
					}
				}
			}
			utility = prepare("ALTER TABLE ONLY(t) RENAME TO newtable")
			r, err = utility.Exec()
			if err != nil {
				t.Fatal(err)
			}
			if n, _ := r.RowsAffected(); n != 0 {
				t.Fatal(n)
			}
			check("newtable", want)
			_, err = db.Query("SELECT * FROM t")
			m.AlterError(err, "42P01", `relation "t" does not exist`, "15")
			_, err = db.Query("SELECT a FROM newtable")
			m.AlterError(err, "42703", `column "a" does not exist`, "8")
			_, err = db.Exec("INSERT INTO newtable VALUES(NULL,'bad',13)")
			wantDiagnostic := fixture.ControlsReferenceDiagnostic("23502", `null value in column "renamed" of relation "newtable" violates not-null constraint`)
			wantDiagnostic.Detail, wantDiagnostic.Schema, wantDiagnostic.Table, wantDiagnostic.Column = "Failing row contains (null, bad, 13).", "public", "newtable", "renamed"
			fixture.AssertDiagnostic(t, err, &wantDiagnostic)
			for _, suffix := range []string{"RENAME a TO b", "RENAME TO u"} {
				*notices = nil
				exec("ALTER TABLE IF EXISTS missing "+suffix, 0)
				if !reflect.DeepEqual(*notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `relation "missing" does not exist, skipping`}}) {
					t.Fatal(notices)
				}
			}
			_, err = db.Exec("ALTER TABLE IF EXISTS newtable RENAME missing TO b")
			m.AlterError(err, "42703", `column "missing" does not exist`, "")
			before.Reset()
			if !backend.PG {
				if err = stuntgres.Dump(ctx, p.Native, &before); err != nil {
					t.Fatal(err)
				}
			}
			if !backend.PG {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				_, err = db.ExecContext(canceled, "ALTER TABLE newtable RENAME TO canceled")
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else {
				c, e := db.Conn(ctx)
				if e != nil {
					t.Fatal(e)
				}
				fixture.SessionCancel(t, ctx, c, false)
				if e = c.Close(); e != nil {
					t.Fatal(e)
				}
			}
			check("newtable", want)
			if !backend.PG {
				for _, fragment := range []string{`"newtable"`, `"renamed"`, `"nn"`, "table comment", "column comment", "O''Brien", "NULL"} {
					if !strings.Contains(before.String(), fragment) {
						t.Fatal(fragment, before.String())
					}
				}
				if strings.Contains(before.String(), "ALTER TABLE") {
					t.Fatal(before.String())
				}
				if err = stuntgres.Reset(ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err = stuntgres.Restore(ctx, p.Native, bytes.NewReader(before.Bytes())); err != nil {
					t.Fatal(err)
				}
				check("newtable", want)
				var after bytes.Buffer
				if err = stuntgres.Dump(ctx, p.Native, &after); err != nil || after.String() != before.String() {
					t.Fatal(err, after.String())
				}
				if err = stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE invalid(a INT);INSERT INTO invalid VALUES('bad');")); err == nil {
					t.Fatal("invalid restore succeeded")
				}
				after.Reset()
				if err = stuntgres.Dump(ctx, p.Native, &after); err != nil || after.String() != before.String() {
					t.Fatal("failed restore changed dump", err)
				}
			}
		})
	}
}
