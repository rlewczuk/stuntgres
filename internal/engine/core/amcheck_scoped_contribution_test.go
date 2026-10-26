package core_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/lock"
)

// Enter through the installed checker at its real heap-recipe context seam.
// The context owns the deterministic callback; production needs no test hook.
type amcheckScopedRecipeContext struct {
	context.Context
	entered bool
	work    func()
}

func (c *amcheckScopedRecipeContext) Err() error {
	if err := c.Context.Err(); err != nil {
		return err
	}
	if !c.entered {
		var pcs [32]uintptr
		count := runtime.Callers(2, pcs[:])
		frames := runtime.CallersFrames(pcs[:count])
		for {
			frame, more := frames.Next()
			if strings.Contains(frame.Function, "index.(*ExecutionPreparation).PredicateMatches") {
				c.entered = true
				c.work()
				break
			}
			if !more {
				break
			}
		}
	}
	return c.Context.Err()
}

func TestAmcheckScopedOrdinaryDuringRecipe(t *testing.T) {
	for _, parent := range []bool{false, true} {
		for _, outcome := range []string{"success", "cancel", "nested", "nested_cancel"} {
			name := "basic/" + outcome
			if parent {
				name = "parent/" + outcome
			}
			t.Run(name, func(t *testing.T) {
				e, target, _ := amcheckUnitRoot(t, false)
				n := amcheckUnitExpression(t, e, parent, true)
				reader, ticket := blockingUnitOwner(t, e)
				scope := pgrowlocksUnitScope(t, e, reader)
				scope.RoutineDependencies = []catalog.ExpressionDependency{{Ref: n.Ref, Revision: n.Procedure.Revision()}}
				e.Mu.Lock()
				auth, err := scope.RoutineAuthorizationLocked()
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				scope.RoutineAuth = auth
				heap := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), target.SQLIndex.Parent)}
				index := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), target.Address())}
				mode, modeSQL := lock.AccessShare, "ACCESS SHARE"
				if parent {
					mode, modeSQL = lock.Share, "SHARE"
				}
				if e.Locks.HasRelationMode(ticket, heap, mode) {
					t.Fatal("fixture already holds target mode")
				}
				_, peer := blockingUnitOwner(t, e)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var waiter *lock.Request
				active := &amcheckScopedRecipeContext{Context: ctx}
				active.work = func() {
					if scope.Op.Restricted == nil || !e.Locks.HasRelationMode(ticket, heap, mode) || !e.Locks.HasRelationMode(ticket, index, mode) {
						t.Fatal("callback did not enter installed locked recipe")
					}
					var err error
					waiter, err = e.Locks.StartAcquire(context.Background(), peer, heap, lock.AccessExclusive, lock.Policy{})
					if err != nil || waiter == nil {
						t.Fatal("missing conflicting peer queue", err)
					}
					// This is ordinary nested SQL admission, not another scoped
					// grant. Its same-mode lock has transaction lifetime.
					err = func() (err error) {
						restore := scope.ActivateSQLRoutine(nil, catalog.Read{})
						defer func() { err = restore(err) }()
						finish, err := scope.BeginRoutineCommand()
						if err != nil {
							return err
						}
						defer func() { err = finish(err) }()
						stmt := enginetest.Statement(t, "LOCK TABLE public.t IN "+modeSQL+" MODE")
						if err = scope.AcquireRelation(stmt, true); err != nil {
							return err
						}
						result, err := core.EngineExecuteStatement(e, scope, stmt, 0, nil)
						if result.Rows != nil {
							result.Rows.Close()
						}
						return err
					}()
					if err != nil {
						t.Fatal("nested ordinary acquisition", err)
					}
					if strings.HasPrefix(outcome, "nested") {
						if err := core.HostOf(scope).Amcheck(n, uint32(target.Address().ObjectID), [3]bool{true}); err != nil {
							t.Fatal("nested installed checker", err)
						}
						if !e.Locks.HasRelationMode(ticket, index, mode) {
							t.Fatal("nested cleanup removed outer contribution")
						}
					}
					if strings.Contains(outcome, "cancel") {
						cancel()
					}
				}
				scope.Op.Ctx = active
				evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: active, Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}}
				_, err = n.Eval(&evaluation)
				if !active.entered {
					t.Fatal("installed heap recipe seam was not reached", err)
				}
				if strings.Contains(outcome, "cancel") {
					if !errors.Is(err, context.Canceled) {
						t.Fatal("cancellation was not propagated", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if !e.Locks.HasRelationMode(ticket, heap, mode) {
					t.Fatal("checker removed ordinary grant acquired during recipe")
				}
				if e.Locks.HasRelationMode(ticket, index, mode) {
					t.Fatal("function-only index contribution leaked")
				}
				select {
				case <-waiter.Done():
					t.Fatal("peer passed before transaction end", waiter.Err())
				default:
				}
				// Success keeps the ordinary grant through statement completion.
				// Cancellation cleanup retains the host grant until the ordinary
				// statement-abort path ends transaction ownership.
				finishErr := scope.Finish(err)
				if !errors.Is(finishErr, err) {
					t.Fatal("statement completion changed outcome", finishErr, err)
				}
				scope.Op.Done()
				if err == nil {
					select {
					case <-waiter.Done():
						t.Fatal("statement end removed transaction grant")
					default:
					}
				}
				sessionSQL(t, reader, "ROLLBACK")
				select {
				case <-waiter.Done():
					if err := waiter.Err(); err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("peer not admitted at transaction end")
				}
			})
		}
	}
}
