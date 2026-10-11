package reference_test

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRowLockLifetime(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
			fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO t VALUES(1,10)", 1)
			tx, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			stmt, err := tx.PrepareContext(p.Ctx, "SELECT v FROM t FOR SHARE")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			fixture.LockExec(t, p.Ctx, b, "UPDATE t SET v=11", 1) // Prepare has no row hold.
			rows, err := stmt.QueryContext(p.Ctx)
			fixture.RowValues(t, fixture.RelationOutcome{Rows: rows, Err: err, RawBackend: backend.Recorder.RawBackend}, 11)
			stmt.Close()
			recorder := backend.Recorder
			if !backend.PG {
				bounded, cancel := context.WithTimeout(p.Ctx, 30*time.Millisecond)
				out := fixture.RowExecute(bounded, b, "UPDATE t SET v=12", false)
				cancel()
				if out.Result != nil || !errors.Is(out.Err, context.DeadlineExceeded) {
					t.Fatal("closed cursor/statement lost row grant", out.Result, out.Err)
				}
				var state interface{ SQLState() string }
				if errors.As(out.Err, &state) {
					t.Fatal("caller deadline fabricated SQLSTATE", out.Err)
				}
			}
			fixture.RunStep(t, p.Ctx, b, recorder, fixture.Exec("SET lock_timeout='20ms'", 0))
			_, err = b.ExecContext(p.Ctx, "UPDATE t SET v=12")
			referenceRowTimeoutDiagnostic(t, err, backend.PG, "55P03", "canceling statement due to lock timeout", "updating")
			fixture.RunStep(t, p.Ctx, b, recorder, fixture.Exec("RESET lock_timeout", 0))
			fixture.RunStep(t, p.Ctx, b, recorder, fixture.Exec("SET statement_timeout='20ms'", 0))
			_, err = b.ExecContext(p.Ctx, "SELECT v FROM t FOR UPDATE")
			referenceRowTimeoutDiagnostic(t, err, backend.PG, "57014", "canceling statement due to statement timeout", "locking")
			fixture.RunStep(t, p.Ctx, b, recorder, fixture.Exec("RESET statement_timeout", 0))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.LockExec(t, p.Ctx, b, "UPDATE t SET v=12", 1)
		})
	}
}
func TestReferenceRowLockJoinedIndependence(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
			for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "CREATE TABLE u(id INT)", "INSERT INTO t VALUES(1,10),(2,20)", "INSERT INTO u VALUES(1),(3)"} {
				if _, err := a.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			ap, bp := 0, 0
			if backend.PG {
				ap, bp = fixture.RowPIDs(t, p.Ctx, a, b)
			}
			for _, prepared := range []bool{false, true} {
				tx, err := a.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer tx.Rollback()
					fixture.RowValues(t, fixture.RowQuery(p.Ctx, tx, "SELECT t.v FROM t JOIN u USING(id) FOR SHARE", prepared, backend.Recorder.RawBackend), 10)
					fixture.RowAffected(t, p.Ctx, b, "UPDATE u SET id=3 WHERE id=3", prepared, 1)
					fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=20 WHERE id=2", prepared, 1)
					if backend.PG {
						ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
						defer cancel()
						done := fixture.RowAsync(t, ctx, b, "DELETE FROM u WHERE id=1", prepared, false)
						fixture.RowWait(t, ctx, bp, ap)
						tx.Rollback()
						outcome := fixture.LockResult(t, done)
						if outcome.Err != nil {
							t.Fatal(outcome.Err)
						}
						fixture.LockAffected(t, outcome.Result, 1)
						fixture.LockExec(t, p.Ctx, b, "INSERT INTO u VALUES(1)", 1)
					} else {
						ctx, cancel := context.WithTimeout(p.Ctx, 30*time.Millisecond)
						outcome := fixture.RowExecute(ctx, b, "DELETE FROM u WHERE id=1", prepared)
						cancel()
						if !errors.Is(outcome.Err, context.DeadlineExceeded) {
							t.Fatal(outcome.Err)
						}
						if outcome.Result != nil {
							t.Fatal("failed joined DELETE returned result")
						}
						var state interface{ SQLState() string }
						if errors.As(outcome.Err, &state) {
							t.Fatal("caller deadline fabricated SQLSTATE", outcome.Err)
						}
					}
				}()
				if !backend.PG {
					fixture.RowAffected(t, p.Ctx, b, "DELETE FROM u WHERE id=1", prepared, 1)
					fixture.RowAffected(t, p.Ctx, b, "INSERT INTO u VALUES(1)", prepared, 1)
				}
			}
		})
	}
}

func referenceRowTimeoutDiagnostic(t *testing.T, err error, postgres bool, code, message, action string) {
	t.Helper()
	got := fixture.ExtractDiagnostic(t, err)
	if postgres {
		pattern := `^while ` + action + ` tuple \([0-9]+,[1-9][0-9]*\) in relation "t"$`
		if !regexp.MustCompile(pattern).MatchString(got.Context) {
			t.Fatalf("PostgreSQL physical wait context: %#v", got)
		}
		got.Context = ""
	}
	want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wait diagnostic %#v want %#v", got, want)
	}
	fixture.State(t, err, code)
}
