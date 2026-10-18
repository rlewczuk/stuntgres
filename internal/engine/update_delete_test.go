package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
)

// This worker executes the real command and Finish; its owner cancels and joins
// before session cleanup, even when an assertion terminates the subtest.
func updateDeleteAsync(t *testing.T, e *Engine, s *Session, parent context.Context, q string) func() (Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	type outcome struct {
		result Result
		err    error
	}
	done := make(chan outcome, 1)
	joined := false
	go func() {
		op, err := s.Acquire(ctx)
		var result Result
		if err == nil {
			result, err = e.Execute(op, mustLockStatement(q), 0, nil)
			op.Done()
		}
		done <- outcome{result, err}
	}()
	await := func() (Result, error) {
		t.Helper()
		select {
		case o := <-done:
			joined = true
			return o.result, o.err
		case <-time.After(5 * time.Second):
			t.Fatal("mixed DML worker not joined")
			return Result{}, context.DeadlineExceeded
		}
	}
	t.Cleanup(func() {
		cancel()
		if !joined {
			await()
		}
	})
	return await
}
func updateDeleteExecute(t *testing.T, e *Engine, s *Session, q string, n int64) {
	t.Helper()
	result, err := updateDeleteAsync(t, e, s, context.Background(), q)()
	if err != nil || result.Count != n || result.Rows != nil {
		t.Fatal(q, result, err, n)
	}
}

func TestUpdateCoreDeleteRowContention(t *testing.T) {
	for _, lane := range []string{"delete_blocks_update", "update_changes_predicate", "update_latest_delete"} {
		for _, commit := range []bool{false, true} {
			t.Run(lane+map[bool]string{false: "/rollback", true: "/commit"}[commit], func(t *testing.T) {
				e := New()
				holder := sessionStart(t, e, nil)
				worker := sessionStart(t, e, nil)
				peer := sessionStart(t, e, nil)
				sessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
				sessionSQL(t, peer, "INSERT INTO t VALUES(1,10),(2,20)")
				h := transactionBegin(t, holder)
				w := transactionBegin(t, worker)
				hold, q := "DELETE FROM t WHERE id=1", "UPDATE t SET v=30 WHERE v=10"
				if lane != "delete_blocks_update" {
					hold = "UPDATE t SET v=20 WHERE id=1"
					q = "DELETE FROM t WHERE v=10"
				}
				if lane == "update_latest_delete" {
					q = "DELETE FROM t WHERE id=1"
				}
				sessionSQL(t, holder, hold)
				await := updateDeleteAsync(t, e, worker, context.Background(), q)
				lockPending(t, e, 1)
				ref := catalogRef(t, e, e.CatalogRead(), "t")
				resource := lockResource{ObjectRef: ref, Row: e.storage[ref.Address].versions[0].row}
				lockState := e.locks.Inspect()
				entry := lockState.Resources[resource]
				actual := entry != nil && len(entry.Pending) == 1 && entry.Holders[holder.transactions.ticket].Mode&(rowUpdate|noKeyUpdate) != 0
				if !actual {
					t.Fatal("not a shared logical-row arbitration wait")
				}
				transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20)
				if lane == "update_latest_delete" {
					updateDeleteExecute(t, e, peer, "UPDATE t SET v=21 WHERE id=2", 1)
				} else {
					updateDeleteExecute(t, e, peer, "UPDATE t SET v=10 WHERE id=2", 1)
				}
				transactionEnd(t, holder, h, commit)
				result, err := await()
				want := int64(1)
				if commit && lane != "update_latest_delete" {
					want = 0
				}
				if err != nil || result.Count != want || result.Rows != nil {
					t.Fatal(result, err, want)
				}
				transactionEnd(t, worker, w, true)
				switch lane {
				case "delete_blocks_update":
					if commit {
						transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10)
					} else {
						transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 30, 10)
					}
				case "update_changes_predicate":
					if commit {
						transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 20, 10)
					} else {
						transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10)
					}
				case "update_latest_delete":
					transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 21)
					store := e.storage[ref.Address]
					head := store.versions[0]
					if store.ends.rows[head.row].version != head.version {
						t.Fatal("deleted captured rather than latest version")
					}
				}
				lockEmpty(t, e)
			})
		}
	}
}

