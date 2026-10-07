//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"regexp"
	"testing"
	"time"
)

func TestReferenceRowLockLifetime(t *testing.T) {
	p := newReferencePair(t)
	for backend, db := range []*sql.DB{p.postgres, p.memory} {
		t.Run([]string{"postgres", "memory"}[backend], func(t *testing.T) {
			a, b := referenceLockConnections(t, p.ctx, db)
			referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
			referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10)", 1)
			tx, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			stmt, err := tx.PrepareContext(p.ctx, "SELECT v FROM t FOR SHARE")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			referenceLockExec(t, p.ctx, b, "UPDATE t SET v=11", 1) // Prepare has no row hold.
			rows, err := stmt.QueryContext(p.ctx)
			referenceRowValues(t, relationOutcome{rows: rows, err: err}, 11)
			stmt.Close()
			recorder := p.postgresRecord
			if backend == 1 {
				recorder = p.memoryRecord
			}
			referenceRunStep(t, p.ctx, b, recorder, referenceExec("SET lock_timeout='20ms'", 0))
			_, err = b.ExecContext(p.ctx, "UPDATE t SET v=12")
			referenceRowTimeoutDiagnostic(t, err, backend == 0, "55P03", "canceling statement due to lock timeout", "updating")
			referenceRunStep(t, p.ctx, b, recorder, referenceExec("RESET lock_timeout", 0))
			referenceRunStep(t, p.ctx, b, recorder, referenceExec("SET statement_timeout='20ms'", 0))
			_, err = b.ExecContext(p.ctx, "SELECT v FROM t FOR UPDATE")
			referenceRowTimeoutDiagnostic(t, err, backend == 0, "57014", "canceling statement due to statement timeout", "locking")
			referenceRunStep(t, p.ctx, b, recorder, referenceExec("RESET statement_timeout", 0))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			referenceLockExec(t, p.ctx, b, "UPDATE t SET v=12", 1)
		})
	}
}
func TestReferenceRowLockJoinedIndependence(t *testing.T) {
	p := newReferencePair(t)
	for backend, db := range []*sql.DB{p.postgres, p.memory} {
		t.Run([]string{"postgres", "memory"}[backend], func(t *testing.T) {
			a, b := referenceLockConnections(t, p.ctx, db)
			for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "CREATE TABLE u(id INT)", "INSERT INTO t VALUES(1,10),(2,20)", "INSERT INTO u VALUES(1),(3)"} {
				if _, err := a.ExecContext(p.ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			ap, bp := 0, 0
			if backend == 0 {
				ap, bp = referenceRowPIDs(t, p.ctx, a, b)
			}
			for _, prepared := range []bool{false, true} {
				tx, err := a.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer tx.Rollback()
					referenceRowValues(t, referenceRowQuery(p.ctx, tx, "SELECT t.v FROM t JOIN u USING(id) FOR SHARE", prepared), 10)
					referenceRowAffected(t, p.ctx, b, "UPDATE u SET id=3 WHERE id=3", prepared, 1)
					referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=20 WHERE id=2", prepared, 1)
					if backend == 0 {
						ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
						defer cancel()
						done := referenceRowAsync(t, ctx, b, "DELETE FROM u WHERE id=1", prepared, false)
						referenceRowWait(t, ctx, bp, ap)
						tx.Rollback()
						outcome := referenceLockResult(t, done)
						if outcome.err != nil {
							t.Fatal(outcome.err)
						}
						referenceLockAffected(t, outcome.result, 1)
						referenceLockExec(t, p.ctx, b, "INSERT INTO u VALUES(1)", 1)
					} else {
						ctx, cancel := context.WithTimeout(p.ctx, 30*time.Millisecond)
						outcome := referenceRowExecute(ctx, b, "DELETE FROM u WHERE id=1", prepared)
						cancel()
						if !errors.Is(outcome.err, context.DeadlineExceeded) {
							t.Fatal(outcome.err)
						}
					}
				}()
			}
		})
	}
}
func TestReferenceRowLockKeyControl(t *testing.T) {
	p := newReferencePair(t)
	a, b := referenceLockConnections(t, p.ctx, p.postgres)
	referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT PRIMARY KEY,v INT)", 0)
	referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10)", 1)
	ap, bp := referenceRowPIDs(t, p.ctx, a, b)
	tx, err := a.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	referenceRowValues(t, referenceRowQuery(p.ctx, tx, "SELECT v FROM t FOR KEY SHARE", false), 10)
	// PostgreSQL-only unique-key metadata control; native tables are keyless.
	referenceLockExec(t, p.ctx, b, "UPDATE t SET v=20 WHERE id=1", 1)
	ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
	defer cancel()
	done := referenceRowAsync(t, ctx, b, "UPDATE t SET id=2 WHERE id=1", true, false)
	referenceRowWait(t, ctx, bp, ap)
	tx.Rollback()
	outcome := referenceLockResult(t, done)
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	referenceLockAffected(t, outcome.result, 1)
	referenceRowValues(t, referenceRowQuery(p.ctx, b, "SELECT v FROM t WHERE id=2 FOR UPDATE", true), 20)
}
func TestReferenceRowLockSavepointControl(t *testing.T) {
	p := newReferencePair(t)
	a, b := referenceLockConnections(t, p.ctx, p.postgres)
	referenceLockExec(t, p.ctx, a, "CREATE TABLE t(id INT,v INT)", 0)
	referenceLockExec(t, p.ctx, a, "INSERT INTO t VALUES(1,10),(2,20)", 2)
	tx, err := a.BeginTx(p.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	referenceRowValues(t, referenceRowQuery(p.ctx, tx, "SELECT v FROM t WHERE id=1 FOR KEY SHARE", false), 10)
	referenceLockExec(t, p.ctx, tx, "SAVEPOINT s", 0)
	referenceRowValues(t, referenceRowQuery(p.ctx, tx, "SELECT v FROM t WHERE id=2 FOR SHARE", true), 20)
	referenceLockExec(t, p.ctx, tx, "ROLLBACK TO s", 0)
	// This PostgreSQL-only post-mark release differs from native conservative
	// outer-ticket retention, independently asserted in engine/public tests.
	referenceLockExec(t, p.ctx, b, "UPDATE t SET v=21 WHERE id=2", 1)
}

func referenceRowTimeoutDiagnostic(t *testing.T, err error, postgres bool, code, message, action string) {
	t.Helper()
	got := referenceExtractDiagnostic(t, err)
	if postgres {
		pattern := `^while ` + action + ` tuple \([0-9]+,[1-9][0-9]*\) in relation "t"$`
		if !regexp.MustCompile(pattern).MatchString(got.Context) {
			t.Fatalf("PostgreSQL physical wait context: %#v", got)
		}
		got.Context = ""
	}
	want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wait diagnostic %#v want %#v", got, want)
	}
}
