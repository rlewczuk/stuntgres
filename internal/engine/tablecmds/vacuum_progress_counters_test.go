package tablecmds_test

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func vacuumProgressInteger(t *testing.T, row []sqltype.Value, column int, want int64) {
	t.Helper()
	got, err := row[column].Integer()
	if err != nil || got != want {
		t.Fatalf("progress column %d=%v (%v), want %d", column, row[column], err, want)
	}
}

func TestVacuumProgressCounters(t *testing.T) {
	for _, completed := range []int{0, 127, 128, 129} {
		t.Run(fmt.Sprintf("inspect_prefix_%d", completed), func(t *testing.T) {
			e := engine.New()
			owner, worker, observer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, owner, "CREATE TABLE progress_prefix(id INT)")
			var q strings.Builder
			q.WriteString("INSERT INTO progress_prefix VALUES")
			for i := range 129 {
				if i != 0 {
					q.WriteByte(',')
				}
				fmt.Fprintf(&q, "(%d)", i)
			}
			sessionSQL(t, owner, q.String())
			task := startVacuumBarrierAfter(t, e, worker, "VACUUM progress_prefix", "storage.InspectVacuum", completed)
			rows := vacuumProgressCapture(t, e, observer)
			if len(rows) != 1 {
				t.Fatal("missing actual inspect row", rows)
			}
			want := int64(completed / 128)
			if completed == 129 {
				want = 2
			}
			vacuumProgressInteger(t, rows[0], 5, 2)
			vacuumProgressInteger(t, rows[0], 6, want)
			vacuumProgressInteger(t, rows[0], 7, 0)
			vacuumProgressInteger(t, rows[0], 9, 129*8)
			vacuumProgressInteger(t, rows[0], 12, 0)
			task.cancel()
			task.join(t, context.Canceled)
		})
	}
	for _, retained := range []bool{false, true} {
		t.Run(fmt.Sprintf("dead_collection_retained_%v", retained), func(t *testing.T) {
			e := engine.New()
			owner, worker, observer, reader := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE progress_dead(id INT)", "INSERT INTO progress_dead VALUES(1),(2),(3),(4),(5),(6)", "CREATE INDEX progress_dead_a ON progress_dead(id)", "CREATE INDEX progress_dead_b ON progress_dead(id)"} {
				sessionSQL(t, owner, q)
			}
			if retained {
				sessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
				transactionRows(t, reader, "SELECT id FROM progress_dead ORDER BY id", 1, 2, 3, 4, 5, 6)
			}
			sessionSQL(t, owner, "DELETE FROM progress_dead WHERE id IN (1,2)")
			task := startVacuumBarrier(t, e, worker, "VACUUM (FREEZE) progress_dead", "storage.MaterializeVacuum")
			rows := vacuumProgressCapture(t, e, observer)
			if len(rows) != 1 {
				t.Fatal(rows)
			}
			dead := int64(2)
			if retained {
				dead = 0
			}
			vacuumProgressInteger(t, rows[0], 9, 48)
			vacuumProgressInteger(t, rows[0], 10, 8*dead)
			vacuumProgressInteger(t, rows[0], 11, dead)
			vacuumProgressInteger(t, rows[0], 12, 0)
			task.join(t, nil)
			if retained {
				sessionSQL(t, reader, "ROLLBACK")
			}
		})
	}
}

// This context observes genuine completed detached passes, outside engine locks.
// It neither supplies provider facts nor adds a production callback.
type vacuumProgressCompletionContext struct {
	context.Context
	visit func()
}

func (c *vacuumProgressCompletionContext) Err() error {
	var pcs [32]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		if strings.Contains(f.Function, "vacuumCompletion") {
			c.visit()
			break
		}
		if !more {
			break
		}
	}
	return c.Context.Err()
}

