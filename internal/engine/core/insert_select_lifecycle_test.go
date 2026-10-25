package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestInsertSelectSourceTimeoutPosition(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		e := engine.New()
		a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, a, "CREATE TABLE t(i INT)")
		enginetest.SessionSQL(t, a, "CREATE TABLE s(i INT)")
		enginetest.SessionSQL(t, b, "SET lock_timeout='80ms'")
		tx := enginetest.TransactionBegin(t, a)
		enginetest.SessionSQL(t, a, "LOCK TABLE s IN ACCESS EXCLUSIVE MODE")
		done := waitsCommandAsync(e, b, context.Background(), "INSERT INTO t SELECT i FROM s", prepare)
		enginetest.LockPending(t, e, 1)
		err := <-done
		waitsDiagnostic(t, err, "55P03", "canceling statement due to lock timeout", "29")
		enginetest.TransactionEnd(t, a, tx, false)
		enginetest.LockEmpty(t, e)
	}
	// Cached execution retains no parse-analysis position callback.
	e := engine.New()
	a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(i INT)")
	enginetest.SessionSQL(t, a, "CREATE TABLE s(i INT)")
	ast := enginetest.Statement(t, "INSERT INTO t SELECT i FROM s")
	op := enginetest.TransactionOperation(t, b)
	desc, err := e.Describe(op, ast)
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, b, "SET lock_timeout='80ms'")
	tx := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "LOCK TABLE s IN ACCESS EXCLUSIVE MODE")
	done := make(chan error, 1)
	go func() {
		op, _, err := b.AcquireCommand(context.Background())
		if err == nil {
			scope, startErr := e.StartStatement(op)
			err = startErr
			if err == nil {
				scope.Expected = &desc
				_, err = scope.Execute(ast, 0, nil)
				err = scope.Finish(err)
			}
			err = op.CommandError(err)
			op.Done()
		}
		done <- err
	}()
	enginetest.LockPending(t, e, 1)
	waitsDiagnostic(t, <-done, "55P03", "canceling statement due to lock timeout")
	enginetest.TransactionEnd(t, a, tx, false)
	enginetest.LockEmpty(t, e)
}

func TestInsertSelectStaleEpoch(t *testing.T) {
	for _, replacement := range []string{"reset", "restore"} {
		t.Run(replacement, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE t(i INT)")
			enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(9)")
			enginetest.SessionSQL(t, s, "CREATE TABLE source(i INT)")
			enginetest.SessionSQL(t, s, "INSERT INTO source VALUES(1),(2)")
			tx := enginetest.TransactionBegin(t, s)
			op := enginetest.TransactionOperation(t, s)
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			ast := enginetest.Statement(t, "INSERT INTO t SELECT i FROM source")
			if _, err = scope.Describe(ast); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			b, err := engine.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			view, err := scope.AcquireDataViewLocked(e, b.QuerySources[0].Ref)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			e.Mu.Unlock()
			source, err := exec.CaptureSource(op.Ctx, view, b.QuerySources[0].Ref)
			if err != nil {
				t.Fatal(err)
			}
			project := b.Query.Root.(*plan.ProjectNode)
			original := project.Expressions[0]
			calls := 0
			project.Expressions[0] = queryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
				calls++
				if calls == 1 {
					var err error
					if replacement == "reset" {
						err = engine.Reset(context.Background(), e)
					} else {
						err = engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE t(i INT); INSERT INTO t VALUES(77)"))
					}
					if err != nil {
						return sqltype.Value{}, err
					}
				}
				return original.Eval(c)
			})
			rows, err := dml.EvaluateInsertQuery(op.Ctx, b, source, nil, "public", scope.Input, scope.Output, scope.WriteHost())
			if err != nil || len(rows.Values) != 2 || calls != 2 || source.View != nil {
				t.Fatal(rows, err, calls, source.View)
			}
			if err = scope.Writes().Apply(b.Ref, rows.Values); !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal(err)
			}
			if err = scope.Finish(err); !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal(err)
			}
			op.Done()
			enginetest.TransactionEnd(t, s, tx, false)
			if replacement == "restore" {
				enginetest.TransactionRows(t, s, "SELECT i FROM t", 77)
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
