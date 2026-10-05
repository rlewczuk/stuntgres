package smoke_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
	"time"
)

func TestRelationLockSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	db.SetMaxOpenConns(3)
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
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			if err = stuntgres.Reset(ctx, db); err != nil {
				t.Fatal(err)
			}
			if err = transactionAffected(ctx, a, "CREATE TABLE t(v INT)", false, 0); err != nil {
				t.Fatal(err)
			}
			tx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = transactionAffected(ctx, tx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", prepared, 0); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			if err = transactionAffected(ctx, tx, "INSERT INTO t VALUES(1)", prepared, 1); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			for _, q := range []string{"SELECT v FROM t", "INSERT INTO t VALUES(2)"} {
				bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
				_, err = transactionExecute(bounded, b, q, prepared)
				cancel()
				if !errors.Is(err, context.DeadlineExceeded) {
					tx.Rollback()
					t.Fatal("held AX cancellation", q, err)
				}
				var diagnostic *stuntgres.Error
				if errors.As(err, &diagnostic) {
					tx.Rollback()
					t.Fatal("fabricated context SQLSTATE", diagnostic)
				}
			}
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			if commit {
				err = transactionSelectValues(ctx, b, "SELECT v FROM t ORDER BY v", prepared, 1)
			} else {
				err = transactionSelectValues(ctx, b, "SELECT v FROM t ORDER BY v", prepared)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = transactionAffected(ctx, b, "INSERT INTO t VALUES(2)", prepared, 1); err != nil {
				t.Fatal(err)
			}
			if commit {
				err = transactionSelectValues(ctx, b, "SELECT v FROM t ORDER BY v", prepared, 1, 2)
			} else {
				err = transactionSelectValues(ctx, b, "SELECT v FROM t ORDER BY v", prepared, 2)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, q := range []string{"SELECT v FROM t", "INSERT INTO t VALUES(3)"} {
		stmt, err := a.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		stmt.Close()
		writer, err := b.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = transactionAffected(ctx, writer, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0); err != nil {
			writer.Rollback()
			t.Fatal("implicit prepare leaked", err)
		}
		writer.Rollback()
		reader, err := a.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		stmt, err = reader.PrepareContext(ctx, q)
		if err != nil {
			reader.Rollback()
			t.Fatal(err)
		}
		stmt.Close()
		if err = transactionAffected(ctx, b, "INSERT INTO t VALUES(7)", false, 1); err != nil {
			reader.Rollback()
			t.Fatal("AS/RX compatibility", err)
		}
		writer, err = b.BeginTx(ctx, nil)
		if err != nil {
			reader.Rollback()
			t.Fatal(err)
		}
		bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
		_, err = writer.ExecContext(bounded, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		cancel()
		writer.Rollback()
		reader.Rollback()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("explicit preparation ownership", err)
		}
	}
	check := func(err error, code, message, position string) {
		t.Helper()
		if err = transactionDiagnostic(err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}); err != nil {
			t.Fatal(err)
		}
	}
	for _, prepared := range []bool{false, true} {
		_, err = transactionExecute(ctx, a, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE", prepared)
		check(err, "25P01", "LOCK TABLE can only be used in transaction blocks", "")
	}
	tx, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE")
	check(err, "42P01", `relation "absent" does not exist`, "")
	_, err = tx.ExecContext(ctx, "LOCK TABLE absent IN ACCESS EXCLUSIVE MODE")
	check(err, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
	_, err = tx.ExecContext(ctx, "SELECT * FROM t WHERE v = )")
	check(err, "42601", `syntax error at or near ")"`, "27")
}
