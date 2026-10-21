package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestInsertSelectSourceTimeoutPosition(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		e := New()
		a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(i INT)")
		sessionSQL(t, a, "CREATE TABLE s(i INT)")
		sessionSQL(t, b, "SET lock_timeout='80ms'")
		tx := transactionBegin(t, a)
		sessionSQL(t, a, "LOCK TABLE s IN ACCESS EXCLUSIVE MODE")
		done := waitsCommandAsync(e, b, context.Background(), "INSERT INTO t SELECT i FROM s", prepare)
		lockPending(t, e, 1)
		err := <-done
		waitsDiagnostic(t, err, "55P03", "canceling statement due to lock timeout", "29")
		transactionEnd(t, a, tx, false)
		lockEmpty(t, e)
	}
	// Cached execution retains no parse-analysis position callback.
	e := New()
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(i INT)")
	sessionSQL(t, a, "CREATE TABLE s(i INT)")
	ast := statement(t, "INSERT INTO t SELECT i FROM s")
	op := transactionOperation(t, b)
	desc, err := e.Describe(op, ast)
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	sessionSQL(t, b, "SET lock_timeout='80ms'")
	tx := transactionBegin(t, a)
	sessionSQL(t, a, "LOCK TABLE s IN ACCESS EXCLUSIVE MODE")
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
	lockPending(t, e, 1)
	waitsDiagnostic(t, <-done, "55P03", "canceling statement due to lock timeout")
	transactionEnd(t, a, tx, false)
	lockEmpty(t, e)
}

func TestInsertSelectStaleEpoch(t *testing.T) {
	for _, replacement := range []string{"reset", "restore"} {
		t.Run(replacement, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(i INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(9)")
			sessionSQL(t, s, "CREATE TABLE source(i INT)")
			sessionSQL(t, s, "INSERT INTO source VALUES(1),(2)")
			tx := transactionBegin(t, s)
			op := transactionOperation(t, s)
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			ast := statement(t, "INSERT INTO t SELECT i FROM source")
			if _, err = scope.Describe(ast); err != nil {
				t.Fatal(err)
			}
			e.mu.Lock()
			auth, err := e.authorizationLocked(op)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			b, err := describe(op.Ctx, e.analysisHost(op), ast, auth, scope.lockedRef, scope.input, nil, &scope.queryLockAdmission)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			view, err := scope.acquireDataViewLocked(e, b.QuerySources[0].Ref)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			e.mu.Unlock()
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
						err = e.Reset(context.Background())
					} else {
						err = e.Restore(context.Background(), strings.NewReader("CREATE TABLE t(i INT); INSERT INTO t VALUES(77)"))
					}
					if err != nil {
						return sqltype.Value{}, err
					}
				}
				return original.Eval(c)
			})
			rows, err := evaluateInsertQuery(op.Ctx, b, source, nil, "public", scope.input, scope.output, scope)
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
			transactionEnd(t, s, tx, false)
			if replacement == "restore" {
				transactionRows(t, s, "SELECT i FROM t", 77)
			}
			lockEmpty(t, e)
		})
	}
}