func TestUpdateCoreDeleteCandidateIdentity(t *testing.T) {
	for _, deletedBefore := range []bool{false, true} {
		t.Run(map[bool]string{false: "captured_before_delete", true: "visible_after_delete"}[deletedBefore], func(t *testing.T) {
			e := New()
			owner := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			sessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
			sessionSQL(t, peer, "INSERT INTO t VALUES(1,10),(2,20),(3,30)")
			retained := sessionSQL(t, peer, "SELECT v FROM t ORDER BY id").Rows
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			store := e.storage[ref.Address]
			oldGeneration := store.generation
			oldEnds := store.ends
			row := store.versions[2].row
			if deletedBefore {
				sessionSQL(t, peer, "DELETE FROM t WHERE id=1")
			}
			op, scope, b, source := updateBoundSource(t, e, owner, context.Background(), "UPDATE t SET v=33 WHERE id=3")
			defer func() {
				source.close()
				scope.Finish(context.Canceled)
				op.Done()
			}()
			if deletedBefore && source.rawIndex(1) != 2 {
				t.Fatal("visible index confused with physical slot")
			}
			if !deletedBefore {
				sessionSQL(t, peer, "DELETE FROM t WHERE id=1")
			}
			if len(store.versions) != 3 || store.versions[2].row != row || store.generation <= oldGeneration || store.ends == oldEnds {
				t.Fatal("DELETE compacted or failed to advance guard")
			}
			result, err := e.executeUpdate(op.ctx, scope, b, source, nil, nil, "public")
			if err != nil || result.Count != 1 {
				t.Fatal(result, err)
			}
			if err = scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
			op.Done()
			transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 20, 33)
			if got := tuples(Result{Rows: retained}); !reflect.DeepEqual(got, [][]driver.Value{{int64(10)}, {int64(20)}, {int64(30)}}) {
				t.Fatal("retained source changed", got)
			}
			if err := retained.Close(); err != nil {
				t.Fatal(err)
			}
			lockEmpty(t, e)
		})
	}
}

func TestUpdateCoreDeleteCancellation(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "delete_wait", true: "update_wait"}[reverse], func(t *testing.T) {
			e := New()
			holder := sessionStart(t, e, nil)
			owner := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			sessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
			sessionSQL(t, peer, "INSERT INTO t VALUES(1,10),(2,20),(3,30)")
			h := transactionBegin(t, holder)
			o := transactionBegin(t, owner)
			q := "DELETE FROM t WHERE id=3"
			if reverse {
				sessionSQL(t, holder, "DELETE FROM t WHERE id=3")
				sessionSQL(t, owner, "DELETE FROM t WHERE id=1")
				sessionSQL(t, owner, "UPDATE t SET v=22 WHERE id=2")
				q = "UPDATE t SET v=33 WHERE id=3"
			} else {
				sessionSQL(t, holder, "UPDATE t SET v=99 WHERE id=3")
				sessionSQL(t, owner, "UPDATE t SET v=20 WHERE id=1")
				sessionSQL(t, owner, "DELETE FROM t WHERE id=2")
			}
			ticket := owner.transactions.ticket
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			await := updateDeleteAsync(t, e, owner, ctx, q)
			lockPending(t, e, 1)
			cancel()
			result, err := await()
			if !errors.Is(err, context.Canceled) || result.Count != 0 || result.Rows != nil {
				t.Fatal(result, err)
			}
			if owner.TransactionState() != TxAborted || len(owner.transactions.writes) != 0 || len(owner.transactions.deletes) != 0 {
				t.Fatal("mixed private work survived abort")
			}
			lockState := e.locks.Inspect(ticket)
			held := len(lockState.Journals[ticket])
			if held != 0 {
				t.Fatal("mixed row grants survived", held)
			}
			transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20, 30)
			if reverse {
				updateDeleteExecute(t, e, peer, "DELETE FROM t WHERE id=1", 1)
				updateDeleteExecute(t, e, peer, "UPDATE t SET v=21 WHERE id=2", 1)
			} else {
				updateDeleteExecute(t, e, peer, "UPDATE t SET v=11 WHERE id=1", 1)
				updateDeleteExecute(t, e, peer, "DELETE FROM t WHERE id=2", 1)
			}
			transactionEnd(t, owner, o, false)
			transactionEnd(t, holder, h, false)
			if reverse {
				transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 21, 30)
			} else {
				transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 11, 30)
			}
			lockEmpty(t, e)
		})
	}
}

