package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"
)

func TestReferenceFormatEncodingPrivileges(t *testing.T) {
	roleReferenceCleanup(t, []string{"fe_encoding_role"})
	formatEncodingBackends(t, func(p *referencePair, b referenceBackend, c *sql.Conn) {
		var notices []referenceDiagnostic
		_, noticeConn, record := noticeReferencePool(t, p, b.pg, &notices)
		c, b.recorder = noticeConn, record
		run := func(s referenceStep) {
			t.Helper()
			alreadyFailed := t.Failed()
			defer func() {
				if !alreadyFailed && t.Failed() {
					t.Logf("privilege scenario failing query: %s", s.query)
				}
			}()
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		run(referenceExec("CREATE ROLE fe_encoding_role", 0))
		run(referenceExec("SET lc_messages TO 'POSIX'", 0))
		const q = "SET lc_messages TO 'C'"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(formatEncodingText("SHOW lc_messages", "lc_messages", "POSIX"))
		run(referenceExec("SET ROLE fe_encoding_role", 0))
		defer c.ExecContext(p.ctx, "RESET ROLE")
		held := referenceImplicitError(q, formatEncodingPermission())
		held.errorStage = "execute"
		referenceRunStep(t, p.ctx, referenceHeldStatement{statement: stmt, query: q}, b.recorder, held)
		for _, query := range []string{"SET lc_messages TO 'C'", "SET lc_messages TO 'stuntgres_missing_locale_18'", "SET lc_messages TO 'é🙂'", "SET lc_messages TO DEFAULT", "RESET lc_messages", "SELECT set_config('lc_messages','C',false)", "SELECT set_config('lc_messages','stuntgres_missing_locale_18',false)", "SELECT set_config('lc_messages','é🙂',false)"} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%v", query, prepared), func(t *testing.T) {
					step := referenceImplicitError(query, formatEncodingPermission())
					step.prepared, step.errorStage = prepared, "execute"
					referenceRunStep(t, p.ctx, c, b.recorder, step)
					if len(notices) != 0 {
						t.Fatalf("unauthorized locale validation published notices: %#v", notices)
					}
					referenceRunStep(t, p.ctx, c, b.recorder, formatEncodingText("SHOW lc_messages", "lc_messages", "POSIX"))
				})
			}
		}
		run(referenceExec("BEGIN", 0))
		run(referenceImplicitError("SET LOCAL lc_messages TO 'bad'", formatEncodingPermission()))
		run(referenceExec("ROLLBACK", 0))
		run(referenceExec("BEGIN", 0))
		run(referenceImplicitError("SELECT set_config('lc_messages','bad',true)", formatEncodingPermission()))
		run(referenceExec("ROLLBACK", 0))
		run(referenceExec("SET extra_float_digits TO -1", 0))
		// Pinned tests-2 observed ordinary-role RESET ALL clearing the prior
		// SUSET override despite individual SET/RESET remaining forbidden.
		run(referenceExec("RESET ALL", 0))
		run(formatEncodingText("SHOW lc_messages", "lc_messages", "C"))
		run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "1"))
		run(referenceImplicitError("SET log_statement_stats TO on", referenceDiagnostic{Code: "42501", Message: `permission denied to set parameter "log_statement_stats"`}))
		run(referenceExec("RESET ROLE", 0))
		referenceRunStep(t, p.ctx, referenceHeldStatement{statement: stmt, query: q}, b.recorder, referenceExec(q, 0))
		run(referenceExec("SET lc_messages TO 'POSIX'", 0))
		run(referenceExec("RESET ALL", 0))
		run(formatEncodingText("SHOW lc_messages", "lc_messages", "C"))
		if !b.pg {
			run(referenceImplicitError("SET log_statement_stats TO on", referenceDiagnostic{Code: "42501", Message: `permission denied to set parameter "log_statement_stats"`}))
		}
	})
}

