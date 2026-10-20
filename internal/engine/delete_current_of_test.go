package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
)

func TestDeleteCurrentOfIdentity(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "DECLARE c CURSOR FOR SELECT 99 AS projected FROM unit_rows ORDER BY i FOR UPDATE")
	c := s.cursors.entries["c"]
	cursorUnitBatch(t, s, "FETCH FORWARD 2 FROM c", [][]driver.Value{{int64(99)}, {int64(99)}})
	if c.authority.scope != nil || c.currentSource == nil || c.position != cursorOn {
		t.Fatal("FETCH retained admission or lost physical occurrence", c)
	}
	ref := catalogRef(t, s.Engine(), s.Engine().CatalogRead(), "unit_rows")
	row := cursorOccurrenceVersion(c.currentSource, c.currentIndex, 0, c.locks)
	s.Engine().mu.Lock()
	want := s.Engine().storage.Relations[ref.Address].Versions[1].Row
	s.Engine().mu.Unlock()
	if row.Row != want {
		t.Fatal("last returned occurrence is not physical row2", row.Row, want)
	}
	deleteCount(t, s, "DELETE FROM unit_rows WHERE CURRENT OF c", 1)
	if cursorOccurrenceVersion(c.currentSource, c.currentIndex, 0, c.locks).Row != want {
		t.Fatal("DELETE moves or clears current identity")
	}
	deleteCount(t, s, "DELETE FROM unit_rows WHERE CURRENT OF c", 0)
	cursorUnitBatch(t, s, "FETCH FORWARD 1 FROM c", [][]driver.Value{{int64(99)}})
	if c.position != cursorOn || c.currentSource == nil {
		t.Fatal("bounded final row probed EOF")
	}
	cursorUnitBatch(t, s, "FETCH FORWARD 1 FROM c", [][]driver.Value{})
	if c.position != cursorAfter || c.currentSource != nil {
		t.Fatal("EOF retains current physical authority")
	}
}

func TestDeleteCurrentOfOccurrenceVersions(t *testing.T) {
	old := storage.RowVersion{Row: 3, Version: 4}
	effective := storage.RowVersion{Row: 3, Version: 9}
	leaf := &querySource{occurrence: 2, committed: []storage.RowVersion{old}}
	locks := &queryLockContext{leaves: make([]queryLockLeaf, 3)}
	locks.leaves[2].source = leaf
	materialized := &querySource{materialized: true, occurrences: []int{1, 2}, provenance: []int{-1, 0}, versions: []storage.RowVersion{{}, effective}}
	if got := cursorOccurrenceVersion(materialized, 0, 2, locks); got.Row != effective.Row || got.Version != effective.Version {
		t.Fatal("effective rowmark version lost", got)
	}
	bridge := &querySource{lineage: &queryLockLineage{stage: materialized, selection: []int{0}}}
	if got := cursorOccurrenceVersion(bridge, 0, 2, locks); got.Version != effective.Version {
		t.Fatal("lineage version lost", got)
	}
	materialized.versionValid = []bool{false}
	if got := cursorOccurrenceVersion(bridge, 0, 2, locks); got.Version != old.Version {
		t.Fatal("sparse cache did not use physical provenance", got)
	}
	if got := cursorOccurrenceVersion(materialized, 0, 1, locks); got.Row != 0 {
		t.Fatal("absent occurrence substituted another leaf", got)
	}
	if got := cursorOccurrenceVersion(bridge, absentQueryIndex, 2, locks); got.Row != 0 {
		t.Fatal("absent bridge gained identity", got)
	}
}

func TestDeleteCurrentOfEligibility(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "DECLARE c CURSOR FOR SELECT i FROM (SELECT i FROM unit_rows) d")
	c := s.cursors.entries["c"]
	scan := cursorSimpleScan(c.root, c.source)
	if scan == nil || scan.view == nil || scan.ref.Address != c.locks.leaves[0].leaf.ref.Address {
		t.Fatal("transparent derived query loses physical scan")
	}
	cursorUnitBatch(t, s, "FETCH FORWARD 1 FROM c", [][]driver.Value{{int64(1)}})
	if got := cursorOccurrenceVersion(c.currentSource, c.currentIndex, scan.occurrence, c.locks); got.Row == 0 {
		t.Fatal("unlocked derived lineage lost identity")
	}
	for _, node := range []LogicalNode{&sortNode{Child: c.root}, &aggregateNode{Child: c.root}, &joinNode{}, &setNode{}, &distinctNode{}, &singletonNode{}} {
		if cursorSimpleScan(node, c.source) != nil {
			t.Fatalf("identity-losing %T accepted", node)
		}
	}
	for _, entry := range c.locks.leaves {
		if entry.mark.Mode != 0 {
			t.Fatal("unlocked lineage acquired rowmark authority")
		}
	}
}

