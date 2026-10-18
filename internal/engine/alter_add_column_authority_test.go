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
			old, storage := e.catalog.state.Get(ref.Address), e.storage[ref.Address]
			generator := e.sequences[seq.Address]
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
			lockState := e.locks.Inspect()
			entry := lockState.Resources[lock.Resource{ObjectRef: seq}]
			queued := entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == lock.AccessShare && entry.Holders[holder.transactions.ticket].Mode&lock.AccessExclusive != 0
			if !queued {
				t.Fatal("ADD did not queue AS against exact provider")
			}
			e.mu.Lock()
			untouched := !generator.called && e.catalog.state.Get(ref.Address) == old && e.storage[ref.Address] == storage
			e.mu.Unlock()
			if !untouched {
				t.Fatal("ADD demanded or published before provider admission")
			}
			cancel()
			lockAwait(t, waiting, context.Canceled)
			transactionEnd(t, holder, tx, false)
			lockEmpty(t, e)
			if generator.called || e.catalog.state.Get(ref.Address) != old || e.storage[ref.Address] != storage {
				t.Fatal("canceled admission changed original candidate/root")
			}
			sessionSQL(t, worker, q)
			want := int64(3)
			if withDefault {
				want = 6
			}
			if generator.last != want || !generator.called {
				t.Fatal("admitted ADD DEFAULT/CHECK demand", generator.last, want)
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
				old := e.catalog.state.MustRelation(ref.Address)
				storage := e.storage[ref.Address]
				generator := e.sequences[seq.Address]
				var tx ids.TxRef
				if explicit {
					tx = transactionBegin(t, worker)
					sessionSQL(t, worker, "UPDATE t SET id=11 WHERE id=1")
					sessionSQL(t, worker, "DELETE FROM t WHERE id=2")
					sessionSQL(t, worker, "INSERT INTO t VALUES(4)")
					sessionSQL(t, worker, "SAVEPOINT before_add")
				}
				priorWrites := worker.transactions.writes[ref.Address]
				priorDeletes := worker.transactions.deletes[ref.Address]
				priorCatalog := worker.transactions.catalog
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
				candidate := scope.catalog.descriptors[ref.Address].(*catalog.Relation)
				shape := scope.catalog.storage[ref.Address]
				if candidate == old || candidate.AttributeHighWater != old.AttributeHighWater+1 || len(candidate.Columns) != 2 || shape.replacement == storage || !shape.rewritten || generator.last != 6 {
					t.Fatal("complete candidate/backfill missing", candidate, shape, generator.last)
				}
				if e.catalog.state.Get(ref.Address) != old || e.storage[ref.Address] != storage || old.AttributeHighWater != 1 {
					t.Fatal("Execute published private ADD")
				}
				for _, row := range storage.versions {
					if len(row.values) != 1 {
						t.Fatal("predecessor width changed")
					}
				}
				lockState := e.locks.Inspect()
				entry := lockState.Resources[lock.Resource{ObjectRef: seq}]
				held := entry != nil && entry.Holders[scope.ticket].Mode&lock.AccessShare != 0
				if !held {
					t.Fatal("provider hold retired before Finish")
				}
				var injectedStorage *relationStorage
				switch veto {
				case "cancel":
					cancel()
				case "authorization":
					worker.mu.Lock()
					worker.authorization.revision++
					worker.mu.Unlock()
				case "storage":
					injectedStorage = &relationStorage{generation: storage.generation + 1, versions: storage.versions, ends: storage.ends}
					e.mu.Lock()
					e.storage[ref.Address] = injectedStorage
					e.mu.Unlock()
				case "replacement":
					if err = e.Restore(context.Background(), strings.NewReader("CREATE TABLE sentinel(v INT);INSERT INTO sentinel VALUES(91)")); err != nil {
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
					wantStorage := storage
					if injectedStorage != nil {
						wantStorage = injectedStorage
					}
					if e.catalog.state.Get(ref.Address) != old || e.storage[ref.Address] != wantStorage || old.AttributeHighWater != 1 || len(old.Columns) != 1 || generator.last != 6 {
						t.Fatal("veto published ADD or rewound sequence")
					}
					if explicit && (!sameVersionRoot(worker.transactions.writes[ref.Address], priorWrites) || worker.transactions.deletes[ref.Address] != priorDeletes || worker.transactions.catalog != priorCatalog) {
						t.Fatal("savepoint private roots changed by veto")
					}
				} else {
					transactionRows(t, sessionStart(t, e, nil), "SELECT v FROM sentinel", 91)
					if e.catalog.state.Get(ref.Address) == old || e.storage[ref.Address] == storage {
						t.Fatal("old incarnation resurrected")
					}
				}
				if explicit {
					transactionEnd(t, worker, tx, false)
				}
				lockEmpty(t, e)
				if !e.replacementMu.TryLock() {
					t.Fatal("replacement reservation retained")
				}
				e.replacementMu.Unlock()
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
