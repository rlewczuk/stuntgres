package stuntgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"stuntgres/internal/engine"
	"testing"
)

func transactionRaw(t *testing.T, d *Driver, dsn string) *conn {
	t.Helper()
	connector, err := d.OpenConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := connector.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c := raw.(*conn)
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c
}
func transactionRawExec(t *testing.T, c *conn, q string) {
	t.Helper()
	if _, err := c.ExecContext(context.Background(), q, nil); err != nil {
		t.Fatal(err)
	}
}
func transactionRawValues(t *testing.T, r driver.Rows, want ...int64) {
	t.Helper()
	defer r.Close()
	got := []int64{}
	dst := make([]driver.Value, 1)
	for {
		err := r.Next(dst)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, dst[0].(int64))
	}
	if len(got) != len(want) {
		t.Fatal(got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatal(got, want)
		}
	}
}
func TestTransactionRawHandleAndBorrowerLifecycle(t *testing.T) {
	d := NewDriver()
	a := transactionRaw(t, d, t.Name())
	b := transactionRaw(t, d, t.Name())
	ctx := context.Background()
	transactionRawExec(t, b, "CREATE TABLE t(v INT)")
	transactionRawExec(t, b, "INSERT INTO t VALUES(7)")
	first, err := a.BeginTx(ctx, driver.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	transactionRawExec(t, a, "INSERT INTO t VALUES(1)")
	if _, err = a.BeginTx(ctx, driver.TxOptions{}); err == nil || err.Error() != "stuntgres: transaction already active" {
		t.Fatal(err)
	}
	statement, err := a.PrepareContext(ctx, "SELECT v FROM t ORDER BY v")
	if err != nil {
		t.Fatal(err)
	}
	statement.Close()
	if err = a.ResetSession(ctx); err != nil {
		t.Fatal(err)
	}
	if a.session.TransactionState() != engine.TxActive {
		t.Fatal("statement/reset ended transaction")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = a.ExecContext(canceled, "INSERT INTO t VALUES(2)", nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err = statement.(driver.StmtExecContext).ExecContext(ctx, nil); err == nil {
		t.Fatal("closed stmt")
	}
	if err = first.Commit(); err != nil {
		t.Fatal(err)
	}
	second, err := a.BeginTx(ctx, driver.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	transactionRawExec(t, a, "INSERT INTO t VALUES(2)")
	if err = first.Rollback(); err == nil || err.Error() != "stuntgres: transaction is done" {
		t.Fatal(err)
	}
	if err = second.Commit(); err != nil {
		t.Fatal(err)
	}
	r, err := b.QueryContext(ctx, "SELECT v FROM t ORDER BY v", nil)
	if err != nil {
		t.Fatal(err)
	}
	transactionRawValues(t, r, 1, 2, 7)
	beginCtx, beginCancel := context.WithCancel(ctx)
	third, err := a.BeginTx(beginCtx, driver.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	transactionRawExec(t, a, "INSERT INTO t VALUES(3)")
	beginCancel()
	if err = third.Rollback(); err != nil {
		t.Fatal(err)
	}
	fourth, err := a.BeginTx(ctx, driver.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	transactionRawExec(t, a, "INSERT INTO t VALUES(4)")
	retained, err := a.QueryContext(ctx, "SELECT v FROM t ORDER BY v", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = fourth.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err = b.session.Engine().Reset(ctx); err != nil {
		t.Fatal(err)
	}
	transactionRawValues(t, retained, 1, 2, 4, 7)
}
func TestTransactionBorrowerCloseAndPhysicalEnd(t *testing.T) {
	d := NewDriver()
	db := sessionPool(t, d, t.Name())
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var physical *conn
	var handle driver.Tx
	if err = a.Raw(func(raw any) error {
		physical = raw.(*conn)
		transactionRawExec(t, physical, "CREATE TABLE t(v INT)")
		transactionRawExec(t, physical, "INSERT INTO t VALUES(7)")
		var err error
		handle, err = physical.Begin()
		if err == nil {
			transactionRawExec(t, physical, "INSERT INTO t VALUES(1)")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	borrower, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := nativeSession(t, borrower); got != physical.session || got.TransactionState() != engine.TxActive {
		t.Fatal("borrower release ended private work")
	}
	if err = borrower.Raw(func(raw any) error {
		if raw.(*conn) != physical {
			return errors.New("physical connection changed")
		}
		return handle.Rollback()
	}); err != nil {
		t.Fatal(err)
	}
	borrower.Close()
	a, err = db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Raw(func(raw any) error {
		c := raw.(*conn)
		_, err := c.Begin()
		if err == nil {
			transactionRawExec(t, c, "INSERT INTO t VALUES(2)")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	a.Close()
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := sessionPool(t, d, t.Name())
	rows, err := reopened.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []int64
	for rows.Next() {
		var v int64
		if err = rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if rows.Err() != nil || !reflect.DeepEqual(got, []int64{7}) {
		t.Fatal(got, rows.Err())
	}
	for _, other := range []*conn{transactionRaw(t, d, t.Name()+"/other"), transactionRaw(t, NewDriver(), t.Name())} {
		_, err = other.QueryContext(ctx, "SELECT v FROM t", nil)
		var sqlerr *Error
		if !errors.As(err, &sqlerr) || sqlerr.Code != "42P01" {
			t.Fatal("engine isolation", err)
		}
	}
}

func TestTransactionRawTerminalCleanup(t *testing.T) {
	for _, reason := range []engine.EndReason{engine.CleanClose, engine.Terminated, engine.Abandoned} {
		t.Run(map[engine.EndReason]string{engine.CleanClose: "close", engine.Terminated: "terminate", engine.Abandoned: "abandon"}[reason], func(t *testing.T) {
			d := NewDriver()
			a := transactionRaw(t, d, t.Name())
			b := transactionRaw(t, d, t.Name())
			ctx := context.Background()
			transactionRawExec(t, b, "CREATE TABLE t(v INT)")
			transactionRawExec(t, b, "INSERT INTO t VALUES(7)")
			transactionRawExec(t, b, "SET application_name TO peer")
			if _, err := a.Begin(); err != nil {
				t.Fatal(err)
			}
			transactionRawExec(t, a, "INSERT INTO t VALUES(1)")
			switch reason {
			case engine.CleanClose:
				if err := a.Close(); err != nil {
					t.Fatal(err)
				}
			case engine.Terminated:
				a.session.RequestEnd(reason)
			case engine.Abandoned:
				// Invoke the real cleanup capsule deterministically; GC timing
				// is not a compatibility guarantee or a test admission barrier.
				cleanupSession(cleanupCapsule{a.session})
			}
			if err := a.session.WaitEnd(ctx); err != nil {
				t.Fatal(err)
			}
			rows, err := b.QueryContext(ctx, "SELECT v FROM t ORDER BY v", nil)
			if err != nil {
				t.Fatal(err)
			}
			transactionRawValues(t, rows, 7)
			rows, err = b.QueryContext(ctx, "SHOW application_name", nil)
			if err != nil {
				t.Fatal(err)
			}
			value := make([]driver.Value, 1)
			if err = rows.Next(value); err != nil || value[0] != "peer" {
				t.Fatal(value, err)
			}
			rows.Close()
		})
	}
}

func TestTransactionRawArgumentRejectionDoesNotAbort(t *testing.T) {
	d := NewDriver()
	a := transactionRaw(t, d, t.Name())
	b := transactionRaw(t, d, t.Name())
	ctx := context.Background()
	transactionRawExec(t, b, "CREATE TABLE t(v INT)")
	handle, err := a.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Rollback()
	transactionRawExec(t, a, "INSERT INTO t VALUES(1)")
	prepared, err := a.PrepareContext(ctx, "INSERT INTO t VALUES($1)")
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	_, err = prepared.(driver.StmtExecContext).ExecContext(ctx, nil)
	if err == nil || err.Error() != "stuntgres: got 0 arguments, want 1" {
		t.Fatal(err)
	}
	_, err = a.ExecContext(ctx, "INSERT INTO t VALUES($1)", []driver.NamedValue{{Name: "v", Ordinal: 1, Value: int64(2)}})
	if err == nil || err.Error() != "stuntgres: named parameters are not supported" {
		t.Fatal(err)
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		t.Fatal("argument error fabricated SQLSTATE")
	}
	if a.session.TransactionState() != engine.TxActive {
		t.Fatal("adapter rejection aborted prior writes")
	}
	if err = handle.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err := b.QueryContext(ctx, "SELECT v FROM t ORDER BY v", nil)
	if err != nil {
		t.Fatal(err)
	}
	transactionRawValues(t, rows, 1)
}