func TestVacuumProgressRecapture(t *testing.T) {
	e := engine.New()
	owner, worker, observer, peer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE progress_retry(id INT)", "INSERT INTO progress_retry VALUES(1),(2),(3)", "CREATE INDEX progress_retry_a ON progress_retry(id)", "CREATE INDEX progress_retry_b ON progress_retry(id)", "DELETE FROM progress_retry WHERE id=1"} {
		sessionSQL(t, owner, q)
	}
	op, err := worker.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	var snapshots [][][]sqltype.Value
	var callbackErr error
	insert := mustLockStatement("INSERT INTO progress_retry VALUES(4)")
	op.Ctx = &vacuumProgressCompletionContext{Context: op.Context(), visit: func() {
		if callbackErr != nil {
			return
		}
		rows, err := captureVacuumProgress(e, observer)
		if err != nil {
			callbackErr = err
			return
		}
		snapshots = append(snapshots, rows)
		if len(snapshots) == 1 {
			peerOp, err := peer.Acquire(context.Background())
			if err != nil {
				callbackErr = err
				return
			}
			defer peerOp.Done()
			result, err := e.Execute(peerOp, insert, 0, nil)
			callbackErr = err
			if result.Rows != nil {
				if err := result.Rows.Close(); callbackErr == nil {
					callbackErr = err
				}
			}
		}
	}}
	result, err := e.Execute(op, mustLockStatement("VACUUM progress_retry"), 0, nil)
	if result.Rows != nil {
		result.Rows.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	if callbackErr != nil {
		t.Fatal(callbackErr)
	}
	if len(snapshots) != 2 {
		t.Fatal("real peer DML did not force exactly two passes", len(snapshots))
	}
	for i, rows := range snapshots {
		if len(rows) != 1 {
			t.Fatal("missing recapture row", rows)
		}
		vacuumProgressInteger(t, rows[0], 8, int64(i+1))
		for _, column := range []int{10, 11, 12, 13} {
			vacuumProgressInteger(t, rows[0], column, 0)
		}
		vacuumProgressInteger(t, rows[0], 5, 1)
		vacuumProgressInteger(t, rows[0], 6, 1)
		vacuumProgressInteger(t, rows[0], 7, 1)
	}
	vacuumProgressInteger(t, snapshots[0][0], 8, 1)
	if rows := vacuumProgressCapture(t, e, observer); len(rows) != 0 {
		t.Fatal("recapture retained row", rows)
	}
	transactionRows(t, owner, "SELECT id FROM progress_retry ORDER BY id", 2, 3, 4)
}

func TestVacuumProgressFailureCleanup(t *testing.T) {
	e := engine.New()
	owner, worker, observer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE progress_failure(id INT)", "INSERT INTO progress_failure VALUES(1),(2)", "DELETE FROM progress_failure WHERE id=1"} {
		sessionSQL(t, owner, q)
	}
	_, store := vacuumStore(t, e, "progress_failure")
	// Real exhausted-generation authority makes a changed detached pass fail.
	store.Generation = ^uint64(0)
	root, ends, stats := store.Versions, store.Ends, store.Maintenance
	task := startVacuumBarrier(t, e, worker, "VACUUM progress_failure", "storage.InspectVacuum")
	rows := vacuumProgressCapture(t, e, observer)
	if len(rows) != 1 {
		t.Fatal("failure never registered", rows)
	}
	task.barrier.resume()
	err := <-task.done
	task.joined = true
	if err == nil || err.Error() != "stuntgres: storage generation exhausted" {
		t.Fatal("not genuine ordinary generation failure", err)
	}
	if rows := vacuumProgressCapture(t, e, observer); len(rows) != 0 {
		t.Fatal("failed command retained row", rows)
	}
	if !storage.SameVersionRoot(root, store.Versions) || store.Ends != ends || store.Maintenance != stats || store.Generation != ^uint64(0) {
		t.Fatal("ordinary failure installed candidate/completion")
	}
	vacuumProgressInteger(t, rows[0], 8, 0)
	lockEmpty(t, e)
}