func TestUpdateCoreDeletePublication(t *testing.T) {
	for _, mode := range []string{"commit", "canceled_final_publication", "restore_fenced"} {
		t.Run(mode, func(t *testing.T) {
			e := New()
			owner := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			sessionSQL(t, peer, "CREATE TABLE t(v INT)")
			sessionSQL(t, peer, "CREATE TABLE u(v INT)")
			sessionSQL(t, peer, "INSERT INTO t VALUES(1)")
			sessionSQL(t, peer, "INSERT INTO u VALUES(2)")
			tr := catalogRef(t, e, e.CatalogRead(), "t")
			ur := catalogRef(t, e, e.CatalogRead(), "u")
			ts, us := e.storage[tr.Address], e.storage[ur.Address]
			roots := [2][]rowVersion{ts.versions, us.versions}
			old := [2]rowVersion{roots[0][0], roots[1][0]}
			ends := [2]*deletionRoot{ts.ends, us.ends}
			frontier := e.commitSeq
			tx := transactionBegin(t, owner)
			sessionSQL(t, owner, "UPDATE t SET v=11")
			sessionSQL(t, owner, "DELETE FROM u")
			ticket := owner.transactions.ticket
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := owner.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			switch mode {
			case "commit":
				_, err = e.Commit(op, tx)
				if err != nil || e.commitSeq != frontier+1 || ts.versions[0].born != e.commitSeq || us.ends.rows[old[1].row].end != e.commitSeq {
					t.Fatal("head/end not one frontier", err)
				}
			case "restore_fenced":
				if err = e.Restore(context.Background(), strings.NewReader("CREATE TABLE t(v INT);CREATE TABLE u(v INT);INSERT INTO t VALUES(91);INSERT INTO u VALUES(92);")); err != nil {
					t.Fatal(err)
				}
				_, err = e.Commit(op, tx)
				if !errors.Is(err, ErrCatalogReplaced) {
					t.Fatal(err)
				}
			case "canceled_final_publication":
				barrier := &transactionBarrierContext{Context: op.Context(), entered: make(chan struct{}), release: make(chan struct{})}
				op.ctx = barrier
				done := make(chan error, 1)
				go func() { _, err := e.Commit(op, tx); done <- err }()
				select {
				case <-barrier.entered:
				case <-time.After(5 * time.Second):
					cancel()
					close(barrier.release)
					lockAwait(t, done, context.Canceled)
					t.Fatal("publication barrier not reached")
				}
				cancel()
				close(barrier.release)
				lockAwait(t, done, context.Canceled)
				if e.commitSeq != frontier || ts.ends != ends[0] || us.ends != ends[1] || &ts.versions[0] != &roots[0][0] || &us.versions[0] != &roots[1][0] {
					t.Fatal("partial canceled head/end installation")
				}
			}
			op.Done()
			if !reflect.DeepEqual(old, [2]rowVersion{roots[0][0], roots[1][0]}) {
				t.Fatal("old prefixes mutated")
			}
			lockState := e.locks.Inspect(ticket)
			held := len(lockState.Journals[ticket])
			if held != 0 {
				t.Fatal("old journal survived")
			}
			if owner.TransactionState() != TxIdle {
				transactionEnd(t, owner, tx, false)
			}
			lockEmpty(t, e)
			switch mode {
			case "commit":
				transactionRows(t, peer, "SELECT v FROM t", 11)
				transactionRows(t, peer, "SELECT v FROM u")
			case "restore_fenced":
				transactionRows(t, peer, "SELECT v FROM t", 91)
				transactionRows(t, peer, "SELECT v FROM u", 92)
			default:
				transactionRows(t, peer, "SELECT v FROM t", 1)
				transactionRows(t, peer, "SELECT v FROM u", 2)
			}
			updateDeleteExecute(t, e, peer, "UPDATE t SET v=3", 1)
			count := int64(1)
			if mode == "commit" {
				count = 0
			}
			updateDeleteExecute(t, e, peer, "DELETE FROM u", count)
			lockEmpty(t, e)
		})
	}
}

