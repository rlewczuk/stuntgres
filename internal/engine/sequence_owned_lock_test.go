package engine

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

// The retained description is captured before the competing utility starts.
func ownedLockAsync(e *Engine, s *Session, ctx context.Context, ast parser.Statement, description *StatementDescription) <-chan error {
	done := make(chan error, 1)
	go func() {
		op, err := s.Acquire(ctx)
		if err == nil {
			var scope *StatementScope
			scope, err = e.StartStatement(op)
			if err == nil {
				scope.Expected = description
				_, err = scope.Execute(ast, 0, nil)
				err = scope.Finish(err)
			}
			op.Done()
		}
		done <- err
	}()
	return done
}

func ownedLockQueued(t *testing.T, e *Engine, ref ids.ObjectRef, mode lock.Mode, done <-chan error) *lock.Request {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lockState := e.locks.Inspect()
		entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
		var request *lock.Request
		if entry != nil && len(entry.Pending) == 1 && entry.Pending[0].Mode == mode {
			request = entry.Pending[0].Request
		}
		if request != nil {
			return request
		}
		select {
		case err := <-done:
			t.Fatalf("OWNED BY operation completed before required %d admission on %v: %v", mode, ref, err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("required exact relation admission not observed")
		}
		runtime.Gosched()
	}
}

func ownedLockStatement(t *testing.T, e *Engine, s *Session, create, prepared bool) (parser.Statement, *StatementDescription) {
	t.Helper()
	q := "ALTER SEQUENCE late OWNED BY t.c"
	if create {
		q = "CREATE SEQUENCE late START 17 OWNED BY t.c"
	}
	if prepared {
		ast, d := sequencePreparedDescription(t, e, s, q)
		return ast, &d
	}
	return statement(t, q), nil
}

func TestSequenceOwnedTableLock(t *testing.T) {
	for _, create := range []bool{true, false} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{true: "Create", false: "Alter"}[create]+map[bool]string{false: "/Direct", true: "/Held"}[prepared], func(t *testing.T) {
				e := New()
				a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
				sessionSQL(t, a, "CREATE TABLE t(c INT, keep INT)")
				if !create {
					sessionSQL(t, a, "CREATE SEQUENCE late START 17")
				}
				ast, d := ownedLockStatement(t, e, b, create, prepared)
				ar := transactionBegin(t, a)
				sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
				br := transactionBegin(t, b)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := ownedLockAsync(e, b, ctx, ast, d)
				ref := catalogRef(t, e, e.CatalogRead(), "t")
				ownedLockQueued(t, e, ref, lock.AccessShare, done)
				transactionEnd(t, a, ar, false)
				lockAwait(t, done, nil)
				lockState := e.locks.Inspect()
				entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
				retained := entry != nil && entry.Holders[b.transactions.ticket].Mode&lock.AccessShare != 0
				if !retained {
					t.Fatal("ownership table AS not retained to transaction end")
				}
				transactionEnd(t, b, br, true)
				late := e.catalog.state.MustRelation(catalogRef(t, e, e.CatalogRead(), "late").Address)
				table := e.catalog.state.MustRelation(ref.Address)
				want := ref.Address
				want.SubID = table.Attributes[table.Index["c"]]
				if late.Sequence.owned != want {
					t.Fatal("wrong ownership address", late.Sequence.owned, want)
				}
				sequenceScalar(t, b, "SELECT nextval('late')", 17)
				assertTemporaryProjectionProviders(t, e)
				lockEmpty(t, e)
			})
		}
	}
}

