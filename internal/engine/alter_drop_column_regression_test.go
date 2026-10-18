package engine

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAlterDropColumnQueuedSameIdentity(t *testing.T) {
	for _, q := range []string{"SELECT * FROM t ORDER BY a", "INSERT INTO t(a,c) VALUES(2,20)", "ALTER TABLE t DROP c", "ALTER TABLE t DROP IF EXISTS b"} {
		t.Run(q, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE t(a INT,b TEXT,c BIGINT)")
			run(t, e, "INSERT INTO t VALUES(1,'gone',9)")
			ref := roleTableRef(t, e, "t")
			beforeDropRevision := e.state.descriptors[ref.Address].Revision()
			entered, release := make(chan struct{}), make(chan struct{})
			var first, unblock sync.Once
			t.Cleanup(func() { unblock.Do(func() { close(release) }) })
			e.providers[ids.RelationClassID] = alterDropProvider{e.providers[ids.RelationClassID], func() { first.Do(func() { close(entered); <-release }) }}
			dropping := lockAsync(e, sessionStart(t, e, nil), context.Background(), "ALTER TABLE t DROP b", false)
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("DROP never reached provider barrier")
			}
			op, cleanup, err := testOperation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			type outcome struct {
				result  Result
				err     error
				notices []sqlstate.Error
			}
			done := make(chan outcome, 1)
			stmt := statement(t, q)
			go func() { result, err := e.Execute(op, stmt, 0, nil); done <- outcome{result, err, op.DetachNotices()} }()
			lockPending(t, e, 1)
			unblock.Do(func() { close(release) })
			lockAwait(t, dropping, nil)
			var got outcome
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("queued operation did not finish")
			}
			if got.err != nil {
				t.Fatalf("same-identity definition evolution must rebind: %v", got.err)
			}
			if roleTableRef(t, e, "t") != ref {
				t.Fatal("relation identity changed")
			}
			switch q {
			case "SELECT * FROM t ORDER BY a":
				if len(got.result.Rows.Schema()) != 2 || got.result.Rows.Schema()[1].Name != "c" || got.result.Rows.Schema()[1].OriginAttribute != 3 {
					t.Fatal(got.result.Rows.Schema())
				}
				if rows := tuples(got.result); !reflect.DeepEqual(rows, [][]driver.Value{{int64(1), int64(9)}}) {
					t.Fatal(rows)
				}
			case "INSERT INTO t(a,c) VALUES(2,20)":
				if got.result.Count != 1 {
					t.Fatal(got.result.Count)
				}
				if rows := tuples(run(t, e, "SELECT * FROM t ORDER BY a")); !reflect.DeepEqual(rows, [][]driver.Value{{int64(1), int64(9)}, {int64(2), int64(20)}}) {
					t.Fatal(rows)
				}
			case "ALTER TABLE t DROP c":
				if rows := tuples(run(t, e, "SELECT * FROM t")); !reflect.DeepEqual(rows, [][]driver.Value{{int64(1)}}) {
					t.Fatal(rows)
				}
			case "ALTER TABLE t DROP IF EXISTS b":
				want := []sqlstate.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `column "b" of relation "t" does not exist, skipping`}}
				if !reflect.DeepEqual(got.notices, want) {
					t.Fatal(got.notices, want)
				}
				descriptor := e.state.descriptors[ref.Address]
				storage := e.storage[ref.Address]
				if descriptor.Revision() != beforeDropRevision+1 || len(storage.versions) != 1 || len(storage.versions[0].values) != 2 {
					t.Fatal(descriptor, storage)
				}
				if _, err := e.Execute(op, stmt, 0, nil); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(op.DetachNotices(), want) || e.state.descriptors[ref.Address] != descriptor || e.storage[ref.Address] != storage {
					t.Fatal("repeated skip changed descriptor/storage or notices")
				}
			}
			lockEmpty(t, e)
		})
	}
}

func TestAlterDropColumnQueuedReplacementFenced(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INT,b TEXT)")
	run(t, e, "INSERT INTO t VALUES(1,'old')")
	entered, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	t.Cleanup(func() { unblock.Do(func() { close(release) }) })
	var gated atomic.Bool
	e.providers[ids.RelationClassID] = alterDropProvider{e.providers[ids.RelationClassID], func() {
		if !gated.CompareAndSwap(false, true) {
			return
		}
		close(entered)
		<-release
	}}
	dropping := lockAsync(e, sessionStart(t, e, nil), context.Background(), "ALTER TABLE t DROP b", false)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("DROP barrier missing")
	}
	queued := lockAsync(e, sessionStart(t, e, nil), context.Background(), "SELECT * FROM t", false)
	lockPending(t, e, 1)
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	lockAwait(t, queued, ErrCatalogReplaced)
	unblock.Do(func() { close(release) })
	lockAwait(t, dropping, ErrCatalogReplaced)
	e.providers[ids.RelationClassID] = relationProvider{}
	run(t, e, "CREATE TABLE t(replacement INT)")
	result := run(t, e, "SELECT * FROM t")
	if result.Count != 0 || result.Rows.Schema()[0].Name != "replacement" {
		t.Fatal(result)
	}
	result.Rows.Close()
	lockEmpty(t, e)
}

