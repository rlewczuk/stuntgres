package engine

import (
	"errors"
	"fmt"
	"strings"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func conflictUpdateBatchValues(size int, value int64) string {
	var rows strings.Builder
	for k := 1; k <= size; k++ {
		if k != 1 {
			rows.WriteByte(',')
		}
		fmt.Fprintf(&rows, "(%d,%d)", k, value)
	}
	return rows.String()
}

func TestInsertConflictUpdateDistinctStage(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprintf("duplicate_%t", duplicate), func(t *testing.T) {
			const size = 128
			e := New()
			s := sessionStart(t, e, nil)
			observer := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(k INT UNIQUE,v INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES"+conflictUpdateBatchValues(size, 10))
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			committed := e.storage.Relations[ref.Address].Versions
			frontier := e.visibility.CommitSeq()
			tx := transactionBegin(t, s)
			sessionSQL(t, s, "UPDATE t SET v=11 WHERE k<=64")
			prior := s.Transactions.Writes[ref.Address]
			op, scope, b := conflictUpdateAnalyzedScope(t, e, s, "INSERT INTO t VALUES(1,1) ON CONFLICT(k) DO UPDATE SET v=t.v+excluded.v")
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Input: scope.input, Output: scope.output, Host: hostOf(scope)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
			integer := func(n int64) sqltype.Value {
				value, err := sqltype.Integer(b.Relation.Columns[0].Usage.Type, n)
				if err != nil {
					t.Fatal(err)
				}
				return value
			}
			var pinned []storage.RowVersion
			for k := 1; k <= size*2; k++ {
				proposal := []sqltype.Value{integer(int64(k)), integer(1)}
				if admitted, err := scope.admitInsertRow(ref, &index.WriteCandidate{Values: proposal}, index.WriteBatch{}, &evaluation); admitted || err != nil {
					t.Fatal("distinct mixed effect", k, admitted, err)
				}
				stage := scope.stage.rows[ref.Address]
				if len(stage) != k || scope.insertConflict.affected != k || len(scope.insertConflict.affectedRows) != k || scope.insertConflict.affectedRows[stage[k-1].Row] != k-1 {
					t.Fatal("append order/identity lookup/count", k, len(stage), scope.insertConflict.affected)
				}
				if k == 1 {
					pinned = stage[:1]
				}
				row := stage[k-1]
				want := int64(1)
				if k <= size {
					want = 11
					if k <= 64 {
						want = 12
					}
					base := committed[k-1]
					if row.Row != base.Row || row.Base != base.Version || row.Version == base.Version {
						t.Fatal("original committed base through prior own", k, row, base)
					}
				} else if row.Base != 0 {
					t.Fatal("fresh identity base", row)
				}
				if testExport(row.Values[1]) != want {
					t.Fatal("exact replacement payload", k, row)
				}
			}
			stage := scope.stage.rows[ref.Address]
			if pinned[0].Version != stage[0].Version || testExport(pinned[0].Values[1]) != int64(12) {
				t.Fatal("retained current-command prefix mutated", pinned)
			}
			for _, row := range prior {
				if testExport(row.Values[1]) != int64(11) {
					t.Fatal("retained prior-own root mutated", row)
				}
			}
			for _, row := range committed {
				if testExport(row.Values[1]) != int64(10) {
					t.Fatal("retained committed root mutated", row)
				}
			}
			var finishErr error
			if duplicate {
				_, finishErr = scope.admitInsertRow(ref, &index.WriteCandidate{Values: []sqltype.Value{integer(1), integer(1)}}, index.WriteBatch{}, &evaluation)
				var d *sqlstate.Error
				if !errors.As(finishErr, &d) || d.Code != "21000" || len(scope.stage.rows[ref.Address]) != size*2 || scope.insertConflict.affected != size*2 {
					t.Fatal("duplicate does not append or count", finishErr, scope.insertConflict.affected)
				}
			}
			if err := scope.Finish(finishErr); err != finishErr {
				t.Fatal(err)
			}
			if err := scope.Finish(nil); err != finishErr {
				t.Fatal("once-only Finish result", err)
			}
			op.Done()
			if e.visibility.CommitSeq() != frontier {
				t.Fatal("statement published before transaction commit")
			}
			if duplicate {
				transactionEnd(t, s, tx, false)
				want := make([]int64, size)
				for k := range want {
					want[k] = 10
				}
				transactionRows(t, observer, "SELECT v FROM t ORDER BY k", want...)
			} else {
				want := make([]int64, size*2)
				for k := range want {
					want[k] = 1
					if k < size {
						want[k] = 11
					}
					if k < 64 {
						want[k] = 12
					}
				}
				transactionRows(t, s, "SELECT v FROM t ORDER BY k", want...)
				transactionEnd(t, s, tx, true)
				if e.visibility.CommitSeq() != frontier+1 {
					t.Fatal("not exactly one transaction publication", e.visibility.CommitSeq(), frontier)
				}
				transactionRows(t, observer, "SELECT v FROM t ORDER BY k", want...)
			}
			lockEmpty(t, e)
		})
	}
}