func TestReferenceFormatEncodingScope(t *testing.T) {
	formatEncodingBackends(t, func(p *referencePair, b referenceBackend, a *sql.Conn) {
		other, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
		for _, tc := range []struct{ name, base, alternate string }{
			{"client_encoding", "UTF8", "UNICODE"}, {"lc_messages", "C", "POSIX"},
			{"lc_monetary", "C", "POSIX"}, {"lc_numeric", "C", "POSIX"},
			{"lc_time", "C", "POSIX"}, {"icu_validation_level", "warning", "notice"},
			{"extra_float_digits", "1", "-1"},
		} {
			t.Run("journal/"+tc.name, func(t *testing.T) {
				exec := func(q string) { referenceRunStep(t, p.ctx, a, b.recorder, referenceExec(q, 0)) }
				show := func(value string) {
					referenceRunStep(t, p.ctx, a, b.recorder, formatEncodingText("SHOW "+tc.name, tc.name, value))
				}
				exec("SET " + tc.name + " TO '" + tc.base + "'")
				exec("BEGIN")
				exec("SET SESSION " + tc.name + " TO '" + tc.alternate + "'")
				exec("ROLLBACK")
				show(tc.base)
				exec("BEGIN")
				exec("SET SESSION " + tc.name + " TO '" + tc.alternate + "'")
				exec("COMMIT")
				show(tc.alternate)
				referenceRunStep(t, p.ctx, other, b.recorder, formatEncodingText("SHOW "+tc.name, tc.name, tc.base))
				exec("BEGIN")
				exec("SET LOCAL " + tc.name + " TO '" + tc.base + "'")
				show(tc.base)
				exec("COMMIT")
				show(tc.alternate)
				exec("BEGIN")
				exec("SAVEPOINT setting_point")
				exec("RESET " + tc.name)
				show(tc.base)
				exec("ROLLBACK TO setting_point")
				show(tc.alternate)
				exec("RELEASE setting_point")
				exec("COMMIT")
				exec("RESET " + tc.name)
				show(tc.base)
			})
		}
		run(referenceExec("SET extra_float_digits TO 0", 0))
		referenceRunStep(t, p.ctx, other, b.recorder, formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "1"))
		const q = "SELECT '1.2345678901234567'::float8::text AS v"
		stmt, err := a.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{statement: stmt, query: q}
		run(referenceExec("SET extra_float_digits TO -1", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, formatEncodingText(q, "v", "1.2345678901235"))
		// A cursor holds TEXT captured before another physical session changes its
		// own environment; pq cannot execute new commands while a cursor is open.
		rows, err := stmt.QueryContext(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, other, b.recorder, referenceExec("SET extra_float_digits TO -15", 0))
		if !rows.Next() {
			t.Fatal("retained cursor missing row", rows.Err())
		}
		var retained string
		if err := rows.Scan(&retained); err != nil {
			t.Fatal(err)
		}
		if retained != "1.2345678901235" {
			t.Fatal("retained cursor", retained)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("retained EOF", rows.Err())
		}
		rows.Close()
		for _, finish := range []string{"COMMIT", "ROLLBACK"} {
			run(referenceExec("SET extra_float_digits TO 0", 0))
			run(referenceExec("BEGIN", 0))
			run(referenceExec("SET SESSION extra_float_digits TO 3", 0))
			run(referenceExec(finish, 0))
			want := "0"
			if finish == "COMMIT" {
				want = "3"
			}
			run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", want))
		}
		run(referenceExec("SET extra_float_digits TO 0", 0))
		run(referenceExec("BEGIN", 0))
		run(referenceExec("SET LOCAL extra_float_digits TO -1", 0))
		run(referenceExec("SAVEPOINT fe", 0))
		run(referenceExec("SET extra_float_digits TO -15", 0))
		run(referenceExec("SET LOCAL lc_numeric TO 'POSIX'", 0))
		run(referenceExec("ROLLBACK TO fe", 0))
		run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "-1"))
		run(formatEncodingText("SHOW lc_numeric", "lc_numeric", "C"))
		run(referenceExec("SET LOCAL extra_float_digits TO 3", 0))
		run(referenceExec("RELEASE fe", 0))
		run(referenceExec("COMMIT", 0))
		run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "0"))
		run(referenceExec("BEGIN", 0))
		run(referenceExec("RESET extra_float_digits", 0))
		run(referenceExec("ROLLBACK", 0))
		run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "0"))
		// Healthy borrower reuse versus replacement recapturing defaults.
		pool, owner, record := noticeReferencePool(t, p, b.pg, new([]referenceDiagnostic))
		pool.SetMaxOpenConns(1)
		pool.SetMaxIdleConns(1)
		referenceRunStep(t, p.ctx, owner, record, referenceExec("SET extra_float_digits TO -1", 0))
		if err := owner.Close(); err != nil {
			t.Fatal(err)
		}
		next, err := pool.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, next, record, formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "-1"))
		if err := next.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
			t.Fatal("retire", err)
		}
		next.Close()
		replacement, err := pool.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer replacement.Close()
		referenceRunStep(t, p.ctx, replacement, record, formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "1"))
	})
}