func TestDeleteCurrentOfPublication(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "DECLARE c CURSOR FOR SELECT i FROM unit_rows WHERE i=1 FOR UPDATE")
	cursorUnitBatch(t, s, "FETCH FORWARD 1 FROM c", [][]driver.Value{{int64(1)}})
	sessionSQL(t, s, "INSERT INTO unit_rows VALUES(4)")
	sessionSQL(t, s, "SAVEPOINT recover")
	cursor := s.cursors.entries["c"]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := s.Engine().StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	result, err := scope.Execute(statement(t, "DELETE FROM unit_rows WHERE CURRENT OF c RETURNING i"), 0, nil)
	if result.Rows != nil {
		defer result.Rows.Close()
	}
	if err != nil || result.Count != 1 || scope.sessionCompleted || len(scope.stage.deletes) == 0 {
		scope.Finish(context.Canceled)
		op.Done()
		t.Fatal("positioned DELETE bypasses ordinary write staging", result, err)
	}
	if len(s.transactions.deletes) != 0 {
		scope.Finish(context.Canceled)
		op.Done()
		t.Fatal("Execute prematurely publishes deletion")
	}
	cancel()
	err = scope.Finish(nil)
	op.Done()
	if !errors.Is(err, context.Canceled) || len(s.transactions.deletes) != 0 {
		t.Fatal("canceled Finish published private deletion", err)
	}
	sessionSQL(t, s, "ROLLBACK TO recover")
	if s.cursors.entries["c"] != cursor || cursor.position != cursorOn || cursor.authority.scope != nil {
		t.Fatal("mutation cancellation failed healthy cursor")
	}
	transactionRows(t, s, "SELECT i FROM unit_rows ORDER BY i", 1, 2, 3, 4)
	deleteCount(t, s, "DELETE FROM unit_rows WHERE CURRENT OF c", 1)
	transactionRows(t, s, "SELECT i FROM unit_rows ORDER BY i", 2, 3, 4)
}

func TestDeleteCurrentOfArbitration(t *testing.T) {
	e := New()
	holder := sessionStart(t, e, nil)
	worker := sessionStart(t, e, nil)
	sessionSQL(t, worker, "CREATE TABLE t(v INT)")
	sessionSQL(t, worker, "INSERT INTO t VALUES(7),(7)")
	sessionSQL(t, worker, "BEGIN")
	sessionSQL(t, worker, "DECLARE c CURSOR FOR SELECT 99 FROM t")
	cursorUnitBatch(t, worker, "FETCH FORWARD 1 FROM c", [][]driver.Value{{int64(99)}})
	cursor := worker.cursors.entries["c"]
	identity := cursorOccurrenceVersion(cursor.currentSource, cursor.currentIndex, 0, cursor.locks)
	tx := transactionBegin(t, holder)
	deleteCount(t, holder, "DELETE FROM t WHERE v=7", 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := deleteAsync(t, worker, ctx, "DELETE FROM t WHERE CURRENT OF c")
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("positioned waiter did not join")
			}
		}
	}()
	lockPending(t, e, 1)
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	lockState := e.locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: ref, Row: identity.Row}]
	queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Ticket == worker.transactions.ticket && entry.Pending[0].Mode == lock.ForUpdate
	if !queued {
		t.Fatal("positioned DELETE did not wait on exact captured duplicate identity")
	}
	transactionEnd(t, holder, tx, false)
	deleteAwait(t, done, 1, nil)
	joined = true
	if !reflect.DeepEqual(tuples(sessionSQL(t, worker, "SELECT v FROM t")), [][]driver.Value{{int64(7)}}) {
		t.Fatal("arbitration deleted equal substitute")
	}
}

func TestDeleteCurrentOfLifetime(t *testing.T) {
	s := cursorUnitSetup(t)
	sessionSQL(t, s, "DECLARE c CURSOR FOR SELECT FROM unit_rows FOR UPDATE")
	cursorUnitBatch(t, s, "FETCH FORWARD 1 FROM c", [][]driver.Value{{}})
	old := s.cursors.entries["c"]
	if old.currentSource == nil {
		t.Fatal("zero-width result lost identity")
	}
	sessionSQL(t, s, "SAVEPOINT keep")
	deleteCount(t, s, "DELETE FROM unit_rows WHERE CURRENT OF c", 1)
	sessionSQL(t, s, "ROLLBACK TO keep")
	if s.cursors.entries["c"] != old || old.currentSource == nil {
		t.Fatal("rollback rewinds or discards older current identity")
	}
	sessionSQL(t, s, "CLOSE c")
	if old.currentSource != nil || old.source != nil || old.authority.scope != nil {
		t.Fatal("dispose retains execution authority")
	}
	sessionSQL(t, s, "DECLARE c CURSOR FOR SELECT i FROM unit_rows")
	fresh := s.cursors.entries["c"]
	if fresh == old || fresh.currentSource != nil || fresh.position != cursorBefore {
		t.Fatal("name incarnation inherited current identity")
	}
	sessionSQL(t, s, "ROLLBACK")
	if len(s.cursors.entries) != 0 || fresh.currentSource != nil {
		t.Fatal("transaction end retained cursor")
	}
}

