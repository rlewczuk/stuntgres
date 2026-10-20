package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func TestRowLockRelationAuthority(t *testing.T) {
	for _, clause := range rowLockTestClauses {
		t.Run(clause, func(t *testing.T) {
			e, a, b, _ := rowLockEngineFixture(t)
			ar := transactionBegin(t, a)
			op := transactionOperation(t, a)
			ast := statement(t, "SELECT v FROM t WHERE id=99 "+clause)
			if _, err := e.Describe(op, ast); err != nil {
				t.Fatal(err)
			}
			op.Done()
			lockState := e.locks.Inspect()
			rowHolds := 0
			relationHolds := 0
			for resource, entry := range lockState.Resources {
				held := entry.Holders[a.Transactions.ticket].Mode
				if resource.Row != 0 && held != 0 {
					rowHolds++
				}
				if resource.Row == 0 && held == lock.RowShare {
					relationHolds++
				}
			}
			if rowHolds != 0 || relationHolds != 1 {
				t.Fatal("prepare authority", rowHolds, relationHolds)
			}
			transactionRows(t, a, "SELECT v FROM t WHERE id=99 "+clause)
			br := transactionBegin(t, b)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := lockAsync(e, b, ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", false)
			lockPending(t, e, 1)
			cancel()
			lockAwait(t, done, context.Canceled)
			transactionEnd(t, b, br, false)
			transactionEnd(t, a, ar, false)
			lockEmpty(t, e)
		})
	}
}
func TestRowLockTimeouts(t *testing.T) {
	for _, setting := range []string{"lock_timeout", "statement_timeout"} {
		t.Run(setting, func(t *testing.T) {
			e, a, b, _ := rowLockEngineFixture(t)
			ar := transactionBegin(t, a)
			transactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR UPDATE", 10)
			sessionSQL(t, b, "SET "+setting+"='20ms'")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			op, _, err := b.AcquireCommand(ctx)
			if err != nil {
				t.Fatal(err)
			}
			ast := statement(t, "SELECT v FROM t WHERE id=1 FOR KEY SHARE")
			done := make(chan deleteOutcome, 1)
			go func() {
				result, err := e.Execute(op, ast, 0, nil)
				err = op.CommandError(err)
				op.Done()
				done <- deleteOutcome{result, err}
				close(done)
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case remaining := <-done:
					if remaining.result.Rows != nil {
						remaining.result.Rows.Close()
					}
				case <-time.After(5 * time.Second):
					t.Error("SQL timeout waiter did not join")
				}
			})
			var outcome deleteOutcome
			select {
			case outcome = <-done:
			case <-ctx.Done():
				t.Fatal("SQL timeout did not precede bounded caller deadline", ctx.Err())
			}
			code, message := "55P03", "canceling statement due to lock timeout"
			if setting == "statement_timeout" {
				code, message = "57014", "canceling statement due to statement timeout"
			}
			waitsDiagnostic(t, outcome.err, code, message)
			sessionSQL(t, b, "RESET "+setting)
			transactionEnd(t, a, ar, false)
			lockEmpty(t, e)
		})
	}
}
func TestRowLockDeadlock(t *testing.T) {
	e, a, b, _ := rowLockEngineFixture(t)
	ar, br := transactionBegin(t, a), transactionBegin(t, b)
	transactionRows(t, a, "SELECT v FROM t WHERE id=1 FOR KEY SHARE", 10)
	transactionRows(t, b, "SELECT v FROM t WHERE id=2 FOR UPDATE", 20)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first := deleteAsync(t, a, ctx, "SELECT v FROM t WHERE id=2 FOR KEY SHARE")
	rowLockQueued(t, e, b.Transactions.ticket, lock.ForKeyShare)
	second := deleteAsync(t, b, ctx, "SELECT v FROM t WHERE id=1 FOR UPDATE")
	var outcome deleteOutcome
	var remaining <-chan deleteOutcome
	select {
	case outcome = <-first:
		remaining = second
	case outcome = <-second:
		remaining = first
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var survivor deleteOutcome
	select {
	case survivor = <-remaining:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Finish releases the victim's locks before its worker reports the error;
	// the successful survivor may therefore be received first. Join both and
	// identify the victim by its error, not channel completion order.
	if outcome.err == nil {
		outcome, survivor = survivor, outcome
	}
	if outcome.result.Rows != nil {
		defer outcome.result.Rows.Close()
	}
	if survivor.result.Rows != nil {
		defer survivor.result.Rows.Close()
	}
	var diagnostic *sqlstate.Error
	if !errors.As(outcome.err, &diagnostic) || diagnostic.Code != "40P01" || diagnostic.Message != "deadlock detected" || diagnostic.Hint != "See server log for query details." || diagnostic.Position != "" || diagnostic.Context != "" || !strings.Contains(diagnostic.Detail, "waits for KeyShare on relation ") || !strings.Contains(diagnostic.Detail, "waits for Update on relation ") {
		t.Fatal(outcome.err, diagnostic)
	}
	if outcome.result.Rows != nil {
		outcome.result.Rows.Close()
		t.Fatal("deadlock exposed cursor")
	}
	if survivor.err != nil {
		t.Fatal(survivor.err)
	}
	if survivor.result.Rows == nil {
		t.Fatal("missing survivor result")
	}
	survivor.result.Rows.Close()
	transactionEnd(t, a, ar, false)
	transactionEnd(t, b, br, false)
	lockEmpty(t, e)
}