func TestUpdateCoreDeleteImportIdentity(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		for _, failure := range []string{"success", "row_exhaustion", "version_exhaustion", "frontier_exhaustion", "canceled"} {
			t.Run(map[bool]string{false: "self/", true: "foreign/"}[foreign]+failure, func(t *testing.T) {
				source := New()
				run(t, source, "CREATE TABLE t(v INT)")
				run(t, source, "INSERT INTO t VALUES(1),(1),(2)")
				retained := run(t, source, "SELECT v FROM t ORDER BY v")
				defer retained.Rows.Close()
				run(t, source, "DELETE FROM t WHERE v=1")
				sr := catalogRef(t, source, source.CatalogRead(), "t")
				oldStore := source.storage[sr.Address]
				oldHead := oldStore.versions[2]
				oldEnds := oldStore.ends
				target := source
				if foreign {
					target = New()
					run(t, target, "CREATE TABLE sentinel(v INT)")
					run(t, target, "INSERT INTO sentinel VALUES(99)")
				}
				switch failure {
				case "row_exhaustion":
					target.nextRow = ^uint64(0)
				case "version_exhaustion":
					target.nextVersion = ^uint64(0)
				case "frontier_exhaustion":
					target.commitSeq = ids.CommitSeq(^uint64(0))
				}
				state, frontier, rowCounter, versionCounter := target.catalog.state, target.commitSeq, target.nextRow, target.nextVersion
				oldStorage := target.storage
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if failure == "canceled" {
					cancel()
				}
				_, err := target.ReplaceCatalog(ctx, source.CatalogRead())
				if failure != "success" {
					if err == nil {
						t.Fatal("failed import accepted")
					}
					if failure == "canceled" && !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
					if target.catalog.state != state || target.commitSeq != frontier || target.nextRow != rowCounter || target.nextVersion != versionCounter {
						t.Fatal("failed import consumed state/identities")
					}
					for address, store := range oldStorage {
						if target.storage[address] != store {
							t.Fatal("failed import installed storage")
						}
					}
				} else {
					tr := catalogRef(t, target, target.CatalogRead(), "t")
					store := target.storage[tr.Address]
					if tr.Epoch != state.epoch+1 || store == oldStore || len(store.versions) != 1 || store.ends != nil || store.versions[0].row == 0 || store.versions[0].version == 0 || store.versions[0].row == oldHead.row || store.versions[0].version == oldHead.version || store.versions[0].born != target.commitSeq {
						t.Fatal("live-only fresh import", store)
					}
					if got := tuples(run(t, target, "SELECT v FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(2)}}) {
						t.Fatal(got)
					}
					if foreign {
						run(t, target, "INSERT INTO t VALUES(3)")
						if got := tuples(run(t, source, "SELECT v FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(2)}}) {
							t.Fatal("foreign append changed source", got)
						}
					}
				}
				if oldStore.ends != oldEnds || oldStore.versions[2].row != oldHead.row || oldStore.versions[2].version != oldHead.version {
					t.Fatal("import mutated source history")
				}
				if got := tuples(retained); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}, {int64(1)}, {int64(2)}}) {
					t.Fatal("import mutated retained source", got)
				}
			})
		}
	}
}

