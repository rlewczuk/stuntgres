package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestReferenceRangePartitionLifecycle(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec("CREATE TABLE rp_tx(k INT) PARTITION BY RANGE(k)", 0))
		run(referenceExec("CREATE TABLE rp_tx_a PARTITION OF rp_tx FOR VALUES FROM(1) TO(3)", 0))
		run(referenceExec("CREATE TABLE rp_tx_b PARTITION OF rp_tx FOR VALUES FROM(3) TO(5)", 0))
		for _, commit := range []bool{false, true} {
			tx, err := b.db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceRunStep(t, p.ctx, tx, b.recorder, referencePrepared(referenceExec("INSERT INTO rp_tx VALUES(1),(3)", 2)))
			referenceRunStep(t, p.ctx, tx, b.recorder, referenceRangeRows("SELECT k FROM rp_tx ORDER BY k", 1, 3))
			run(referenceRangeRows("SELECT k FROM rp_tx ORDER BY k"))
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		run(referenceRangeRows("SELECT k FROM rp_tx ORDER BY k", 1, 3))
		tx, err := b.db.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		txrun := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, b.recorder, s) }
		txrun(referenceExec("SAVEPOINT rp_before", 0))
		txrun(referenceRangeMiss("INSERT INTO rp_tx VALUES(2),(4),(5)", "rp_tx", "k", "5"))
		s := referenceImplicitError("SELECT k FROM rp_tx", referenceTransactionAborted())
		s.selectRows = true
		txrun(s)
		txrun(referenceExec("ROLLBACK TO rp_before", 0))
		txrun(referenceRangeRows("SELECT k FROM rp_tx ORDER BY k", 1, 3))
		txrun(referencePrepared(referenceExec("INSERT INTO rp_tx VALUES($1),($2)", 2, 2, 4)))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		run(referenceRangeRows("SELECT k FROM rp_tx ORDER BY k", 1, 2, 3, 4))
		if !b.pg {
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			for _, q := range []string{"CREATE TABLE rp_canceled PARTITION OF rp_tx DEFAULT", "INSERT INTO rp_tx VALUES(1),(3)", "DROP TABLE rp_tx"} {
				result, err := b.db.ExecContext(ctx, q)
				if result != nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("precanceled result=%v error=%v", result, err)
				}
			}
			run(referenceRangeRows("SELECT k FROM rp_tx ORDER BY k", 1, 2, 3, 4))
			run(referenceSelect("SELECT COUNT(*) FROM pg_class WHERE relname='rp_canceled'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
	})
}

// PostgreSQL has observable server lock admission. Native queue/default-scan
// cancellation is independently covered by production-phase engine units;
// native hierarchy LOCK TABLE is an explicit capability boundary.
func TestReferenceRangePartitionAdmittedCancellation(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec("CREATE TABLE rp_wait(k INT) PARTITION BY RANGE(k)", 0))
		run(referenceExec("CREATE TABLE rp_wait_a PARTITION OF rp_wait FOR VALUES FROM(1) TO(3)", 0))
		if !b.pg {
			tx, err := b.db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceRunStep(t, p.ctx, tx, b.recorder, referenceRangeError("LOCK TABLE rp_wait IN ACCESS EXCLUSIVE MODE", "0A000", "locking partition hierarchies is unsupported", referenceDiagnostic{}))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			return
		}
		waiter, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer waiter.Close()
		holder, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		hold, err := holder.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer hold.Rollback()
		if _, err := hold.ExecContext(p.ctx, "LOCK TABLE rp_wait IN ACCESS EXCLUSIVE MODE"); err != nil {
			t.Fatal(err)
		}
		var pid int
		if err := waiter.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
		defer cancel()
		type outcome struct {
			result sql.Result
			err    error
		}
		result := make(chan outcome, 1)
		joined := false
		defer func() {
			cancel()
			hold.Rollback()
			if !joined {
				select {
				case <-result:
				case <-time.After(5 * time.Second):
					t.Error("cancel worker did not join")
				}
			}
		}()
		go func() {
			r, e := waiter.ExecContext(ctx, "CREATE TABLE rp_wait_b PARTITION OF rp_wait FOR VALUES FROM(3) TO(5)")
			result <- outcome{r, e}
		}()
		for {
			var admitted bool
			err := referenceAdmin.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation='rp_wait'::regclass AND mode='AccessExclusiveLock' AND NOT granted)", pid).Scan(&admitted)
			if err != nil {
				t.Fatal(err)
			}
			if admitted {
				break
			}
			select {
			case got := <-result:
				joined = true
				t.Fatalf("CREATE completed before admission: %v", got.err)
			default:
			}
			runtime.Gosched()
		}
		var signaled bool
		if err := referenceAdmin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
			t.Fatalf("cancel=%t err=%v", signaled, err)
		}
		var got outcome
		select {
		case got = <-result:
			joined = true
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if got.result != nil {
			t.Fatal("canceled CREATE exposed result")
		}
		referenceAssertDiagnostic(t, got.err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
		if err := hold.Rollback(); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, waiter, b.recorder, referenceExec("CREATE TABLE rp_wait_b PARTITION OF rp_wait FOR VALUES FROM(3) TO(5)", 0))
		referenceRunStep(t, p.ctx, waiter, b.recorder, referenceExec("INSERT INTO rp_wait VALUES(1),(3)", 2))
		referenceRunStep(t, p.ctx, waiter, b.recorder, referenceRangeRows("SELECT k FROM rp_wait ORDER BY k", 1, 3))
	})
}

func TestReferenceRangePartitionDropClosure(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
		run(referenceExec("CREATE TABLE rp_drop(k INT) PARTITION BY RANGE(k)", 0))
		run(referenceExec("CREATE TABLE rp_drop_mid PARTITION OF rp_drop DEFAULT PARTITION BY RANGE(k)", 0))
		run(referenceExec("CREATE TABLE rp_drop_leaf PARTITION OF rp_drop_mid DEFAULT", 0))
		run(referenceExec("INSERT INTO rp_drop VALUES(1),(NULL)", 2))
		run(referenceExec("DROP TABLE rp_drop", 0))
		run(referenceSelect("SELECT COUNT(*) FROM pg_class WHERE relname='rp_drop' OR relname='rp_drop_mid' OR relname='rp_drop_leaf'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(referenceExec("CREATE TABLE rp_drop(k INT) PARTITION BY RANGE(k)", 0))
		run(referenceExec("CREATE TABLE rp_drop_leaf PARTITION OF rp_drop DEFAULT", 0))
		run(referenceExec("CREATE VIEW rp_drop_view AS SELECT * FROM rp_drop", 0))
		run(referenceRangeError("DROP TABLE rp_drop RESTRICT", "2BP01", "cannot drop table rp_drop because other objects depend on it", referenceDiagnostic{Detail: "view rp_drop_view depends on table rp_drop", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
		run(referenceSelect("SELECT relname FROM pg_class WHERE relname='rp_drop' OR relname='rp_drop_leaf' OR relname='rp_drop_view' ORDER BY relname", []string{"relname"}, []string{"NAME"}, [][]any{{"rp_drop"}, {"rp_drop_leaf"}, {"rp_drop_view"}}))
		run(referenceExec("DROP TABLE rp_drop CASCADE", 0))
		run(referenceSelect("SELECT COUNT(*) FROM pg_class WHERE relname='rp_drop' OR relname='rp_drop_leaf' OR relname='rp_drop_view'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}
