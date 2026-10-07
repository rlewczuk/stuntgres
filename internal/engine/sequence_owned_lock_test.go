package engine

import (
	"context"
	"runtime"
	"stuntgres/internal/parser"
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

func ownedLockQueued(t *testing.T, e *Engine, ref ObjectRef, mode relationLockMode, done <-chan error) *relationRequest {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.locks.mu.Lock()
		entry := e.locks.entries[lockResource{ObjectRef: ref}]
		var request *relationRequest
		if entry != nil && len(entry.pending) == 1 && entry.pending[0].mode == mode {
			request = entry.pending[0]
		}
		e.locks.mu.Unlock()
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
				ownedLockQueued(t, e, ref, accessShare, done)
				transactionEnd(t, a, ar, false)
				lockAwait(t, done, nil)
				e.locks.mu.Lock()
				entry := e.locks.entries[lockResource{ObjectRef: ref}]
				retained := entry != nil && entry.holders[b.transactions.ticket]&accessShare != 0
				e.locks.mu.Unlock()
				if !retained {
					t.Fatal("ownership table AS not retained to transaction end")
				}
				transactionEnd(t, b, br, true)
				late := e.state.descriptors[catalogRef(t, e, e.CatalogRead(), "late").Address].(*relationDescriptor)
				table := e.state.descriptors[ref.Address].(*relationDescriptor)
				want := ref.Address
				want.SubID = table.attributes[table.index["c"]]
				if late.sequence.owned != want {
					t.Fatal("wrong ownership address", late.sequence.owned, want)
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
				ownedLockQueued(t, e, blocker, accessExclusive, dropping)
				late := ownedLockAsync(e, c, ctx, ast, d)
				ref := catalogRef(t, e, e.CatalogRead(), "t")
				ownedLockQueued(t, e, ref, accessShare, late)
				transactionEnd(t, b, br, false)
				lockAwait(t, dropping, nil)
				select {
				case err := <-late:
					sequenceOwnerDiagnostic(t, err, "42703", `column "c" of relation "t" does not exist`, "")
				case <-time.After(5 * time.Second):
					t.Fatal("late ownership did not finish")
				}
				table := e.state.descriptors[ref.Address].(*relationDescriptor)
				if _, ok := table.index["c"]; ok {
					t.Fatal("column survived drop")
				}
				transactionRows(t, c, "SELECT keep FROM t", 9)
				if _, ok := e.state.descriptors[blocker.Address]; ok {
					t.Fatal("owned blocker survived drop")
				}
				found := e.CatalogRead().Lookup(NameKey{ClassID: RelationClassID, Namespace: table.namespace, Name: "late"})
				if create {
					if len(found) != 0 {
						t.Fatal("failed CREATE published late sequence")
					}
				} else {
					if len(found) != 1 || e.state.descriptors[found[0]].(*relationDescriptor).sequence.owned != (ObjectAddress{}) {
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
					var sequence ObjectRef
					var generator sequenceGenerator
					if !create {
						sequence = catalogRef(t, e, before, "late")
						generator = *e.sequences[sequence.Address]
					}
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					done := ownedLockAsync(e, b, ctx, ast, d)
					ref := catalogRef(t, e, before, "t")
					request := ownedLockQueued(t, e, ref, accessShare, done)
					if failure == "Cancel" {
						cancel()
					} else {
						var notify *relationRequest
						e.locks.mu.Lock()
						request.deadline = time.Now().Add(-time.Second)
						e.locks.settle(request.entry, &notify)
						e.locks.retire(request.ref, request.entry)
						e.locks.mu.Unlock()
						notifyRequests(notify)
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
					if e.state != before.state {
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

// Existing documented native boundary: cycles terminate through cancellation
// or lock_timeout, not a fabricated PostgreSQL deadlock diagnostic.
func TestSequenceOwnedLockCycleCleanup(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, failure := range []string{"Cancel", "Timeout"} {
			t.Run(map[bool]string{false: "Direct/", true: "Held/"}[prepared]+failure, func(t *testing.T) {
				e := New()
				a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
				sessionSQL(t, a, "CREATE TABLE t(c INT, keep INT)")
				sessionSQL(t, a, "INSERT INTO t VALUES(7,9)")
				sessionSQL(t, a, "CREATE SEQUENCE late START 17 OWNED BY t.c")
				ast, d := ownedLockStatement(t, e, b, false, prepared)
				sessionSQL(t, b, "SET lock_timeout TO '1h'")
				br := transactionBegin(t, b)
				sequence := catalogRef(t, e, e.CatalogRead(), "late")
				// Real ticket admission fixes the sequence-first ordering without
				// a production pause hook between the two ALTER lock requests.
				if err := e.locks.acquire(context.Background(), b.transactions.ticket, lockResource{ObjectRef: sequence}, shareRowExclusive); err != nil {
					t.Fatal(err)
				}
				ctx, stop := context.WithCancel(context.Background())
				defer stop()
				dropping := lockAsync(e, a, ctx, "ALTER TABLE t DROP COLUMN c", false)
				dropRequest := ownedLockQueued(t, e, sequence, accessExclusive, dropping)
				before := e.CatalogRead()
				generator := *e.sequences[sequence.Address]
				waitctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				altering := ownedLockAsync(e, b, waitctx, ast, d)
				table := catalogRef(t, e, before, "t")
				request := ownedLockQueued(t, e, table, accessShare, altering)
				e.locks.mu.Lock()
				cycle := request.entry.holders[dropRequest.ticket]&accessExclusive != 0
				e.locks.mu.Unlock()
				if !cycle {
					t.Fatal("drop did not retain table AX while waiting for sequence")
				}
				if e.state != before.state || *e.sequences[sequence.Address] != generator {
					t.Fatal("cycle published catalog or generator")
				}
				if failure == "Cancel" {
					cancel()
				} else {
					var notify *relationRequest
					e.locks.mu.Lock()
					request.deadline = time.Now().Add(-time.Second)
					e.locks.settle(request.entry, &notify)
					e.locks.retire(request.ref, request.entry)
					e.locks.mu.Unlock()
					notifyRequests(notify)
				}
				if failure == "Cancel" {
					lockAwait(t, altering, context.Canceled)
				} else {
					select {
					case err := <-altering:
						sequenceOwnerDiagnostic(t, err, "55P03", "canceling statement due to lock timeout", "")
					case <-time.After(5 * time.Second):
						t.Fatal("cycle timeout did not finish")
					}
				}
				if b.TransactionState() != TxAborted {
					t.Fatal("failed explicit ownership did not abort")
				}
				lockAwait(t, dropping, nil)
				transactionEnd(t, b, br, false)
				if _, ok := e.state.descriptors[sequence.Address]; ok {
					t.Fatal("drop failed to remove owned sequence after cycle release")
				}
				transactionRows(t, b, "SELECT keep FROM t", 9)
				assertTemporaryProjectionProviders(t, e)
				sessionSQL(t, b, "CREATE SEQUENCE healthy START 23 OWNED BY t.keep")
				sequenceScalar(t, b, "SELECT nextval('healthy')", 23)
				assertTemporaryProjectionProviders(t, e)
				lockEmpty(t, e)
			})
		}
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
				ownedLockQueued(t, e, ref, accessShare, done)
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
				lockAwait(t, done, ErrCatalogReplaced)
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
