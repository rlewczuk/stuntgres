package engine

import (
	"errors"
	"fmt"
	"strings"
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
			committed := e.storage[ref.Address].versions
			frontier := e.commitSeq
			tx := transactionBegin(t, s)
			sessionSQL(t, s, "UPDATE t SET v=11 WHERE k<=64")
			prior := s.transactions.writes[ref.Address]
			op, scope, b := conflictUpdateAnalyzedScope(t, e, s, "INSERT INTO t VALUES(1,1) ON CONFLICT(k) DO UPDATE SET v=t.v+excluded.v")
			evaluation := EvalContext{Context: op.Context(), Scope: 1, Frame: 1, Input: scope.input, Output: scope.output, statementScope: scope}
			integer := func(n int64) sqltype.Value {
				value, err := sqltype.Integer(b.relation.columns[0].Usage.Type, n)
				if err != nil {
					t.Fatal(err)
				}
				return value
			}
			var pinned []rowVersion
			for k := 1; k <= size*2; k++ {
				proposal := []sqltype.Value{integer(int64(k)), integer(1)}
				if admitted, err := scope.admitInsertRow(ref, &indexWriteCandidate{values: proposal}, indexWriteBatch{}, &evaluation); admitted || err != nil {
					t.Fatal("distinct mixed effect", k, admitted, err)
				}
				stage := scope.stage.rows[ref.Address]
				if len(stage) != k || b.conflict.affected != k || len(b.conflict.affectedRows) != k || b.conflict.affectedRows[stage[k-1].row] != k-1 {
					t.Fatal("append order/identity lookup/count", k, len(stage), b.conflict.affected)
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
					if row.row != base.row || row.base != base.version || row.version == base.version {
						t.Fatal("original committed base through prior own", k, row, base)
					}
				} else if row.base != 0 {
					t.Fatal("fresh identity base", row)
				}
				if testExport(row.values[1]) != want {
					t.Fatal("exact replacement payload", k, row)
				}
			}
			stage := scope.stage.rows[ref.Address]
			if pinned[0].version != stage[0].version || testExport(pinned[0].values[1]) != int64(12) {
				t.Fatal("retained current-command prefix mutated", pinned)
			}
			for _, row := range prior {
				if testExport(row.values[1]) != int64(11) {
					t.Fatal("retained prior-own root mutated", row)
				}
			}
			for _, row := range committed {
				if testExport(row.values[1]) != int64(10) {
					t.Fatal("retained committed root mutated", row)
				}
			}
			var finishErr error
			if duplicate {
				_, finishErr = scope.admitInsertRow(ref, &indexWriteCandidate{values: []sqltype.Value{integer(1), integer(1)}}, indexWriteBatch{}, &evaluation)
				var d *sqlstate.Error
				if !errors.As(finishErr, &d) || d.Code != "21000" || len(scope.stage.rows[ref.Address]) != size*2 || b.conflict.affected != size*2 {
					t.Fatal("duplicate does not append or count", finishErr, b.conflict.affected)
				}
			}
			if err := scope.Finish(finishErr); err != finishErr {
				t.Fatal(err)
			}
			if err := scope.Finish(nil); err != finishErr {
				t.Fatal("once-only Finish result", err)
			}
			op.Done()
			if e.commitSeq != frontier {
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
				if e.commitSeq != frontier+1 {
					t.Fatal("not exactly one transaction publication", e.commitSeq, frontier)
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
	bound := &binding.keys[0]
	manager := f.e.locks
	indexRef := ref
	indexRef.Address = bound.index.address
	oldKey := insertConflictUnitRow(t, f, 10)[:1]
	newKey := insertConflictUnitRow(t, f, 20)[:1]
	if err := scope.acquireUnique(indexRef, bound, oldKey); err != nil {
		t.Fatal(err)
	}
	// UniqueKey requests admit only uniqueClaim. Stronger modes belong to
	// actual logical-row resources, not a fabricated mode on an interned key.
	strong := lockResource{ObjectRef: ref, row: 98}
	if err := manager.acquire(scope.op.Context(), scope.ticket, strong, rowUpdate); err != nil {
		t.Fatal(err)
	}
	manager.beginUniqueDelta(scope.ticket)
	defer manager.endUniqueDelta(scope.ticket)
	if err := scope.acquireUnique(indexRef, bound, oldKey); err != nil {
		t.Fatal(err)
	}
	if err := scope.acquireUnique(indexRef, bound, newKey); err != nil {
		t.Fatal(err)
	}
	row := lockResource{ObjectRef: ref, row: 99}
	if err := manager.acquire(scope.op.Context(), scope.ticket, row, rowUpdate); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	recorded := len(scope.ticket.uniqueDelta)
	manager.mu.Unlock()
	if recorded != 1 {
		t.Fatal("delta contains prior unique or non-key grants", recorded)
	}
	manager.restoreUniqueDelta(scope.ticket)
	held := manager.checkpoint(scope.ticket)
	if held[strong].mode != rowUpdate || held[row].mode != rowUpdate {
		t.Fatal("stronger/new row grants lost", held)
	}
	keys := manager.checkpointUnique(scope.ticket, nil)
	for _, mode := range keys {
		if mode.mode != uniqueClaim || len(mode.multi) != 0 {
			t.Fatal("prior key mode lost", keys)
		}
	}
	if len(keys) != 1 {
		t.Fatal("prior unique claim lost or fresh candidate retained", keys)
	}
	// The same mark remains active across a retry; accepted keys survive end.
	if err := scope.acquireUnique(indexRef, bound, newKey); err != nil {
		t.Fatal(err)
	}
	manager.endUniqueDelta(scope.ticket)
	if manager.checkpoint(scope.ticket)[strong].mode != rowUpdate || len(manager.checkpointUnique(scope.ticket, nil)) != 2 {
		t.Fatal("accepted candidate or stronger row contribution lost")
	}
	// A later release cannot be resurrected by a stale candidate journal.
	manager.beginUniqueDelta(scope.ticket)
	if err := scope.acquireUnique(indexRef, bound, insertConflictUnitRow(t, f, 30)[:1]); err != nil {
		t.Fatal(err)
	}
	manager.restore(scope.ticket, nil)
	manager.restoreUniqueDelta(scope.ticket)
	if len(manager.checkpoint(scope.ticket)) != 0 {
		t.Fatal("delta resurrected released grants")
	}
}