func TestSequenceOwnedColumnDropAdmission(t *testing.T) {
	for _, create := range []bool{true, false} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{true: "Create", false: "Alter"}[create]+map[bool]string{false: "/Direct", true: "/Held"}[prepared], func(t *testing.T) {
				e := New()
				a, b, c := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
				sessionSQL(t, a, "CREATE TABLE t(c INT, keep INT)")
				sessionSQL(t, a, "INSERT INTO t VALUES(7,9)")
				sessionSQL(t, a, "CREATE SEQUENCE blocker OWNED BY t.c")
				if !create {
					sessionSQL(t, a, "CREATE SEQUENCE late START 17")
				}
				ast, d := ownedLockStatement(t, e, c, create, prepared)
				br := transactionBegin(t, b)
				sequenceScalar(t, b, "SELECT nextval('blocker')", 1)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				dropping := lockAsync(e, a, ctx, "ALTER TABLE t DROP COLUMN c", false)
				blocker := catalogRef(t, e, e.CatalogRead(), "blocker")
				ownedLockQueued(t, e, blocker, lock.AccessExclusive, dropping)
				late := ownedLockAsync(e, c, ctx, ast, d)
				ref := catalogRef(t, e, e.CatalogRead(), "t")
				ownedLockQueued(t, e, ref, lock.AccessShare, late)
				transactionEnd(t, b, br, false)
				lockAwait(t, dropping, nil)
				select {
				case err := <-late:
					sequenceOwnerDiagnostic(t, err, "42703", `column "c" of relation "t" does not exist`, "")
				case <-time.After(5 * time.Second):
					t.Fatal("late ownership did not finish")
				}
				table := e.catalog.state.MustRelation(ref.Address)
				if _, ok := table.Index["c"]; ok {
					t.Fatal("column survived drop")
				}
				transactionRows(t, c, "SELECT keep FROM t", 9)
				if _, ok := e.catalog.state.Find(blocker.Address); ok {
					t.Fatal("owned blocker survived drop")
				}
				found := e.CatalogRead().Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: table.namespace, Name: "late"})
				if create {
					if len(found) != 0 {
						t.Fatal("failed CREATE published late sequence")
					}
				} else {
					if len(found) != 1 || e.catalog.state.MustRelation(found[0]).Sequence.owned != (ids.ObjectAddress{}) {
						t.Fatal("failed ALTER published association")
					}
					sequenceScalar(t, c, "SELECT nextval('late')", 17)
				}
				assertTemporaryProjectionProviders(t, e)
				lockEmpty(t, e)
			})
		}
	}
}

func TestSequenceOwnedTableLockFailure(t *testing.T) {
	for _, create := range []bool{true, false} {
		for _, prepared := range []bool{false, true} {
			for _, failure := range []string{"Cancel", "Timeout"} {
				t.Run(map[bool]string{true: "Create", false: "Alter"}[create]+map[bool]string{false: "/Direct/", true: "/Held/"}[prepared]+failure, func(t *testing.T) {
					e := New()
					a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
					sessionSQL(t, a, "CREATE TABLE t(c INT)")
					sessionSQL(t, a, "INSERT INTO t VALUES(7)")
					if !create {
						sessionSQL(t, a, "CREATE SEQUENCE late START 17")
					}
					ast, d := ownedLockStatement(t, e, b, create, prepared)
					ar := transactionBegin(t, a)
					sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
					sessionSQL(t, b, "SET lock_timeout TO '1h'")
					before := e.CatalogRead()
					var sequence ids.ObjectRef
					var generator sequenceGenerator
					if !create {
						sequence = catalogRef(t, e, before, "late")
						generator = *e.sequences[sequence.Address]
					}
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					done := ownedLockAsync(e, b, ctx, ast, d)
					ref := catalogRef(t, e, before, "t")
					request := ownedLockQueued(t, e, ref, lock.AccessShare, done)
					if failure == "Cancel" {
						cancel()
					} else {
						// The statement runs without command admission, so the session's
						// lock_timeout never reaches the request; fire its timeout directly.
						e.locks.ExpireLockTimeout(request)
					}
					if failure == "Cancel" {
						lockAwait(t, done, context.Canceled)
					} else {
						select {
						case err := <-done:
							sequenceOwnerDiagnostic(t, err, "55P03", "canceling statement due to lock timeout", "")
						case <-time.After(5 * time.Second):
							t.Fatal("timeout did not terminate ownership")
						}
					}
					if e.catalog.state != before.state {
						t.Fatal("failed ownership published catalog")
					}
					if !create && *e.sequences[sequence.Address] != generator {
						t.Fatal("failed association changed generator")
					}
					assertTemporaryProjectionProviders(t, e)
					transactionEnd(t, a, ar, false)
					transactionRows(t, b, "SELECT c FROM t", 7)
					// Reuse the same retained utility after the failed admission.
					lockAwait(t, ownedLockAsync(e, b, context.Background(), ast, d), nil)
					sequenceScalar(t, b, "SELECT nextval('late')", 17)
					assertTemporaryProjectionProviders(t, e)
					lockEmpty(t, e)
				})
			}
		}
	}
}

