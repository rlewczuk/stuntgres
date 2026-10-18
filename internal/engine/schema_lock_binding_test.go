package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/sqltype"
)

// The shadow has the same shape, so frozen descriptions cannot enforce the
// acquired-resource invariant. Keep its AX until the waiter has terminated.
func TestSchemaLockedBindingShadowCreation(t *testing.T) {
	for _, kind := range []string{"select", "insert", "prepare-select", "prepare-insert", "held-select", "held-insert"} {
		t.Run(kind, func(t *testing.T) {
			e := New()
			setup := sessionStart(t, e, nil)
			late := sessionStart(t, e, nil)
			early := sessionStart(t, e, nil)
			waiter := sessionStart(t, e, nil)
			sessionSQL(t, setup, "CREATE SCHEMA early")
			sessionSQL(t, setup, "CREATE SCHEMA late CREATE TABLE t(id INT)")
			sessionSQL(t, setup, "INSERT INTO late.t VALUES(7)")
			sessionSQL(t, waiter, "SET search_path TO early, late")
			query := "SELECT id FROM t"
			insert := kind == "insert" || kind == "prepare-insert" || kind == "held-insert"
			if insert {
				query = "INSERT INTO t VALUES($1)"
			}
			ast := statement(t, query)
			var expected *StatementDescription
			if kind == "held-select" || kind == "held-insert" {
				op := transactionOperation(t, waiter)
				info, err := e.Describe(op, ast)
				op.Done()
				if err != nil {
					t.Fatal(err)
				}
				expected = &info
			}
			lateTx := transactionBegin(t, late)
			sessionSQL(t, late, "LOCK TABLE late.t IN ACCESS EXCLUSIVE MODE")
			lateHeld := true
			defer func() {
				if lateHeld {
					transactionEnd(t, late, lateTx, false)
				}
			}()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type outcome struct {
				err      error
				imported bool
				rows     bool
				metadata bool
			}
			done := make(chan outcome, 1)
			go func() {
				var out outcome
				op, err := waiter.Acquire(ctx)
				if err == nil {
					var scope *StatementScope
					scope, err = e.StartStatement(op)
					if err == nil {
						scope.Expected = expected
						if kind == "prepare-select" || kind == "prepare-insert" {
							info, describeErr := scope.Describe(ast)
							err = describeErr
							out.metadata = len(info.Results) != 0 || info.Parameters.Count != 0
						} else {
							count := 0
							if insert {
								count = 1
							}
							r, executeErr := scope.Execute(ast, count, func(_ context.Context, info ParameterInfo, frame []sqltype.Value, _ sqltype.InputEnvironment) error {
								out.imported = true
								var importErr error
								frame[0], importErr = sqltype.Integer(info.Types[0].Type, 11)
								return importErr
							})
							err = executeErr
							out.rows = r.Rows != nil
							if r.Rows != nil {
								r.Rows.Close()
							}
						}
						err = scope.Finish(err)
					}
					op.Done()
				}
				out.err = err
				done <- out
			}()
			joined := false
			defer func() {
				cancel()
				if !joined {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("waiter cleanup did not terminate")
					}
				}
			}()
			lockPending(t, e, 1)
			lockState := e.locks.Inspect(late.transactions.ticket)
			queuedOnLate := false
			for ref := range lockState.Journals[late.transactions.ticket] {
				entry := lockState.Resources[ref]
				queuedOnLate = entry != nil && len(entry.Pending) == 1
			}
			if !queuedOnLate {
				t.Fatal("waiter did not queue on the exact late AX resource")
			}
			sessionSQL(t, setup, "CREATE TABLE early.t(id INT)")
			sessionSQL(t, setup, "INSERT INTO early.t VALUES(9)")
			earlyTx := transactionBegin(t, early)
			sessionSQL(t, early, "LOCK TABLE early.t IN ACCESS EXCLUSIVE MODE")
			earlyHeld := true
			defer func() {
				if earlyHeld {
					transactionEnd(t, early, earlyTx, false)
				}
			}()
			transactionEnd(t, late, lateTx, false)
			lateHeld = false
			select {
			case out := <-done:
				joined = true
				if !errors.Is(out.err, ErrCatalogReplaced) || out.imported || out.rows || out.metadata {
					t.Errorf("shadow binding: err=%v imported=%t rows=%t metadata=%t", out.err, out.imported, out.rows, out.metadata)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("waiter did not reject changed resolution")
			}
			lockPending(t, e, 0)
			transactionRows(t, early, "SELECT id FROM early.t", 9)
			transactionEnd(t, early, earlyTx, false)
			earlyHeld = false
			transactionRows(t, setup, "SELECT id FROM early.t", 9)
			transactionRows(t, setup, "SELECT id FROM late.t", 7)
			if waiter.TransactionState() != TxIdle {
				t.Fatal("waiter did not finish implicit transaction")
			}
			lockEmpty(t, e)
		})
	}
}
