package stuntgres_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"stuntgres"
	"testing"
	"time"
)

func TestRelationLockPublic(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared]+map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
				_, a, b := transactionConnections(t)
				ctx := context.Background()
				transactionAffected(t, ctx, a, "CREATE TABLE t(v INT)", false, 0)
				cached, err := b.PrepareContext(ctx, "SELECT v FROM t ORDER BY v")
				if err != nil {
					t.Fatal(err)
				}
				defer cached.Close()
				tx, err := a.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				transactionAffected(t, ctx, tx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", prepared, 0)
				transactionAffected(t, ctx, tx, "INSERT INTO t VALUES(1)", prepared, 1)
				for _, q := range []string{"SELECT v FROM t", "INSERT INTO t VALUES(2)"} {
					bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
					_, err := transactionExec(bounded, b, q, prepared)
					cancel()
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatalf("waiting %s: %v", q, err)
					}
					var state *stuntgres.Error
					if errors.As(err, &state) {
						t.Fatal("context error acquired SQLSTATE", state)
					}
				}
				bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
				rows, err := cached.QueryContext(bounded)
				cancel()
				if rows != nil {
					rows.Close()
				}
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("cached execution", err)
				}
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				rows, err = cached.QueryContext(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if commit {
					transactionValues(t, rows, 1)
				} else {
					transactionValues(t, rows)
				}
				transactionAffected(t, ctx, b, "INSERT INTO t VALUES(2)", prepared, 1)
				rows, err = b.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
				if err != nil {
					t.Fatal(err)
				}
				if commit {
					transactionValues(t, rows, 1, 2)
				} else {
					transactionValues(t, rows, 2)
				}
			})
		}
	}
}
func TestRelationLockPublicPreparation(t *testing.T) {
	_, a, b := transactionConnections(t)
	ctx := context.Background()
	transactionAffected(t, ctx, a, "CREATE TABLE t(v INT)", false, 0)
	for _, q := range []string{"SELECT v FROM t", "INSERT INTO t VALUES(1)"} {
		stmt, err := a.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		stmt.Close()
		tx, err := b.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		transactionAffected(t, ctx, tx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0)
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		reader, err := a.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		stmt, err = reader.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		stmt.Close()
		// RX/AS are compatible; the positive operation completes before reader end.
		transactionAffected(t, ctx, b, "INSERT INTO t VALUES(7)", false, 1)
		writer, err := b.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
		_, err = writer.ExecContext(bounded, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("stmt.Close released explicit preparation", err)
		}
		writer.Rollback()
		reader.Rollback()
	}
}
func TestRelationLockPublicPhysicalEnd(t *testing.T) {
	d := stuntgres.NewDriver()
	connector, err := d.OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, err := connector.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b := sql.OpenDB(connector)
	defer b.Close()
	exec := a.(driver.ExecerContext)
	if _, err = exec.ExecContext(ctx, "CREATE TABLE t(v INT)", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = exec.ExecContext(ctx, "INSERT INTO t VALUES(7)", nil); err != nil {
		t.Fatal(err)
	}
	tx, err := a.(driver.ConnBeginTx).BeginTx(ctx, driver.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = tx
	if _, err = exec.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = exec.ExecContext(ctx, "INSERT INTO t VALUES(1)", nil); err != nil {
		t.Fatal(err)
	}
	// This public path proves actual driver.Conn.Close, not borrower return.
	// TestRelationLockHolderPhysicalEnd supplies the native manager queue witness.
	type outcome struct {
		rows *sql.Rows
		err  error
	}
	done := make(chan outcome, 1)
	joined := false
	go func() {
		rows, err := b.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
		done <- outcome{rows: rows, err: err}
	}()
	defer func() {
		cancel()
		if !joined {
			select {
			case o := <-done:
				if o.rows != nil {
					o.rows.Close()
				}
			case <-time.After(5 * time.Second):
				t.Error("physical-close waiter did not terminate")
			}
		}
	}()
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case o := <-done:
		joined = true
		if o.err != nil {
			t.Fatal(o.err)
		}
		transactionValues(t, o.rows, 7)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	healthy, err := b.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer healthy.Rollback()
	transactionAffected(t, ctx, healthy, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false, 0)
	rows, err := healthy.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
	if err != nil {
		t.Fatal(err)
	}
	transactionValues(t, rows, 7)
	if err := healthy.Rollback(); err != nil {
		t.Fatal(err)
	}
}
