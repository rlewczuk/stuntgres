package tablecmds_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestVacuumProgressWork(t *testing.T) {
	t.Run("no_index_collection_consumed", func(t *testing.T) {
		e := engine.New()
		owner, worker, observer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
		for _, q := range []string{"CREATE TABLE progress_noindex(id INT)", "INSERT INTO progress_noindex VALUES(1),(2)", "DELETE FROM progress_noindex WHERE id=1"} {
			sessionSQL(t, owner, q)
		}
		task := startVacuumBarrier(t, e, worker, "VACUUM progress_noindex", "vacuumCompletion")
		rows := vacuumProgressCapture(t, e, observer)
		if len(rows) != 1 {
			t.Fatal(rows)
		}
		for _, i := range []int{8, 10, 11, 12, 13} {
			vacuumProgressInteger(t, rows[0], i, 0)
		}
		vacuumProgressInteger(t, rows[0], 6, 1)
		vacuumProgressInteger(t, rows[0], 7, 1)
		task.join(t, nil)
	})
	t.Run("second_root_completed_prefix", func(t *testing.T) {
		e := engine.New()
		owner, worker, observer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
		for _, q := range []string{"CREATE TABLE progress_root_prefix(id INT)", "INSERT INTO progress_root_prefix VALUES(1),(2),(3)", "CREATE INDEX progress_root_prefix_a ON progress_root_prefix(id)", "CREATE INDEX progress_root_prefix_b ON progress_root_prefix(id)", "DELETE FROM progress_root_prefix WHERE id=1"} {
			sessionSQL(t, owner, q)
		}
		// Three real entry checks and the final cancellation check complete the
		// first remap; the fifth check enters the second real root.
		task := startVacuumBarrierAfter(t, e, worker, "VACUUM progress_root_prefix", "storage.RemapVacuumIndex", 4)
		rows := vacuumProgressCapture(t, e, observer)
		if len(rows) != 1 {
			t.Fatal(rows)
		}
		vacuumProgressInteger(t, rows[0], 12, 2)
		vacuumProgressInteger(t, rows[0], 13, 1)
		vacuumProgressInteger(t, rows[0], 8, 0)
		_, store := vacuumStore(t, e, "progress_root_prefix")
		stats := store.Maintenance
		task.cancel()
		task.join(t, context.Canceled)
		if store.Maintenance != stats {
			t.Fatal("partial loop cancellation counted completion")
		}
		vacuumProgressInteger(t, rows[0], 8, 0)
	})
	t.Run("empty_unchanged", func(t *testing.T) {
		e := engine.New()
		owner, worker, observer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
		sessionSQL(t, owner, "CREATE TABLE progress_empty(id INT)")
		task := startVacuumBarrier(t, e, worker, "VACUUM progress_empty", "vacuumCompletion")
		rows := vacuumProgressCapture(t, e, observer)
		if len(rows) != 1 {
			t.Fatal(rows)
		}
		for i := 5; i < 14; i++ {
			vacuumProgressInteger(t, rows[0], i, 0)
		}
		task.join(t, nil)
	})
	for _, completed := range []int{0, 127, 128, 129} {
		t.Run(fmt.Sprintf("materialize_prefix_%d", completed), func(t *testing.T) {
			e := engine.New()
			owner, worker, observer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, owner, "CREATE TABLE progress_materialize(id INT)")
			var q strings.Builder
			q.WriteString("INSERT INTO progress_materialize VALUES")
			for i := range 129 {
				if i != 0 {
					q.WriteByte(',')
				}
				fmt.Fprintf(&q, "(%d)", i)
			}
			sessionSQL(t, owner, q.String())
			sessionSQL(t, owner, "DELETE FROM progress_materialize WHERE id=0")
			task := startVacuumBarrierAfter(t, e, worker, "VACUUM progress_materialize", "storage.MaterializeVacuum", completed)
			rows := vacuumProgressCapture(t, e, observer)
			if len(rows) != 1 {
				t.Fatal(rows)
			}
			want := int64(completed / 128)
			if completed == 129 {
				want = 2
			}
			vacuumProgressInteger(t, rows[0], 5, 2)
			vacuumProgressInteger(t, rows[0], 6, 2)
			vacuumProgressInteger(t, rows[0], 7, want)
			vacuumProgressInteger(t, rows[0], 8, 0)
			vacuumProgressInteger(t, rows[0], 11, 1)
			task.join(t, nil)
		})
	}
	for _, tc := range []struct {
		name, method, function string
		freeze                 bool
		total                  int64
	}{
		{"two_btree", "btree", "storage.RemapVacuumIndex", false, 2},
		{"freeze_hash_selective", "hash", "storage.RemapVacuumIndex", true, 1},
		{"freeze_brin_selective", "brin", "RebuildBrinVacuumRoot", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			owner, worker, observer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE progress_indexes(id INT)", "INSERT INTO progress_indexes VALUES(1),(2),(3)", "CREATE INDEX progress_indexes_a ON progress_indexes(id)", "CREATE INDEX progress_indexes_b ON progress_indexes USING " + tc.method + "(id)"} {
				sessionSQL(t, owner, q)
			}
			q := "VACUUM progress_indexes"
			if tc.freeze {
				q = "VACUUM (FREEZE) progress_indexes"
			} else {
				sessionSQL(t, owner, "DELETE FROM progress_indexes WHERE id=1")
			}
			task := startVacuumBarrier(t, e, worker, q, tc.function)
			rows := vacuumProgressCapture(t, e, observer)
			if len(rows) != 1 {
				t.Fatal(rows)
			}
			vacuumProgressInteger(t, rows[0], 12, tc.total)
			vacuumProgressInteger(t, rows[0], 13, 0)
			vacuumProgressInteger(t, rows[0], 8, 0)
			task.join(t, nil)
		})
	}
}