func TestInsertConflictUpdateUniqueDelta(t *testing.T) {
	f := insertConflictUniqueFixture(t)
	scope, ref := insertConflictUnitScope(t, f)
	binding, err := scope.bindUniqueWrite(ref)
	if err != nil {
		t.Fatal(err)
	}
	bound := &binding.Keys[0]
	manager := f.e.locks
	indexRef := ref
	indexRef.Address = bound.Index.Address()
	oldKey := insertConflictUnitRow(t, f, 10)[:1]
	newKey := insertConflictUnitRow(t, f, 20)[:1]
	if err := scope.writeHost().ClaimUniqueKey(indexRef, bound, oldKey); err != nil {
		t.Fatal(err)
	}
	// UniqueKey requests admit only uniqueClaim. Stronger modes belong to
	// actual logical-row resources, not a fabricated mode on an interned key.
	strong := lock.Resource{ObjectRef: ref, Row: 98}
	if err := manager.Acquire(scope.op.Context(), scope.ticket, strong, lock.ForUpdate); err != nil {
		t.Fatal(err)
	}
	manager.BeginUniqueDelta(scope.ticket)
	defer manager.EndUniqueDelta(scope.ticket)
	if err := scope.writeHost().ClaimUniqueKey(indexRef, bound, oldKey); err != nil {
		t.Fatal(err)
	}
	if err := scope.writeHost().ClaimUniqueKey(indexRef, bound, newKey); err != nil {
		t.Fatal(err)
	}
	row := lock.Resource{ObjectRef: ref, Row: 99}
	if err := manager.Acquire(scope.op.Context(), scope.ticket, row, lock.ForUpdate); err != nil {
		t.Fatal(err)
	}
	recorded := len(manager.Inspect().UniqueDeltas[scope.ticket])
	if recorded != 1 {
		t.Fatal("delta contains prior unique or non-key grants", recorded)
	}
	manager.RestoreUniqueDelta(scope.ticket)
	held := manager.Checkpoint(scope.ticket)
	if held[strong].Mode != lock.ForUpdate || held[row].Mode != lock.ForUpdate {
		t.Fatal("stronger/new row grants lost", held)
	}
	keys := manager.CheckpointUnique(scope.ticket, nil)
	for _, mode := range keys {
		if mode.Mode != lock.UniqueClaim || len(mode.Multi) != 0 {
			t.Fatal("prior key mode lost", keys)
		}
	}
	if len(keys) != 1 {
		t.Fatal("prior unique claim lost or fresh candidate retained", keys)
	}
	// The same mark remains active across a retry; accepted keys survive end.
	if err := scope.writeHost().ClaimUniqueKey(indexRef, bound, newKey); err != nil {
		t.Fatal(err)
	}
	manager.EndUniqueDelta(scope.ticket)
	if manager.Checkpoint(scope.ticket)[strong].Mode != lock.ForUpdate || len(manager.CheckpointUnique(scope.ticket, nil)) != 2 {
		t.Fatal("accepted candidate or stronger row contribution lost")
	}
	// A later release cannot be resurrected by a stale candidate journal.
	manager.BeginUniqueDelta(scope.ticket)
	if err := scope.writeHost().ClaimUniqueKey(indexRef, bound, insertConflictUnitRow(t, f, 30)[:1]); err != nil {
		t.Fatal(err)
	}
	manager.Restore(scope.ticket, nil)
	manager.RestoreUniqueDelta(scope.ticket)
	if len(manager.Checkpoint(scope.ticket)) != 0 {
		t.Fatal("delta resurrected released grants")
	}
}
