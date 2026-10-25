package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
)

// Exercise the real dependency admission before any DEFAULT or CHECK demand.
func TestAlterAddColumnProviderAdmissionBeforeDemand(t *testing.T) {
	for _, withDefault := range []bool{false, true} {
		t.Run(fmt.Sprint(withDefault), func(t *testing.T) {
			e := New()
			holder, worker := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, holder, "CREATE SEQUENCE s START 1 CACHE 1")
			sessionSQL(t, holder, "CREATE TABLE t(id INT)")
			sessionSQL(t, holder, "INSERT INTO t VALUES(1),(2),(2)")
			ref := roleTableRef(t, e, "t")
			seq := roleTableRef(t, e, "s")
			old, storage := e.Catalog.State.Get(ref.Address), e.Storage.Relations[ref.Address]
			generator := e.Storage.Sequences[seq.Address]
			tx := transactionBegin(t, holder)
			sessionSQL(t, holder, "DROP SEQUENCE s")
			q := "ALTER TABLE t ADD b BIGINT DEFAULT 7 CHECK(nextval('s')>0)"
			if withDefault {
				q = "ALTER TABLE t ADD b BIGINT DEFAULT nextval('s') CHECK(nextval('s')>0)"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			waiting := lockAsync(e, worker, ctx, q, false)
			lockPending(t, e, 1)
			lockState := e.Locks.Inspect()
			entry := lockState.Resources[lock.Resource{ObjectRef: seq}]
			queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.AccessShare && entry.Holders[holder.Transactions.Ticket].Mode&lock.AccessExclusive != 0
			if !queued {
				t.Fatal("ADD did not queue AS against exact provider")
			}
			e.Mu.Lock()
			untouched := !generator.Called && e.Catalog.State.Get(ref.Address) == old && e.Storage.Relations[ref.Address] == storage
			e.Mu.Unlock()
			if !untouched {
				t.Fatal("ADD demanded or published before provider admission")
			}
			cancel()
			lockAwait(t, waiting, context.Canceled)
			transactionEnd(t, holder, tx, false)
			lockEmpty(t, e)
			if generator.Called || e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != storage {
				t.Fatal("canceled admission changed original candidate/root")
			}
			sessionSQL(t, worker, q)
			want := int64(3)
			if withDefault {
				want = 6
			}
			if generator.Last != want || !generator.Called {
				t.Fatal("admitted ADD DEFAULT/CHECK demand", generator.Last, want)
			}
			checkReviewDependencyValid(t, e)
			lockEmpty(t, e)
		})
	}
}

