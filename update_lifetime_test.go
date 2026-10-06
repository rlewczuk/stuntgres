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

func TestUpdateCoreBorrowerAndStatementClose(t *testing.T) {
	d := stuntgres.NewDriver()
	a := openLocal(t, d, t.Name())
	b := openLocal(t, d, t.Name())
	a.SetMaxOpenConns(1)
	a.SetMaxIdleConns(1)
	execSQL(t, a, "CREATE TABLE t(v INT)")
	execSQL(t, a, "INSERT INTO t VALUES(1)")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	borrower, err := a.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = borrower.ExecContext(ctx, "BEGIN"); err != nil {
		t.Fatal(err)
	}
	stmt, err := borrower.PrepareContext(ctx, "UPDATE t SET v=2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stmt.ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	if err = stmt.Close(); err != nil {
		t.Fatal(err)
	}
	rows, err := borrower.QueryContext(ctx, "SELECT v FROM t")
	if err != nil {
		t.Fatal(err)
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err = borrower.Close(); err != nil {
		t.Fatal(err)
	}
	// Borrower return leaves the explicit owner and its row hold in the pool.
	short, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	_, err = b.ExecContext(short, "UPDATE t SET v=3")
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("stmt/Rows/borrower Close released explicit row hold", err)
	}
	borrower, err = a.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer borrower.Close()
	defer borrower.ExecContext(context.Background(), "ROLLBACK")
	rows, err = borrower.QueryContext(ctx, "SELECT v FROM t")
	if err != nil {
		t.Fatal(err)
	}
	transactionValues(t, rows, 2)
	if _, err = borrower.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	result, err := b.ExecContext(ctx, "UPDATE t SET v=3")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	wantTuples(t, b, "SELECT v FROM t", [][]any{{int64(3)}})
}

func TestUpdateCorePublicPhysicalEnd(t *testing.T) {
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	physical, err := connector.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer physical.Close()
	peer := sql.OpenDB(connector)
	defer peer.Close()
	execute := physical.(driver.ExecerContext)
	for _, q := range []string{"CREATE TABLE t(v INT)", "INSERT INTO t VALUES(1)", "BEGIN", "UPDATE t SET v=2"} {
		if _, err = execute.ExecContext(ctx, q, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err = physical.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := peer.ExecContext(ctx, "UPDATE t SET v=3")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	wantTuples(t, peer, "SELECT v FROM t", [][]any{{int64(3)}})
}
