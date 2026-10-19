package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqltype"
)

func TestSQLCursorHoldPublication(t *testing.T) {
	t.Run("independent_promotion_before_failure", func(t *testing.T) {
		s := cursorUnitSetup(t)
		sessionSQL(t, s, "DECLARE old CURSOR WITH HOLD FOR SELECT i FROM unit_rows ORDER BY i")
		sessionSQL(t, s, "COMMIT")
		sessionSQL(t, s, "BEGIN")
		sessionSQL(t, s, "DECLARE good CURSOR WITH HOLD FOR SELECT i FROM unit_rows ORDER BY i")
		sessionSQL(t, s, "DECLARE bad CURSOR WITH HOLD FOR SELECT i FROM unit_rows ORDER BY i")
		sessionSQL(t, s, "DECLARE ordinary CURSOR FOR SELECT i FROM unit_rows")
		good, bad := s.cursors.entries["good"], s.cursors.entries["bad"]
		goodPin, badPin, badCell := good.source.view, bad.source.view, bad.authority
		original, calls := bad.root.expressions[0], 0
		sentinel := errors.New("remaining projection failed")
		bad.root.expressions[0] = queryProbe(original.ResultUsage(), func(ctx *EvalContext) (sqltype.Value, error) {
			calls++
			if calls == 2 {
				return sqltype.Value{}, sentinel
			}
			return original.Eval(ctx)
		})
		sessionSQL(t, s, "INSERT INTO unit_rows VALUES(4)")
		result, err := cursorUnitExecute(t, s, context.Background(), "COMMIT")
		if !errors.Is(err, sentinel) || result.Rows != nil || result.Count != 0 || calls != 2 || !good.promoted || s.cursors.entries["good"] != good || s.cursors.entries["bad"] != nil || s.cursors.entries["ordinary"] != nil || bad.values != nil || bad.source != nil || badPin.Load() != nil || goodPin.Load() != nil || badCell.scope != nil || s.transactions.state != TxIdle {
			t.Fatal("failed end retracts promotion or publishes pending store", result, err, calls)
		}
		cursorUnitBatch(t, s, "FETCH ALL FROM good", [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}})
		cursorUnitBatch(t, s, "FETCH 1 FROM old", [][]driver.Value{{int64(1)}})
		if got := tuples(sessionSQL(t, s, "SELECT i FROM unit_rows ORDER BY i")); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}}) {
			t.Fatal("failed commit publishes private write", got)
		}
	})
	t.Run("promotion_before_real_ssi_publication", func(t *testing.T) {
		e := New()
		s, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE hold_ssi(class INT,v INT)")
		sessionSQL(t, s, "INSERT INTO hold_ssi VALUES(1,10),(2,20)")
		sessionSQL(t, s, "CREATE SEQUENCE hold_ssi_draw START 10 CACHE 1")
		sessionSQL(t, s, "BEGIN ISOLATION LEVEL SERIALIZABLE")
		sessionSQL(t, peer, "BEGIN ISOLATION LEVEL SERIALIZABLE")
		sessionSQL(t, s, "SELECT v FROM hold_ssi WHERE class=1").Rows.Close()
		sessionSQL(t, peer, "SELECT v FROM hold_ssi WHERE class=2").Rows.Close()
		sessionSQL(t, s, "INSERT INTO hold_ssi VALUES(2,30)")
		sessionSQL(t, peer, "INSERT INTO hold_ssi VALUES(1,40)")
		sessionSQL(t, s, "DECLARE h CURSOR WITH HOLD FOR SELECT nextval('hold_ssi_draw') AS n")
		c := s.cursors.entries["h"]
		original, calls := c.root.expressions[0], 0
		c.root.expressions[0] = queryProbe(original.ResultUsage(), func(eval *EvalContext) (sqltype.Value, error) {
			calls++
			return original.Eval(eval)
		})
		sessionSQL(t, peer, "COMMIT")
		before := serializableSnapshotPublication(e)
		result, err := cursorUnitExecute(t, s, context.Background(), "COMMIT")
		serializableUnitDiagnostic(t, err, "Canceled on identification as a pivot, during commit attempt.")
		after := serializableSnapshotPublication(e)
		if result.Rows != nil || calls != 1 || !c.promoted || c.count != 1 || c.source != nil || c.authority != nil || e.catalog.state != before.catalog || e.visibility.commitSeq != before.frontier || !reflect.DeepEqual(before.storage, after.storage) || !reflect.DeepEqual(before.indexes, after.indexes) {
			t.Fatal("final SSI failure retracts export or publishes private storage", result, calls)
		}
		cursorUnitBatch(t, s, "FETCH ALL FROM h", [][]driver.Value{{int64(10)}})
		cursorUnitBatch(t, s, "FETCH ALL FROM h", [][]driver.Value{})
		if calls != 1 {
			t.Fatal("post-publication-failure FETCH reevaluates sequence target", calls)
		}
		if got := tuples(sessionSQL(t, s, "SELECT last_value,is_called FROM hold_ssi_draw")); !reflect.DeepEqual(got, [][]driver.Value{{int64(10), true}}) {
			t.Fatal("failed publication changes consumed sequence effects", got)
		}
	})
	t.Run("implicit_install_is_not_completion", func(t *testing.T) {
		s := cursorUnitSetup(t)
		sessionSQL(t, s, "ROLLBACK")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		op, err := s.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		scope, err := s.Engine().StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		defer scope.Finish(context.Canceled)
		result, err := scope.Execute(statement(t, "DECLARE implicit_h CURSOR WITH HOLD FOR SELECT i FROM unit_rows"), 0, nil)
		if err != nil || result.Rows != nil || scope.completed || !scope.sessionCompleted {
			t.Fatal("installation classified as external completion", result, err)
		}
		c := s.cursors.entries["implicit_h"]
		pin, cell := c.source.view, c.authority
		cancel()
		if err := scope.Finish(nil); !errors.Is(err, context.Canceled) {
			t.Fatal("implicit materialization cancellation suppressed", err)
		}
		if s.cursors.entries["implicit_h"] != nil || pin.Load() != nil || cell.scope != nil || s.transactions.state != TxIdle {
			t.Fatal("failed implicit declaration retains producer")
		}
	})
	t.Run("late_cancel_keeps_successful_export", func(t *testing.T) {
		s := cursorUnitSetup(t)
		sessionSQL(t, s, "DECLARE h CURSOR WITH HOLD FOR SELECT i FROM unit_rows ORDER BY i")
		sessionSQL(t, s, "INSERT INTO unit_rows VALUES(4)")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		op, err := s.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		result, err := s.Engine().Commit(op, s.transactions.ref)
		if err != nil || !result.Committed {
			op.Done()
			t.Fatal(result, err)
		}
		cancel()
		op.Done()
		cursorUnitBatch(t, s, "FETCH ALL FROM h", [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}})
		if got := tuples(sessionSQL(t, s, "SELECT i FROM unit_rows ORDER BY i")); len(got) != 4 {
			t.Fatal("late cancel retracts durable publication", got)
		}
	})
}