// Actual scope.Execute assembles complete private candidate/backfill before the
// existing Finish fences veto publication; no production callback is introduced.
func TestAlterAddColumnFinishFences(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		vetoes := []string{"cancel", "authorization", "storage"}
		// Implicit sequence demand reserves replacement through Finish; the
		// explicit transaction exercises the replacement epoch veto.
		if explicit {
			vetoes = append(vetoes, "replacement")
		}
		for _, veto := range vetoes {
			t.Run(fmt.Sprintf("%v/%s", explicit, veto), func(t *testing.T) {
				e := New()
				worker := sessionStart(t, e, nil)
				sessionSQL(t, worker, "CREATE SEQUENCE s START 1 CACHE 1")
				sessionSQL(t, worker, "CREATE TABLE t(id INT)")
				sessionSQL(t, worker, "INSERT INTO t VALUES(1),(2),(3)")
				ref := roleTableRef(t, e, "t")
				seq := roleTableRef(t, e, "s")
				old := e.Catalog.State.MustRelation(ref.Address)
				stored := e.Storage.Relations[ref.Address]
				generator := e.Storage.Sequences[seq.Address]
				var tx ids.TxRef
				if explicit {
					tx = transactionBegin(t, worker)
					sessionSQL(t, worker, "UPDATE t SET id=11 WHERE id=1")
					sessionSQL(t, worker, "DELETE FROM t WHERE id=2")
					sessionSQL(t, worker, "INSERT INTO t VALUES(4)")
					sessionSQL(t, worker, "SAVEPOINT before_add")
				}
				priorWrites := worker.Transactions.Writes[ref.Address]
				priorDeletes := worker.Transactions.Deletes[ref.Address]
				priorCatalog := worker.Transactions.Catalog
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				op, err := worker.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				scope, err := e.StartStatement(op)
				if err != nil {
					op.Done()
					t.Fatal(err)
				}
				err = nil
				defer func() { scope.Finish(errors.New("unit cleanup")); op.Done() }()
				_, err = scope.Execute(statement(t, "ALTER TABLE t ADD b BIGINT DEFAULT nextval('s') NOT NULL CHECK(nextval('s')>0)"), 0, nil)
				if err != nil {
					t.Fatal(err)
				}
				candidate := scope.Catalog.Descriptors[ref.Address].(*catalog.Relation)
				shape := scope.Catalog.Storage[ref.Address]
				if candidate == old || candidate.ColumnMeta.HighWater != old.ColumnMeta.HighWater+1 || len(candidate.Columns) != 2 || shape.Replacement == stored || !shape.Rewritten || generator.Last != 6 {
					t.Fatal("complete candidate/backfill missing", candidate, shape, generator.Last)
				}
				if e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != stored || old.ColumnMeta.HighWater != 1 {
					t.Fatal("Execute published private ADD")
				}
				for _, row := range stored.Versions {
					if len(row.Values) != 1 {
						t.Fatal("predecessor width changed")
					}
				}
				lockState := e.Locks.Inspect()
				entry := lockState.Resources[lock.Resource{ObjectRef: seq}]
				held := entry != nil && entry.Holders[scope.LockTicket].Mode&lock.AccessShare != 0
				if !held {
					t.Fatal("provider hold retired before Finish")
				}
				var injectedStorage *storage.Relation
				switch veto {
				case "cancel":
					cancel()
				case "authorization":
					worker.Mu.Lock()
					worker.Authorization.Revision++
					worker.Mu.Unlock()
				case "storage":
					injectedStorage = &storage.Relation{Generation: stored.Generation + 1, Versions: stored.Versions, Ends: stored.Ends}
					e.Mu.Lock()
					e.Storage.InstallRelationLocked(ref.Address, injectedStorage)
					e.Mu.Unlock()
				case "replacement":
					if err = Restore(context.Background(), e, strings.NewReader("CREATE TABLE sentinel(v INT);INSERT INTO sentinel VALUES(91)")); err != nil {
						t.Fatal(err)
					}
				}
				err = scope.Finish(nil)
				op.Done()
				if err == nil {
					t.Fatal("Finish accepted veto", veto)
				}
				if veto == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if veto == "replacement" && !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal(err)
				}
				if veto == "authorization" || veto == "storage" {
					var conflict catalog.Conflict
					if !errors.As(err, &conflict) {
						t.Fatal("wrong fence diagnostic", err)
					}
				}
				if veto != "replacement" {
					wantStorage := stored
					if injectedStorage != nil {
						wantStorage = injectedStorage
					}
					if e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != wantStorage || old.ColumnMeta.HighWater != 1 || len(old.Columns) != 1 || generator.Last != 6 {
						t.Fatal("veto published ADD or rewound sequence")
					}
					if explicit && (!storage.SameVersionRoot(worker.Transactions.Writes[ref.Address], priorWrites) || worker.Transactions.Deletes[ref.Address] != priorDeletes || worker.Transactions.Catalog != priorCatalog) {
						t.Fatal("savepoint private roots changed by veto")
					}
				} else {
					transactionRows(t, sessionStart(t, e, nil), "SELECT v FROM sentinel", 91)
					if e.Catalog.State.Get(ref.Address) == old || e.Storage.Relations[ref.Address] == stored {
						t.Fatal("old incarnation resurrected")
					}
				}
				if explicit {
					transactionEnd(t, worker, tx, false)
				}
				lockEmpty(t, e)
				if !e.ReplacementMu.TryLock() {
					t.Fatal("replacement reservation retained")
				}
				e.ReplacementMu.Unlock()
				if veto == "replacement" {
					sessionSQL(t, worker, "CREATE SEQUENCE s START 1 CACHE 1")
					sessionSQL(t, worker, "CREATE TABLE t(id INT)")
					sessionSQL(t, worker, "INSERT INTO t VALUES(1),(2),(3)")
				}
				sessionSQL(t, worker, "ALTER TABLE t ADD b INT DEFAULT 7 CHECK(b>0)")
				checkReviewDependencyValid(t, e)
				lockEmpty(t, e)
			})
		}
	}
}
