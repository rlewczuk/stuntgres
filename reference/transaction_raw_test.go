package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/lib/pq"
)

func referenceTransactionRaw(t *testing.T, p *referencePair, backend referenceBackend) (driver.Conn, func() error) {
	t.Helper()
	var connector driver.Connector = p.nativeConnector
	if backend.pg {
		var err error
		connector, err = pq.NewConnector(referenceDSN)
		if err != nil {
			t.Fatal(err)
		}
	}
	c, err := connector.Connect(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	closeRaw := func() error {
		err := c.Close()
		closed = true
		return err
	}
	t.Cleanup(func() {
		if backend.pg && closed {
			return
		}
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c, closeRaw
}

func referenceTransactionRawExec(t *testing.T, ctx context.Context, c driver.Conn, query string, want int64) {
	t.Helper()
	r, err := c.(driver.ExecerContext).ExecContext(ctx, query, nil)
	if err != nil {
		t.Fatal(query, err)
	}
	referenceLockAffected(t, r, want)
}

func referenceTransactionRawValues(t *testing.T, c driver.Conn, query string, want ...int64) {
	t.Helper()
	// Non-cancellation raw observations use the standard background context:
	// pq's cancel watcher finalizes at EOF, unlike database/sql's Rows wrapper.
	rows, err := c.(driver.QueryerContext).QueryContext(context.Background(), query, nil)
	if err != nil {
		t.Fatal(err)
	}
	referenceTransactionRawRows(t, rows, want...)
}

func referenceTransactionRawRows(t *testing.T, rows driver.Rows, want ...int64) {
	t.Helper()
	defer rows.Close()
	if !reflect.DeepEqual(rows.Columns(), []string{"v"}) {
		t.Fatal(rows.Columns())
	}
	if types, ok := rows.(driver.RowsColumnTypeDatabaseTypeName); !ok || types.ColumnTypeDatabaseTypeName(0) != "INT4" {
		t.Fatal("raw INT4 metadata missing")
	}
	var got []int64
	values := make([]driver.Value, 1)
	for {
		err := rows.Next(values)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		v, ok := values[0].(int64)
		if !ok {
			t.Fatalf("raw INT4 cell %T %#v", values[0], values[0])
		}
		got = append(got, v)
	}
	if len(got) != len(want) {
		t.Fatal(got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatal(got, want)
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal("repeat raw Close", err)
	}
}

func TestReferenceTransactionRawHandleLifecycle(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, closeA := referenceTransactionRaw(t, p, backend)
			b, _ := referenceTransactionRaw(t, p, backend)
			ctx := p.ctx
			referenceTransactionRawExec(t, ctx, b, "CREATE TABLE t(v INT)", 0)
			referenceTransactionRawExec(t, ctx, b, "INSERT INTO t VALUES(7)", 1)
			first, err := a.(driver.ConnBeginTx).BeginTx(ctx, driver.TxOptions{})
			if err != nil {
				t.Fatal(err)
			}
			referenceTransactionRawExec(t, ctx, a, "INSERT INTO t VALUES(1)", 1)
			nested, err := a.(driver.ConnBeginTx).BeginTx(ctx, driver.TxOptions{})
			if nested != nil {
				nested.Rollback()
				t.Fatal("nested Begin returned a handle")
			}
			if backend.pg {
				transactionOrdinary(t, err, "pq: unexpected transaction status idle in transaction")
				if err := closeA(); err != nil {
					t.Fatal(err)
				}
				referenceTransactionRawValues(t, b, "SELECT v FROM t ORDER BY v", 7)
				a, _ = referenceTransactionRaw(t, p, backend)
				first, err = a.(driver.ConnBeginTx).BeginTx(ctx, driver.TxOptions{})
				if err != nil {
					t.Fatal(err)
				}
				referenceTransactionRawExec(t, ctx, a, "INSERT INTO t VALUES(1)", 1)
			} else {
				transactionOrdinary(t, err, "stuntgres: transaction already active")
			}
			statement, err := a.(driver.ConnPrepareContext).PrepareContext(ctx, "SELECT v FROM t ORDER BY v")
			if err != nil {
				t.Fatal(err)
			}
			if err := statement.Close(); err != nil {
				t.Fatal(err)
			}
			if reset, ok := a.(driver.SessionResetter); ok {
				if err := reset.ResetSession(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if !backend.pg {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				r, err := a.(driver.ExecerContext).ExecContext(canceled, "INSERT INTO t VALUES(2)", nil)
				if r != nil || !errors.Is(err, context.Canceled) {
					t.Fatal(r, err)
				}
				if _, ok := err.(interface{ SQLState() string }); ok {
					t.Fatal("caller cancellation fabricated SQLSTATE", err)
				}
				if _, err := statement.(driver.StmtExecContext).ExecContext(ctx, nil); err == nil {
					t.Fatal("closed raw statement executed")
				}
			}
			referenceTransactionRawValues(t, b, "SELECT v FROM t ORDER BY v", 7)
			if err := first.Commit(); err != nil {
				t.Fatal(err)
			}
			second, err := a.(driver.ConnBeginTx).BeginTx(ctx, driver.TxOptions{})
			if err != nil {
				t.Fatal(err)
			}
			referenceTransactionRawExec(t, ctx, a, "INSERT INTO t VALUES(2)", 1)
			if backend.pg {
				if err := first.Rollback(); err != nil {
					t.Fatal(err)
				}
				referenceTransactionRawValues(t, b, "SELECT v FROM t ORDER BY v", 1, 7)
			} else {
				transactionOrdinary(t, first.Rollback(), "stuntgres: transaction is done")
				if err := second.Commit(); err != nil {
					t.Fatal(err)
				}
				referenceTransactionRawValues(t, b, "SELECT v FROM t ORDER BY v", 1, 2, 7)
			}
		})
	}
}

func TestReferenceTransactionRawBeginContextLifetime(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		// Native raw caller-managed lifetime is a distinct capability phase.
		// The PG server-cancel/PID counterpart is covered once by
		// TestReferenceTransactionBeginContextCancellation.
		if backend.pg {
			continue
		}
		t.Run(backend.name, func(t *testing.T) {
			a, _ := referenceTransactionRaw(t, p, backend)
			b, _ := referenceTransactionRaw(t, p, backend)
			ctx := p.ctx
			referenceTransactionRawExec(t, ctx, b, "CREATE TABLE t(v INT)", 0)
			referenceTransactionRawExec(t, ctx, b, "INSERT INTO t VALUES(7)", 1)
			beginCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			tx, err := a.(driver.ConnBeginTx).BeginTx(beginCtx, driver.TxOptions{})
			if err != nil {
				t.Fatal(err)
			}
			referenceTransactionRawExec(t, ctx, a, "INSERT INTO t VALUES(3)", 1)
			cancel()
			if err := tx.Rollback(); err != nil {
				t.Fatal("raw Begin context changed caller-managed lifetime", err)
			}
			referenceTransactionRawValues(t, b, "SELECT v FROM t ORDER BY v", 7)
		})
	}
}

func TestReferenceTransactionRawArgumentRejectionDoesNotAbort(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, _ := referenceTransactionRaw(t, p, backend)
			b, _ := referenceTransactionRaw(t, p, backend)
			ctx := p.ctx
			referenceTransactionRawExec(t, ctx, b, "CREATE TABLE t(v INT)", 0)
			tx, err := a.(driver.ConnBeginTx).BeginTx(ctx, driver.TxOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceTransactionRawExec(t, ctx, a, "INSERT INTO t VALUES(1)", 1)
			stmt, err := a.(driver.ConnPrepareContext).PrepareContext(ctx, "INSERT INTO t VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			r, err := stmt.(driver.StmtExecContext).ExecContext(ctx, nil)
			if r != nil {
				t.Fatal("wrong-count execution returned a result")
			}
			want := "stuntgres: got 0 arguments, want 1"
			if backend.pg {
				want = "pq: got 0 parameters but the statement requires 1"
			}
			transactionOrdinary(t, err, want)
			r, err = a.(driver.ExecerContext).ExecContext(ctx, "INSERT INTO t VALUES($1)", []driver.NamedValue{{Name: "v", Ordinal: 1, Value: int64(2)}})
			if backend.pg {
				if err != nil {
					t.Fatal(err)
				}
				referenceLockAffected(t, r, 1)
			} else {
				if r != nil {
					t.Fatal("named-argument rejection returned a result")
				}
				transactionOrdinary(t, err, "stuntgres: named parameters are not supported")
			}
			if err := tx.Commit(); err != nil {
				t.Fatal("adapter rejection aborted prior writes", err)
			}
			if backend.pg {
				referenceTransactionRawValues(t, b, "SELECT v FROM t ORDER BY v", 1, 2)
			} else {
				referenceTransactionRawValues(t, b, "SELECT v FROM t ORDER BY v", 1)
			}
		})
	}
}
