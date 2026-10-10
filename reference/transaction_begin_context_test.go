package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
)

func TestReferenceTransactionBeginContextCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := p.ctx
			a, b := referenceLockConnections(t, ctx, backend.db)
			run := func(x referenceExecutor, s referenceStep) { referenceRunStep(t, ctx, x, backend.recorder, s) }
			run(b, referenceExec("CREATE TABLE t(v INT)", 0))
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			absent, err := a.BeginTx(canceled, nil)
			if backend.pg {
				// A pre-canceled Begin and its cleanup can race pq's context
				// watcher. Neither admission nor rollback transport outcomes
				// establish PostgreSQL SQL behavior. Dispose any transaction,
				// retire the physical session, then assert independent SQL.
				if absent != nil {
					absent.Rollback()
				}
				a.Raw(func(any) error { return driver.ErrBadConn })
				a.Close()
				a, err = backend.db.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { a.Close() })
				run(b, referenceTransactionRows(false, "SELECT v FROM t"))
				run(a, referenceTransactionRows(false, "SELECT v FROM t"))
			} else {
				if absent != nil || !errors.Is(err, context.Canceled) {
					t.Fatal("pre-canceled Begin", absent, err)
				}
				if _, ok := err.(interface{ SQLState() string }); ok {
					t.Fatal("caller cancellation exposes SQLState", err)
				}
			}
			txctx, txcancel := context.WithCancel(ctx)
			defer txcancel()
			tx, err := a.BeginTx(txctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			run(tx, referenceExec("INSERT INTO t VALUES(1)", 1))
			txcancel()
			err = tx.Commit()
			if !errors.Is(err, context.Canceled) && !errors.Is(err, sql.ErrTxDone) {
				t.Fatal(err)
			}
			// Closing the borrower drains database/sql's automatic rollback lease.
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			a, err = backend.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			run(b, referenceTransactionRows(false, "SELECT v FROM t ORDER BY v"))
			next, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Rollback()
			run(next, referenceExec("INSERT INTO t VALUES(2)", 1))
			if err := next.Commit(); err != nil {
				t.Fatal(err)
			}
			run(b, referenceTransactionRows(false, "SELECT v FROM t ORDER BY v", 2))
			run(b, referenceExec("DROP TABLE t", 0))
		})
	}
	if p.postgres != nil {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "server/direct", true: "server/prepared"}[prepared], func(t *testing.T) {
				oracle := newReferenceScenario(t)
				referenceTransactionCancellationOracle(t, oracle, prepared)
			})
		}
	}
}
