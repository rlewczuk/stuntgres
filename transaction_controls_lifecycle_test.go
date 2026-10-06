package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres"
)

func TestControlTransactionsStaleAbortedCommit(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		endSQL := "COMMIT"
		if prepared {
			name = "retained_prepared_chain"
			endSQL = "COMMIT AND CHAIN"
		}
		t.Run(name, func(t *testing.T) {
			db, a, b, _ := controlsFixture(t)
			ctx := context.Background()
			transactionAffected(t, ctx, b, "INSERT INTO t VALUES(77)", false, 1)
			transactionAffected(t, ctx, a, "BEGIN", false, 0)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
			var end *sql.Stmt
			if prepared {
				var err error
				end, err = a.PrepareContext(ctx, endSQL)
				if err != nil {
					t.Fatal(err)
				}
				defer end.Close()
			}
			if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE t(v INT NOT NULL); INSERT INTO t VALUES(99);")); err != nil {
				t.Fatal(err)
			}
			_, err := a.ExecContext(ctx, "SELECT v FROM t")
			transactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
			if prepared {
				_, err = end.ExecContext(ctx)
			} else {
				_, err = a.ExecContext(ctx, endSQL)
			}
			transactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
			transactionSelect(t, ctx, b, "SELECT v FROM t", false, 99)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(2)", false, 1)
			transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 2, 99)
		})
	}
}