func TestSQLCursorHoldCancellation(t *testing.T) {
	for _, sequenceWait := range []bool{false, true} {
		name := "projection"
		if sequenceWait {
			name = "sequence_wait"
		}
		t.Run(name, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			holder := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE unit_rows(i INT NOT NULL)")
			sessionSQL(t, s, "INSERT INTO unit_rows VALUES(1),(2),(3)")
			sessionSQL(t, s, "CREATE SEQUENCE hold_draw START 10 CACHE 1")
			sessionSQL(t, s, "DECLARE old CURSOR WITH HOLD FOR SELECT i FROM unit_rows ORDER BY i")
			sessionSQL(t, s, "BEGIN")
			query := "SELECT i FROM unit_rows ORDER BY i"
			if sequenceWait {
				query = "SELECT nextval('hold_draw') AS n FROM unit_rows"
			}
			sessionSQL(t, s, "DECLARE h CURSOR WITH HOLD FOR "+query)
			c, ref := s.cursors.entries["h"], s.transactions.ref
			ticket := s.transactions.ticket
			pin, cell := c.source.view, c.authority
			sessionSQL(t, s, "INSERT INTO unit_rows VALUES(4)")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			entered := make(chan struct{})
			if sequenceWait {
				holderRef := transactionBegin(t, holder)
				sessionSQL(t, holder, "ALTER SEQUENCE hold_draw RESTART 20")
				defer transactionEnd(t, holder, holderRef, false)
			} else {
				original, calls := c.root.expressions[0], 0
				c.root.expressions[0] = queryProbe(original.ResultUsage(), func(eval *EvalContext) (sqltype.Value, error) {
					calls++
					if calls == 2 {
						close(entered)
						<-eval.Context.Done()
						return sqltype.Value{}, eval.Context.Err()
					}
					return original.Eval(eval)
				})
			}
			done := make(chan error, 1)
			joined := false
			defer func() {
				cancel()
				if !joined {
					lockAwait(t, done, context.Canceled)
				}
			}()
			go func() {
				op, err := s.Acquire(ctx)
				if err == nil {
					_, err = e.Commit(op, ref)
					op.Done()
				}
				done <- err
			}()
			if sequenceWait {
				lockPending(t, e, 1)
				lockState := e.locks.Inspect()
				var pending *lock.Waiter
				for _, entry := range lockState.Resources {
					for i := range entry.Pending {
						pending = &entry.Pending[i]
					}
				}
				correct := pending != nil && pending.Provenance == ref && pending.Ticket == ticket
				if !correct {
					t.Fatal("materialization does not wait under original transaction ticket")
				}
			} else {
				select {
				case <-entered:
				case err := <-done:
					joined = true
					t.Fatal("commit finished before actual projection barrier", err)
				case <-ctx.Done():
					t.Fatal("projection never entered", ctx.Err())
				}
			}
			cancel()
			lockAwait(t, done, context.Canceled)
			joined = true
			if c.promoted || c.values != nil || c.source != nil || c.stage != nil || pin.Load() != nil || cell.scope != nil || s.cursors.entries["h"] != nil || s.transactions.ticket != nil || s.transactions.state != TxIdle {
				t.Fatal("canceled commit retains partial store/producer/admission")
			}
			s.mu.Lock()
			retired := s.timeouts.authority == (ids.TxRef{}) && s.timeouts.transaction.deadline.IsZero()
			s.mu.Unlock()
			if !retired {
				t.Fatal("failed materialization retains transaction timeout authority")
			}
			cursorUnitBatch(t, s, "FETCH 1 FROM old", [][]driver.Value{{int64(1)}})
			if got := tuples(sessionSQL(t, s, "SELECT i FROM unit_rows ORDER BY i")); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}}) {
				t.Fatal("canceled commit publishes private write", got)
			}
			s.RequestEnd(CleanClose)
			if err := s.WaitEnd(context.Background()); err != nil || len(s.cursors.entries) != 0 {
				t.Fatal("session end retains old exported store", err)
			}
		})
	}
}