func TestAlterDropColumnLockedBindingShadowCreation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, query := range []string{"ALTER TABLE t DROP b", "ALTER TABLE t DROP IF EXISTS missing"} {
			phase := "direct"
			if prepared {
				phase = "retained-prepared"
			}
			t.Run(phase+"/"+query, func(t *testing.T) {
				e := New()
				setup := sessionStart(t, e, nil)
				late := sessionStart(t, e, nil)
				early := sessionStart(t, e, nil)
				waiter := sessionStart(t, e, nil)
				sessionSQL(t, setup, "CREATE SCHEMA early")
				sessionSQL(t, setup, "CREATE SCHEMA late CREATE TABLE t(a INT,b TEXT,c BIGINT)")
				sessionSQL(t, setup, "INSERT INTO late.t VALUES(1,'late',9)")
				sessionSQL(t, waiter, "SET search_path TO early, late")
				ast := statement(t, query)
				var expected *StatementDescription
				if prepared {
					op := transactionOperation(t, waiter)
					description, err := e.Describe(op, ast)
					op.Done()
					if err != nil {
						t.Fatal(err)
					}
					expected = &description
				}
				lateRef := roleTableRef(t, e, "late.t")
				lateTx := transactionBegin(t, late)
				sessionSQL(t, late, "LOCK TABLE late.t IN ACCESS EXCLUSIVE MODE")
				lateHeld := true
				defer func() {
					if lateHeld {
						transactionEnd(t, late, lateTx, false)
					}
				}()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				type outcome struct {
					err     error
					notices []sqlstate.Error
					count   int64
				}
				done := make(chan outcome, 1)
				go func() {
					out := outcome{}
					op, err := waiter.Acquire(ctx)
					if err == nil {
						scope, scopeErr := e.StartStatement(op)
						err = scopeErr
						if err == nil {
							scope.Expected = expected
							result, executeErr := scope.Execute(ast, 0, nil)
							out.count = result.Count
							err = scope.Finish(executeErr)
						}
						out.notices = op.DetachNotices()
						op.Done()
					}
					out.err = err
					done <- out
				}()
				joined := false
				defer func() {
					cancel()
					if !joined {
						select {
						case <-done:
						case <-time.After(5 * time.Second):
							t.Error("shadow waiter cleanup stuck")
						}
					}
				}()
				lockPending(t, e, 1)
				lockState := e.locks.Inspect()
				entry := lockState.Resources[lockResource{ObjectRef: lateRef}]
				queuedOnLate := entry != nil && len(entry.Pending) == 1
				if !queuedOnLate {
					t.Fatal("ALTER did not queue on exact late resource")
				}
				sessionSQL(t, setup, "CREATE TABLE early.t(a INT,b TEXT,c BIGINT)")
				sessionSQL(t, setup, "INSERT INTO early.t VALUES(2,'early',20)")
				earlyRef := roleTableRef(t, e, "early.t")
				earlyTx := transactionBegin(t, early)
				sessionSQL(t, early, "LOCK TABLE early.t IN ACCESS EXCLUSIVE MODE")
				earlyHeld := true
				defer func() {
					if earlyHeld {
						transactionEnd(t, early, earlyTx, false)
					}
				}()
				lateDescriptor, earlyDescriptor := e.state.descriptors[lateRef.Address], e.state.descriptors[earlyRef.Address]
				lateStorage, earlyStorage := e.storage[lateRef.Address], e.storage[earlyRef.Address]
				var before bytes.Buffer
				if err := e.Dump(context.Background(), &before); err != nil {
					t.Fatal(err)
				}
				transactionEnd(t, late, lateTx, false)
				lateHeld = false
				select {
				case out := <-done:
					joined = true
					if !errors.Is(out.err, ErrCatalogReplaced) || len(out.notices) != 0 || out.count != 0 {
						t.Errorf("unlocked shadow must not bind: err=%v notices=%v count=%d", out.err, out.notices, out.count)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("native ALTER did not reject shadow while early AX held")
				}
				if e.state.descriptors[lateRef.Address] != lateDescriptor || e.state.descriptors[earlyRef.Address] != earlyDescriptor || e.storage[lateRef.Address] != lateStorage || e.storage[earlyRef.Address] != earlyStorage {
					t.Error("shadow ALTER changed descriptor or storage")
				}
				var after bytes.Buffer
				if err := e.Dump(context.Background(), &after); err != nil || after.String() != before.String() {
					t.Error("shadow ALTER changed canonical state", err, after.String())
				}
				lockPending(t, e, 0)
				transactionEnd(t, early, earlyTx, false)
				earlyHeld = false
				if values := tuples(run(t, e, "SELECT * FROM late.t")); !reflect.DeepEqual(values, [][]driver.Value{{int64(1), "late", int64(9)}}) {
					t.Error(values)
				}
				if values := tuples(run(t, e, "SELECT * FROM early.t")); !reflect.DeepEqual(values, [][]driver.Value{{int64(2), "early", int64(20)}}) {
					t.Error(values)
				}
				if waiter.TransactionState() != TxIdle {
					t.Error("waiter implicit transaction not finished")
				}
				lockEmpty(t, e)
			})
		}
	}
}