// OWNED BY and column-drop use the same deadlock graph as table/row callers.
// Acyclic cancel/timeout admission remains covered by TestSequenceOwnedTableLockFailure.
func TestSequenceOwnedLockCycleCleanup(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Held"}[prepared], func(t *testing.T) {
			e := New()
			a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(c INT, keep INT)")
			sessionSQL(t, a, "INSERT INTO t VALUES(7,9)")
			sessionSQL(t, a, "CREATE SEQUENCE late START 17 OWNED BY t.c")
			ast := statement(t, "ALTER SEQUENCE late OWNED BY t.keep")
			var description *StatementDescription
			if prepared {
				var d StatementDescription
				ast, d = sequencePreparedDescription(t, e, b, "ALTER SEQUENCE late OWNED BY t.keep")
				description = &d
			}
			br := transactionBegin(t, b)
			bticket := b.transactions.ticket
			sessionSQL(t, b, "CREATE SEQUENCE victim_staged START 31")
			before := e.CatalogRead()
			sequence := catalogRef(t, e, before, "late")
			table := catalogRef(t, e, before, "t")
			// Real admission fixes sequence-first ordering without a pause hook.
			if err := e.locks.Acquire(context.Background(), bticket, lock.Resource{ObjectRef: sequence}, lock.ShareRowExclusive); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			dropping := lockAsync(e, a, ctx, "ALTER TABLE t DROP COLUMN c", false)
			dropRequest := ownedLockQueued(t, e, sequence, lock.AccessExclusive, dropping)
			lockState := e.locks.Inspect()
			entry := lockState.Resources[lock.Resource{ObjectRef: table}]
			cycle := entry != nil && entry.Holders[dropRequest.Ticket()].Mode&lock.AccessExclusive != 0
			if !cycle {
				t.Fatal("drop did not retain table AX while waiting for sequence")
			}
			altering := ownedLockAsync(e, b, ctx, ast, description)
			select {
			case err := <-altering:
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) {
					t.Fatal("missing deadlock diagnostic", err)
				}
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40P01", Message: "deadlock detected", Hint: "See server log for query details.", Detail: diagnostic.Detail}
				if *diagnostic != want {
					t.Fatalf("deadlock fields %#v want %#v", diagnostic, want)
				}
				expected := make(map[string]bool, 2)
				for _, edge := range []struct {
					waiter, blocker *lock.Ticket
					ref             ids.ObjectRef
					mode            string
				}{{bticket, dropRequest.Ticket(), table, "AccessShareLock"}, {dropRequest.Ticket(), bticket, sequence, "AccessExclusiveLock"}} {
					expected[fmt.Sprintf("Transaction %d (session %d, epoch %d) waits for %s on relation %d (class %d, sub-ID %d, epoch %d); blocked by transaction %d (session %d, epoch %d).", edge.waiter.Ref().ID(), edge.waiter.Ref().Session().ID(), edge.waiter.Ref().Epoch(), edge.mode, edge.ref.Address.ObjectID, edge.ref.Address.ClassID, edge.ref.Address.SubID, edge.ref.Epoch, edge.blocker.Ref().ID(), edge.blocker.Ref().Session().ID(), edge.blocker.Ref().Epoch())] = true
				}
				for _, line := range strings.Split(diagnostic.Detail, "\n") {
					if !expected[line] {
						t.Fatalf("unexpected OWNED BY cycle edge %q", line)
					}
					delete(expected, line)
				}
				if len(expected) != 0 {
					t.Fatal("missing OWNED BY cycle edges", expected)
				}
			case <-ctx.Done():
				t.Fatal("cycle did not produce 40P01", ctx.Err())
			}
			if b.TransactionState() != TxAborted {
				t.Fatal("deadlock victim did not abort")
			}
			// Finish must release the victim's sequence lock before explicit ROLLBACK.
			lockAwait(t, dropping, nil)
			op := transactionOperation(t, b)
			_, err := e.Execute(op, statement(t, "SELECT keep FROM t"), 0, nil)
			op.Done()
			sequenceOwnerDiagnostic(t, err, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
			if _, ok := e.catalog.state.Find(sequence.Address); ok {
				t.Fatal("original OWNED BY dependency did not cascade after victim release")
			}
			if len(e.CatalogRead().Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: e.catalog.state.MustRelation(table.Address).namespace, Name: "victim_staged"})) != 0 {
				t.Fatal("deadlock victim published previously staged sequence")
			}
			transactionEnd(t, b, br, false)
			transactionRows(t, b, "SELECT keep FROM t", 9)
			assertTemporaryProjectionProviders(t, e)
			sessionSQL(t, b, "CREATE SEQUENCE healthy START 23 OWNED BY t.keep")
			sequenceScalar(t, b, "SELECT nextval('healthy')", 23)
			assertTemporaryProjectionProviders(t, e)
			lockEmpty(t, e)
		})
	}
}

