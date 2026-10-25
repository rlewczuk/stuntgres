package tablecmds_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

// Exercise the real dependency admission before any DEFAULT or CHECK demand.
func TestAlterAddColumnProviderAdmissionBeforeDemand(t *testing.T) {
	for _, withDefault := range []bool{false, true} {
		t.Run(fmt.Sprint(withDefault), func(t *testing.T) {
			e := engine.New()
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
				e := engine.New()
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
				_, err = scope.Execute(enginetest.Statement(t, "ALTER TABLE t ADD b BIGINT DEFAULT nextval('s') NOT NULL CHECK(nextval('s')>0)"), 0, nil)
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
					if err = engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE sentinel(v INT);INSERT INTO sentinel VALUES(91)")); err != nil {
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

func sessionStart(t *testing.T, e *core.Engine, p settings.SessionDefaultProvider) *session.Session {
	t.Helper()
	s, err := e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Defaults: p})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s
}

func sessionSQL(t *testing.T, s *session.Session, q string) core.Result {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	r, err := core.EngineOf(s).Execute(op, enginetest.Statement(t, q), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Rows != nil {
		t.Cleanup(func() {
			if err := r.Rows.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	return r
}

func roleTableRef(t *testing.T, e *core.Engine, name string) ids.ObjectRef {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	ref, err := e.ResolveReference(catalogAuthorization(e, e.Catalog.Current()), enginetest.Statement(t, "SELECT * FROM "+name).(*parser.Select).From.(*parser.TableReference).Name)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func transactionBegin(t *testing.T, s *session.Session) ids.TxRef {
	t.Helper()
	op := transactionOperation(t, s)
	defer op.Done()
	ref, err := core.EngineOf(s).Begin(op, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func lockAsync(e *core.Engine, s *session.Session, ctx context.Context, q string, prepare bool) <-chan error {
	done := make(chan error, 1)
	go func() {
		op, err := s.Acquire(ctx)
		if err == nil {
			if prepare {
				_, err = e.Describe(op, mustLockStatement(q))
			} else {
				var r core.Result
				r, err = e.Execute(op, mustLockStatement(q), 0, nil)
				if r.Rows != nil {
					r.Rows.Close()
				}
			}
			op.Done()
		}
		done <- err
	}()
	return done
}

func lockPending(t *testing.T, e *core.Engine, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.Locks.Inspect()
		got := 0
		for _, entry := range lockState.Resources {
			got += len(entry.Pending)
		}
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pending %d, want %d", got, n)
		}
		runtime.Gosched()
	}
}

func lockAwait(t *testing.T, done <-chan error, want error) {
	t.Helper()
	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Fatalf("got %v want %v", err, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lock operation did not terminate")
	}
}

func transactionEnd(t *testing.T, s *session.Session, ref ids.TxRef, commit bool) core.EndResult {
	t.Helper()
	op := transactionOperation(t, s)
	defer op.Done()
	var r core.EndResult
	var err error
	if commit {
		r, err = core.EngineOf(s).Commit(op, ref)
	} else {
		r, err = core.EngineOf(s).Rollback(op, ref)
	}
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func lockEmpty(t *testing.T, e *core.Engine) {
	t.Helper()
	lockState := e.Locks.Inspect()
	if len(lockState.Owners) != 0 || len(lockState.Resources) != 0 {
		t.Fatalf("residual lock state: owners=%d entries=%d", len(lockState.Owners), len(lockState.Resources))
	}
}

func transactionRows(t *testing.T, s *session.Session, q string, want ...int64) *exec.FinalRows {
	t.Helper()
	r := sessionSQL(t, s, q)
	t.Cleanup(func() {
		if err := r.Rows.Close(); err != nil {
			t.Error(err)
		}
	})
	got := enginetest.Tuples(r)
	expected := make([][]driver.Value, len(want))
	for i, v := range want {
		expected[i] = []driver.Value{v}
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("%s got %v want %v", q, got, expected)
	}
	return r.Rows.(*exec.FinalRows)
}

// Host catalog fixtures resolve as the real bootstrap principal, not as a
// zero/default-allow authorization view. Caller holds e.mu.
func catalogAuthorization(e *core.Engine, view catalog.Read) core.AuthorizationView {
	return e.AuthorityView(access.View{Owner: &e.Catalog.Owner, PairedCatalog: view, RoleRecord: e.RoleStore.Published, Catalog: view, Roles: e.RoleStore.PublishedRead(&e.Catalog.Owner, view), State: authorization.RoleState{CurrentUser: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")}})
}

func transactionOperation(t *testing.T, s *session.Session) *session.Operation {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	return op
}