func TestUpdateCoreDeletePrivateCandidateIdentity(t *testing.T) {
	e := New()
	owner := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	sessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
	sessionSQL(t, peer, "INSERT INTO t VALUES(1,10),(2,20),(3,30)")
	tx := transactionBegin(t, owner)
	sessionSQL(t, owner, "UPDATE t SET v=31 WHERE id=3")
	sessionSQL(t, peer, "DELETE FROM t WHERE id=1")
	op, scope, b, source := updateBoundSource(t, e, owner, context.Background(), "UPDATE t SET v=33 WHERE id=3")
	defer func() {
		source.close()
		scope.Finish(context.Canceled)
		op.Done()
	}()
	if source.count() != 2 || source.rawIndex(1) != len(source.committed) || source.versionAt(source.rawIndex(1)).born != 0 {
		t.Fatal("private head not canonical substituted raw occurrence")
	}
	result, err := e.executeUpdate(op.ctx, scope, b, source, nil, nil, "public")
	if err != nil || result.Count != 1 {
		t.Fatal(result, err)
	}
	if err = scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()
	transactionRows(t, owner, "SELECT v FROM t ORDER BY id", 20, 33)
	transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 20, 30)
	transactionEnd(t, owner, tx, true)
	transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 20, 33)
	lockEmpty(t, e)
}

func TestUpdateCoreDeleteFailedReplacement(t *testing.T) {
	for _, mode := range []string{"invalid", "reader", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			e := New()
			owner := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			sessionSQL(t, peer, "CREATE TABLE t(id INT,v INT)")
			sessionSQL(t, peer, "INSERT INTO t VALUES(1,10),(2,20)")
			ref := catalogRef(t, e, e.CatalogRead(), "t")
			store := e.storage[ref.Address]
			root := store.versions
			ends, frontier, epoch := store.ends, e.commitSeq, e.catalog.state.epoch
			tx := transactionBegin(t, owner)
			sessionSQL(t, owner, "UPDATE t SET v=11 WHERE id=1")
			sessionSQL(t, owner, "DELETE FROM t WHERE id=2")
			writes, deletes, ticket := owner.transactions.writes[ref.Address], owner.transactions.deletes[ref.Address], owner.transactions.ticket
			sentinel := errors.New("mixed replacement reader")
			var err error
			switch mode {
			case "invalid":
				err = e.Restore(context.Background(), strings.NewReader("CREATE TABLE fresh(v INT);INSERT INTO fresh VALUES(99);malformed"))
			case "reader":
				err = e.Restore(context.Background(), &deleteFailingReader{text: "CREATE TABLE fresh(v INT);INSERT INTO fresh VALUES(99);", err: sentinel})
				if !errors.Is(err, sentinel) {
					t.Fatal(err)
				}
			case "canceled":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				err = e.Restore(ctx, strings.NewReader("CREATE TABLE fresh(v INT)"))
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
			if err == nil || e.catalog.state.epoch != epoch || e.commitSeq != frontier || e.storage[ref.Address] != store || store.ends != ends || &store.versions[0] != &root[0] || owner.TransactionState() != TxActive || owner.transactions.ticket != ticket || owner.transactions.deletes[ref.Address] != deletes || &owner.transactions.writes[ref.Address][0] != &writes[0] {
				t.Fatal("failed replacement changed mixed authority", err)
			}
			transactionRows(t, owner, "SELECT v FROM t", 11)
			transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20)
			sessionSQL(t, owner, "UPDATE t SET v=12 WHERE id=1")
			transactionEnd(t, owner, tx, true)
			transactionRows(t, peer, "SELECT v FROM t", 12)
			lockEmpty(t, e)
		})
	}
}