func TestSequenceOwnedTableLockSkipped(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Held"}[prepared], func(t *testing.T) {
			e := New()
			a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE TABLE t(c INT)")
			sessionSQL(t, a, "CREATE SEQUENCE late")
			ar := transactionBegin(t, a)
			sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			for _, q := range []string{
				"CREATE SEQUENCE fresh OWNED BY NONE",
				"ALTER SEQUENCE late OWNED BY NONE",
				"CREATE SEQUENCE IF NOT EXISTS late OWNED BY t.c",
				"ALTER SEQUENCE IF EXISTS missing OWNED BY t.c",
			} {
				ast := statement(t, q)
				var description *StatementDescription
				if prepared {
					var d StatementDescription
					ast, d = sequencePreparedDescription(t, e, b, q)
					description = &d
				}
				ctx, cancel := context.WithCancel(context.Background())
				done := ownedLockAsync(e, b, ctx, ast, description)
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(q, err)
					}
				case <-time.After(5 * time.Second):
					cancel()
					t.Fatal("skipped/NONE ownership demanded a table lock", q)
				}
				cancel()
			}
			transactionEnd(t, a, ar, false)
			assertTemporaryProjectionProviders(t, e)
			lockEmpty(t, e)
		})
	}
}

func TestSequenceOwnedTableLockReplacement(t *testing.T) {
	for _, create := range []bool{true, false} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{true: "Create", false: "Alter"}[create]+map[bool]string{false: "/Direct", true: "/Held"}[prepared], func(t *testing.T) {
				e := New()
				a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
				sessionSQL(t, a, "CREATE TABLE t(c INT)")
				if !create {
					sessionSQL(t, a, "CREATE SEQUENCE late START 17")
				}
				ast, d := ownedLockStatement(t, e, b, create, prepared)
				ar := transactionBegin(t, a)
				sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := ownedLockAsync(e, b, ctx, ast, d)
				ref := catalogRef(t, e, e.CatalogRead(), "t")
				ownedLockQueued(t, e, ref, lock.AccessShare, done)
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
				lockAwait(t, done, ids.ErrCatalogReplaced)
				transactionEnd(t, a, ar, false)
				assertTemporaryProjectionProviders(t, e)
				sessionSQL(t, b, "CREATE TABLE t(c INT)")
				sessionSQL(t, b, "CREATE SEQUENCE late START 29 OWNED BY t.c")
				sequenceScalar(t, b, "SELECT nextval('late')", 29)
				assertTemporaryProjectionProviders(t, e)
				lockEmpty(t, e)
			})
		}
	}
}