func TestVacuumProgressIndependentWorkers(t *testing.T) {
	e := engine.New()
	owner, a, b, observer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, name := range []string{"progress_worker_a", "progress_worker_b"} {
		sessionSQL(t, owner, "CREATE TABLE "+name+"(id INT)")
		sessionSQL(t, owner, "INSERT INTO "+name+" VALUES(1),(2)")
		sessionSQL(t, owner, "DELETE FROM "+name+" WHERE id=1")
	}
	first := startVacuumBarrier(t, e, a, "VACUUM progress_worker_a", "storage.InspectVacuum")
	second := startVacuumBarrier(t, e, b, "VACUUM progress_worker_b", "storage.InspectVacuum")
	rows := vacuumProgressCapture(t, e, observer)
	if len(rows) != 2 {
		t.Fatal("not an unordered two-worker bag", rows)
	}
	seen := map[int64]bool{}
	for _, row := range rows {
		pid, err := row[0].Integer()
		if err != nil || seen[pid] {
			t.Fatal("duplicate/invalid PID", rows)
		}
		seen[pid] = true
	}
	if !seen[int64(a.SessionToken.ID())] || !seen[int64(b.SessionToken.ID())] {
		t.Fatal("not actual worker identities", seen)
	}
	first.cancel()
	first.join(t, context.Canceled)
	remaining := vacuumProgressCapture(t, e, observer)
	if len(remaining) != 1 {
		t.Fatal("cross-worker cleanup", remaining)
	}
	vacuumProgressInteger(t, remaining[0], 0, int64(b.SessionToken.ID()))
	second.join(t, nil)
	if rows := vacuumProgressCapture(t, e, observer); len(rows) != 0 {
		t.Fatal(rows)
	}
	if len(rows) != 2 {
		t.Fatal("retained snapshot mutated")
	}
}

func TestVacuumProgressReplacement(t *testing.T) {
	e := engine.New()
	owner, worker, observer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, owner, "CREATE TABLE progress_replace(id INT)")
	sessionSQL(t, owner, "INSERT INTO progress_replace VALUES(1)")
	task := startVacuumBarrier(t, e, worker, "VACUUM progress_replace", "storage.InspectVacuum")
	retained := vacuumProgressCapture(t, e, observer)
	if len(retained) != 1 {
		t.Fatal(retained)
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	count := len(e.VacuumProgress)
	e.Mu.Unlock()
	if count != 0 {
		t.Fatal("replacement retained progress", count)
	}
	task.join(t, ids.ErrCatalogReplaced)
	e.Mu.Lock()
	count = len(e.VacuumProgress)
	e.Mu.Unlock()
	if count != 0 {
		t.Fatal("late callback resurrected replaced progress", count)
	}
	if retained[0][4].IsNull() {
		t.Fatal("replacement changed retained typed snapshot")
	}
}

func TestVacuumProgressTargets(t *testing.T) {
	e := engine.New()
	owner, worker, observer := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, name := range []string{"progress_target_a", "progress_target_b"} {
		for _, q := range []string{"CREATE TABLE " + name + "(id INT)", "INSERT INTO " + name + " VALUES(1),(2)", "CREATE INDEX " + name + "_ix ON " + name + "(id)", "DELETE FROM " + name + " WHERE id=1"} {
			sessionSQL(t, owner, q)
		}
	}
	var expected []ids.OID
	for _, name := range []string{"progress_target_a", "progress_target_b"} {
		ref, _ := vacuumStore(t, e, name)
		expected = append(expected, ref.Address.ObjectID)
	}
	op, err := worker.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	var snapshots [][][]sqltype.Value
	var callbackErr error
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
	}}
	result, err := e.Execute(op, mustLockStatement("VACUUM progress_target_a,progress_target_b"), 0, nil)
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
		t.Fatal("missing actual target", len(snapshots))
	}
	for i, rows := range snapshots {
		if len(rows) != 1 {
			t.Fatal(rows)
		}
		relid, err := rows[0][3].OID()
		if err != nil || relid != uint32(expected[i]) {
			t.Fatal("target identity did not turn over", rows)
		}
		vacuumProgressInteger(t, rows[0], 8, 1)
		vacuumProgressInteger(t, rows[0], 11, 0)
	}
	if rows := vacuumProgressCapture(t, e, observer); len(rows) != 0 {
		t.Fatal(rows)
	}
}