func TestTransactionControlsCancellationAndReplacement(t *testing.T) {
	db, a, b, _ := controlsFixture(t)
	ctx := context.Background()
	transactionAffected(t, ctx, b, "INSERT INTO t VALUES(77)", false, 1)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err := a.ExecContext(canceled, "BEGIN")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
	transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 1, 77)
	transactionAffected(t, ctx, a, "SET statement_timeout TO '20ms'", false, 0)
	transactionAffected(t, ctx, b, "BEGIN", false, 0)
	transactionAffected(t, ctx, b, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0)
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	_, err = a.ExecContext(ctx, "INSERT INTO t VALUES(2)")
	transactionDiagnostic(t, err, controlsDiagnostic("57014", "canceling statement due to statement timeout"))
	_, err = a.ExecContext(ctx, "SELECT v FROM t")
	transactionDiagnostic(t, err, controlsAborted())
	transactionAffected(t, ctx, a, "ROLLBACK AND CHAIN", false, 0)
	transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
	transactionAffected(t, ctx, a, "SET statement_timeout TO 0", false, 0)
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	deadline, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	_, err = a.ExecContext(deadline, "INSERT INTO t VALUES(3)")
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	var diagnostic *stuntgres.Error
	if errors.As(err, &diagnostic) {
		t.Fatal("caller deadline fabricated SQLSTATE", err)
	}
	transactionAffected(t, ctx, a, "COMMIT", false, 0)
	transactionAffected(t, ctx, b, "ROLLBACK", false, 0)
	transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 1, 77)
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(4)", false, 1)
	rows, err := a.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
	if err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE t(v INT NOT NULL);INSERT INTO t VALUES(99);")); err != nil {
		t.Fatal(err)
	}
	transactionValues(t, rows, 1, 4, 77)
	_, err = a.ExecContext(ctx, "COMMIT AND CHAIN")
	transactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
	transactionSelect(t, ctx, b, "SELECT v FROM t", false, 99)
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(5)", false, 1)
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	transactionAffected(t, ctx, a, "ROLLBACK AND CHAIN", false, 0)
	transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
	transactionAffected(t, ctx, b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(6)", false, 1)
	transactionSelect(t, ctx, b, "SELECT v FROM t", false, 6)
}
func TestTransactionControlsLockLifecycle(t *testing.T) {
	for _, q := range []string{"COMMIT AND CHAIN", "ROLLBACK AND CHAIN", "ABORT AND CHAIN"} {
		t.Run(q, func(t *testing.T) {
			_, a, b, _ := controlsFixture(t)
			ctx := context.Background()
			transactionAffected(t, ctx, a, "BEGIN", false, 0)
			transactionAffected(t, ctx, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
			end, err := a.PrepareContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer end.Close()
			blocked, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
			_, err = b.QueryContext(blocked, "SELECT v FROM t")
			cancel()
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("Describe released hold", err)
			}
			if _, err = end.ExecContext(ctx); err != nil {
				t.Fatal(err)
			}
			want := []int64{}
			if strings.HasPrefix(q, "COMMIT") {
				want = []int64{1}
			}
			transactionSelect(t, ctx, b, "SELECT v FROM t", true, want...)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(2)", false, 1)
			transactionSelect(t, ctx, b, "SELECT v FROM t", false, want...)
			transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
			transactionAffected(t, ctx, a, "BEGIN", false, 0)
			transactionAffected(t, ctx, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0)
			transactionDiagnostic(t, transactionConstraintExec(t, ctx, a, "INSERT INTO t VALUES(NULL)", true), controlsNull())
			transactionSelect(t, ctx, b, "SELECT v FROM t", false, want...)
			transactionAffected(t, ctx, a, "COMMIT AND CHAIN", false, 0)
			transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
		})
	}
}
func TestTransactionControlsSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	peer := openLocal(t, d, t.Name())
	transactionAffected(t, ctx, peer, "CREATE TABLE t(v INT NOT NULL)", false, 0)
	transactionAffected(t, ctx, peer, "INSERT INTO t VALUES(77)", false, 1)
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	transactionAffected(t, ctx, a, "SET application_name TO 'sql-owner'", false, 0)
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
	stmt, err := a.PrepareContext(ctx, "COMMIT")
	if err != nil {
		t.Fatal(err)
	}
	stmt.Close()
	a.Close()
	a, err = db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1, 77)
	transactionSelect(t, ctx, peer, "SELECT v FROM t", false, 77)
	transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
	var name string
	if err = a.QueryRowContext(ctx, "SHOW application_name").Scan(&name); err != nil || name != "sql-owner" {
		t.Fatal(name, err)
	}
	a.Close()
	a, err = db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(2)", false, 1)
	transactionSelect(t, ctx, peer, "SELECT v FROM t ORDER BY v", false, 2, 77)
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(3)", false, 1)
	a.Close()
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openLocal(t, d, t.Name())
	transactionSelect(t, ctx, reopened, "SELECT v FROM t ORDER BY v", false, 2, 77)
	for _, isolated := range []*sql.DB{openLocal(t, d, t.Name()+"/other"), openLocal(t, stuntgres.NewDriver(), t.Name())} {
		transactionAffected(t, ctx, isolated, "CREATE TABLE t(v INT)", false, 0)
		transactionSelect(t, ctx, isolated, "SELECT v FROM t", false)
	}
}
func TestTransactionControlsNativeRoundTrip(t *testing.T) {
	db, a, b, _ := controlsFixture(t)
	ctx := context.Background()
	transactionAffected(t, ctx, b, "INSERT INTO t VALUES(77)", false, 1)
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	clone := openLocal(t, stuntgres.NewDriver(), t.Name()+"/clone")
	if err := stuntgres.Restore(ctx, clone, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	transactionSelect(t, ctx, clone, "SELECT v FROM t", false, 77)
	for _, script := range []string{"BEGIN;CREATE TABLE x(v INT);COMMIT;", "ROLLBACK;", "CREATE TABLE x(v INT);INSERT INTO x VALUES('bad');"} {
		if err := stuntgres.Restore(ctx, db, strings.NewReader(script)); err == nil {
			t.Fatal("restore accepted invalid script", script)
		}
		transactionSelect(t, ctx, b, "SELECT v FROM t", false, 77)
	}
	readErr := errors.New("transaction controls reader failure")
	if err := stuntgres.Restore(ctx, db, failingReader{readErr}); !errors.Is(err, readErr) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := stuntgres.Restore(canceled, db, strings.NewReader("CREATE TABLE x(v INT)")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1, 77)
	transactionAffected(t, ctx, a, "COMMIT", false, 0)
	transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 1, 77)
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(3)", false, 1)
	if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE t(v INT NOT NULL);INSERT INTO t VALUES(99);")); err != nil {
		t.Fatal(err)
	}
	transactionAffected(t, ctx, a, "ROLLBACK AND CHAIN", true, 0)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(4)", true, 1)
	transactionAffected(t, ctx, a, "END", false, 0)
	transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 4, 99)
}