func TestDeleteCurrentOfJoinIdentityRetention(t *testing.T) {
	for _, tc := range []struct {
		name, setup, query string
		join, marked       bool
	}{
		{"unmarked_join", "", "SELECT a.i FROM unit_rows a JOIN unit_rows b ON a.i=b.i", true, false},
		{"unmarked_derived_join", "", "SELECT i FROM (SELECT a.i FROM unit_rows a JOIN unit_rows b ON a.i=b.i) d", true, false},
		{"unmarked_view_join", "CREATE VIEW joined AS SELECT a.i FROM unit_rows a JOIN unit_rows b ON a.i=b.i", "SELECT i FROM joined", true, false},
		{"marked_join", "", "SELECT a.i FROM unit_rows a JOIN unit_rows b ON a.i=b.i FOR UPDATE OF a", true, true},
		{"marked_descendant_derived_join", "CREATE VIEW marked AS SELECT i FROM unit_rows FOR UPDATE", "SELECT i FROM (SELECT a.i FROM marked a JOIN unit_rows b ON a.i=b.i) d", true, true},
		{"marked_descendant_view_join", "CREATE VIEW marked AS SELECT a.i FROM unit_rows a JOIN unit_rows b ON a.i=b.i FOR UPDATE OF a", "SELECT i FROM marked", true, true},
		{"unmarked_derived_scan", "", "SELECT i FROM (SELECT i FROM unit_rows) d", false, false},
		{"unmarked_view_scan", "CREATE VIEW visible AS SELECT i FROM unit_rows", "SELECT i FROM visible", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := sessionStart(t, New(), nil)
			sessionSQL(t, s, "CREATE TABLE unit_rows(i INT NOT NULL)")
			sessionSQL(t, s, "INSERT INTO unit_rows VALUES(1),(2),(3)")
			if tc.setup != "" {
				sessionSQL(t, s, tc.setup)
			}
			sessionSQL(t, s, "BEGIN")
			sessionSQL(t, s, "DECLARE c CURSOR FOR "+tc.query)
			c := s.cursors.entries["c"]
			if c.locks == nil || c.locks.rowMarked() != tc.marked || c.state.locking != tc.marked {
				t.Fatal("identity manifest changed semantic rowmark requirement")
			}
			cursorUnitBatch(t, s, "FETCH FORWARD 2 FROM c", [][]driver.Value{{int64(1)}, {int64(2)}})
			source := c.currentSource
			for source != nil {
				if source.input != nil {
					source = source.input
				} else if source.lineage != nil {
					source = source.lineage.stage
				} else {
					break
				}
			}
			if source == nil || c.authority.scope != nil || c.position != cursorOn {
				t.Fatal("FETCH lost retained lineage or retained admission")
			}
			if tc.join {
				if !source.materialized || source.flatWidth != 2 || source.flatCount != 2 {
					t.Fatal("bounded FETCH did not retain the actual two-row join frontier")
				}
				if tc.marked {
					if len(source.occurrences) != 2 || len(source.provenance) != 4 || len(source.versions) != 4 ||
						len(source.joinPairs) != 2 || source.joinLeft == nil || source.joinRight == nil {
						t.Fatal("actual rowmarks lost effective-version/recheck provenance")
					}
				} else {
					if source.occurrences != nil || source.provenance != nil || source.versions != nil ||
						source.versionValid != nil || source.joinPairs != nil || source.joinLeft != nil || source.joinRight != nil {
						t.Fatal("identity-only cursor allocated ineligible join caches")
					}
					cursorUnitBatch(t, s, "FETCH ALL FROM c", [][]driver.Value{{int64(3)}})
					cursorUnitError(t, s, "DELETE FROM unit_rows WHERE CURRENT OF c", "24000",
						`cursor "c" is not a simply updatable scan of table "unit_rows"`)
					return
				}
			}
			occurrence := -1
			if tc.marked {
				for i, leaf := range c.locks.leaves {
					if leaf.mark.Mode != 0 {
						if occurrence != -1 {
							t.Fatal("fixture has ambiguous physical rowmarks")
						}
						occurrence = i
					}
				}
			} else {
				scan := cursorSimpleScan(c.root, c.source)
				if scan == nil {
					t.Fatal("identity-only transparent scan lost eligibility")
				}
				occurrence = scan.occurrence
			}
			if occurrence < 0 {
				t.Fatal("physical target occurrence missing")
			}
			leaf := c.locks.leaves[occurrence]
			row := cursorOccurrenceVersion(c.currentSource, c.currentIndex, occurrence, c.locks)
			s.Engine().mu.Lock()
			want := s.Engine().storage.Relations[leaf.leaf.ref.Address].Versions[1].Row
			s.Engine().mu.Unlock()
			if row.Row != want {
				t.Fatal("retained lineage lost last-FETCH physical identity", row.Row, want)
			}
			deleteCount(t, s, "DELETE FROM unit_rows WHERE CURRENT OF c", 1)
			transactionRows(t, s, "SELECT i FROM unit_rows ORDER BY i", 1, 3)
		})
	}
}