func TestReferenceFormatEncodingAtomicity(t *testing.T) {
	formatEncodingBackends(t, func(p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("SET extra_float_digits TO 0", 0))
		run(referenceExec("CREATE TABLE fe_atomic(id integer, v double precision)", 0))
		run(referenceExec("INSERT INTO fe_atomic VALUES(1,'1.2345678901234567')", 1))
		for _, query := range []string{"SET extra_float_digits TO 4", "SELECT set_config('extra_float_digits','4',false)"} {
			want := referenceDiagnostic{Code: "22023", Message: `4 is outside the valid range for parameter "extra_float_digits" (-15 .. 3)`}
			run(referenceImplicitError(query, want))
			run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "0"))
		}
		for _, prepared := range []bool{false, true} {
			step := referenceImplicitError("INSERT INTO fe_atomic VALUES(2,'2.5'),(3,'1e999')", referenceDiagnostic{Code: "22003", Message: `"1e999" is out of range for type double precision`, Position: "42"})
			step.prepared = prepared
			run(step)
			run(referenceSelect("SELECT id,v::text AS v FROM fe_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "1.23456789012346"}}))
		}
		run(referenceExec("CREATE TABLE fe_late(id integer, v double precision)", 0))
		run(referenceExec("INSERT INTO fe_late VALUES(1,'1.2345678901234567'),(2,'1e308')", 2))
		for _, prepared := range []bool{false, true} {
			step := referenceImplicitError("UPDATE fe_late SET v=v*10", referenceDiagnostic{Code: "22003", Message: "value out of range: overflow"})
			step.prepared, step.errorStage = prepared, "execute"
			run(step)
			run(referenceSelect("SELECT id,v::text AS v FROM fe_late ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "1.23456789012346"}, {int64(2), "1e+308"}}))
			run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "0"))
		}
		const q = "SELECT set_config('extra_float_digits',$1,false)"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{statement: stmt, query: q}
		referenceRunStep(t, p.ctx, held, b.recorder, formatEncodingText(q, "set_config", "-1", "-1"))
		bad := referenceImplicitError(q, referenceDiagnostic{Code: "22023", Message: `4 is outside the valid range for parameter "extra_float_digits" (-15 .. 3)`}, "4")
		bad.errorStage = "execute"
		referenceRunStep(t, p.ctx, held, b.recorder, bad)
		run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "-1"))
		referenceRunStep(t, p.ctx, held, b.recorder, formatEncodingText(q, "set_config", "3", "3"))
		// lib/pq may win a pre-canceled SET race and retire its transport; this
		// ordinary adapter guarantee is native-only, not a false PG SQLSTATE.
		if !b.pg {
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			for _, call := range []func() error{
				func() error { _, err := c.ExecContext(ctx, "SET extra_float_digits TO -15"); return err },
				func() error { _, err := stmt.ExecContext(ctx, "-15"); return err },
			} {
				err := call()
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("precanceled ordinary error=%T %v", err, err)
				}
			}
			run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "3"))
		}
		// Public server-side cancellation checks belong to the shared fixture and
		// preserve a usable session rather than relying on transport timing.
		if b.pg {
			referenceSessionCancel(t, p.ctx, c, false)
		}
		run(formatEncodingText("SHOW extra_float_digits", "extra_float_digits", "3"))
	})
}
