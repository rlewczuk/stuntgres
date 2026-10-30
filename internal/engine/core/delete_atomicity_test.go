package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func deleteProbeScope(t *testing.T, s *session.Session) (*core.StatementScope, analyze.Binding, *exec.Source) {
	t.Helper()
	op := enginetest.TransactionOperation(t, s)
	scope, err := core.EngineOf(s).StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !scope.Finished {
			_ = scope.Finish(errors.New("test cleanup"))
		}
	})
	_, err = scope.Describe(enginetest.Statement(t, "DELETE FROM t"))
	if err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	ref := scope.LockedRef
	view, err := scope.AcquireDataView(ref)
	if err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	source, err := exec.CaptureSource(op.Context(), view, ref)
	if err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	t.Cleanup(source.Close)
	relation := scope.LockedDescriptor.(*catalog.Relation)
	return scope, analyze.Binding{Ref: ref, Relation: relation, Storage: scope.LockedStorage}, source
}

func TestDeleteCoreAtomicity(t *testing.T) {
	for _, phase := range []string{"late_predicate", "rejected_scan_cancel", "selected_scan_cancel"} {
		t.Run(phase, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1),(2),(3)")
			scope, b, source := deleteProbeScope(t, s)
			originalFrontier := e.Visibility.CommitSeq()
			boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
			sentinel := errors.New("late predicate failure")
			ctx, cancel := context.WithCancel(scope.Op.Context())
			defer cancel()
			scope.Op.Ctx = ctx
			calls := 0
			b.Predicate = queryProbe(boolean.Usage(), func(c *exec.Context) (sqltype.Value, error) {
				calls++
				if phase == "late_predicate" && calls == 2 {
					return sqltype.Value{}, sentinel
				}
				if phase == "rejected_scan_cancel" {
					cancel()
					return sqltype.Boolean(boolean, false), nil
				}
				if phase == "selected_scan_cancel" && calls == 2 {
					cancel()
					return sqltype.Boolean(boolean, false), nil
				}
				return sqltype.Boolean(boolean, true), nil
			})
			result, err := dml.ExecuteDelete(scope.WriteHost(), dml.DeleteInput{Context: scope.ActivateRuntime(scope.Op.Context()), Binding: b, Source: source})
			want := sentinel
			if phase != "late_predicate" {
				want = context.Canceled
			}
			err = scope.Finish(err)
			if !errors.Is(err, want) || result.Count != 0 || result.Rows != nil {
				t.Fatal(result, err, want)
			}
			if e.Visibility.CommitSeq() != originalFrontier || e.Storage.Relations[b.Ref.Address].Ends != nil {
				t.Fatal("partial publication")
			}
			enginetest.TransactionRows(t, s, "SELECT v FROM t ORDER BY v", 1, 2, 3)
			enginetest.LockEmpty(t, e)
		})
	}
}

type deleteCancelAfterCheck struct {
	context.Context
	cancel context.CancelFunc
	checks int
	at     int
}

func (c *deleteCancelAfterCheck) Err() error {
	c.checks++
	err := c.Context.Err()
	if c.checks == c.at {
		c.cancel()
	}
	return err
}

func TestDeleteCorePublicationCancellation(t *testing.T) {
	for _, wins := range []bool{false, true} {
		t.Run(map[bool]string{false: "final_preflight_cancel", true: "installation_wins_late_cancel"}[wins], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1),(2)")
			op := enginetest.TransactionOperation(t, s)
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			result, err := scope.Execute(enginetest.Statement(t, "DELETE FROM t"), 0, nil)
			if err != nil {
				_ = scope.Finish(err)
				t.Fatal(err)
			}
			frontier := e.Visibility.CommitSeq()
			base, cancel := context.WithCancel(op.Context())
			defer cancel()
			if wins {
				op.Ctx = &deleteCancelAfterCheck{Context: base, cancel: cancel, at: 2}
			} else {
				op.Ctx = &cancelAtErrCheck{Context: base, cancel: cancel, at: 2}
			}
			err = scope.Finish(nil)
			if wins {
				if err != nil || result.Count != 2 || base.Err() != context.Canceled || e.Visibility.CommitSeq() != frontier+1 {
					t.Fatal(result, err, base.Err(), e.Visibility.CommitSeq(), frontier)
				}
				enginetest.TransactionRows(t, s, "SELECT v FROM t ORDER BY v")
			} else {
				if !errors.Is(err, context.Canceled) || e.Visibility.CommitSeq() != frontier {
					t.Fatal(err, e.Visibility.CommitSeq(), frontier)
				}
				enginetest.TransactionRows(t, s, "SELECT v FROM t ORDER BY v", 1, 2)
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestDeleteCoreExplicitFailure(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(1),(2),(3)")
	ref := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(7)")
	deleteCount(t, a, "DELETE FROM t WHERE v=3", 1)
	scope, binding, source := deleteProbeScope(t, a)
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	sentinel := errors.New("later row predicate")
	calls := 0
	binding.Predicate = queryProbe(boolean.Usage(), func(*exec.Context) (sqltype.Value, error) {
		calls++
		if calls == 2 {
			return sqltype.Value{}, sentinel
		}
		return sqltype.Boolean(boolean, true), nil
	})
	_, err := dml.ExecuteDelete(scope.WriteHost(), dml.DeleteInput{Context: scope.ActivateRuntime(scope.Op.Context()), Binding: binding, Source: source})
	if err = scope.Finish(err); err != sentinel {
		t.Fatal(err)
	}
	if a.TransactionState() != session.TxAborted || a.Transactions.Writes != nil || a.Transactions.Deletes != nil || a.Transactions.Ticket != nil {
		t.Fatal("failure did not discard earlier commands")
	}
	op := enginetest.TransactionOperation(t, a)
	_, err = e.Execute(op, enginetest.Statement(t, "DELETE FROM t"), 0, nil)
	enginetest.State(t, err, "25P02")
	op.Done()
	enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 2, 3)
	enginetest.TransactionEnd(t, a, ref, false)
	enginetest.LockEmpty(t, e)
}

func TestDeleteCoreStageAuthority(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1),(1)")
	scope, b, source := deleteProbeScope(t, s)
	version := source.Committed[0]
	candidate := visibility.DeleteCandidate{Ref: b.Ref, Storage: b.Storage, Row: version.Row, Version: version.Version, Ordinal: 0}
	copiedScope := *scope
	if err := core.ScopeAcquireRow(&copiedScope, candidate); err == nil {
		t.Fatal("copied scope acquired a row")
	}
	var absent *core.WriteStage
	if _, err := absent.Delete(candidate); err == nil {
		t.Fatal("nil stage accepted")
	}
	if _, err := scope.Writes().Delete(candidate); err == nil {
		t.Fatal("ungranted deletion accepted")
	}
	if err := core.ScopeAcquireRow(scope, candidate); err != nil {
		t.Fatal(err)
	}
	copied := *scope.Writes()
	if _, err := copied.Delete(candidate); err == nil {
		t.Fatal("copied stage accepted")
	}
	invalidStorage := *candidate.Storage
	invalid := candidate
	invalid.Storage = &invalidStorage
	if err := core.ScopeAcquireRow(scope, invalid); err == nil {
		t.Fatal("copied storage acquired a row")
	}
	if _, err := scope.Writes().Delete(invalid); err == nil {
		t.Fatal("copied storage accepted")
	}
	invalid = candidate
	invalid.Version++
	if _, err := scope.Writes().Delete(invalid); err == nil {
		t.Fatal("wrong version accepted")
	}
	invalid = candidate
	invalid.Ordinal = 1
	if _, err := scope.Writes().Delete(invalid); err == nil {
		t.Fatal("equal-value row substituted")
	}
	if ok, err := scope.Writes().Delete(candidate); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if _, err := scope.Writes().Delete(candidate); err == nil {
		t.Fatal("duplicate stage accepted")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := scope.Writes().Delete(candidate); err == nil {
		t.Fatal("finished stage accepted")
	}
	enginetest.TransactionRows(t, s, "SELECT v FROM t ORDER BY v", 1)
	enginetest.LockEmpty(t, e)
}

func TestDeleteCoreStageShapeAuthority(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1),(2)")
	scope, b, source := deleteProbeScope(t, s)
	version := source.Committed[0]
	candidate := visibility.DeleteCandidate{Ref: b.Ref, Storage: b.Storage, Row: version.Row, Version: version.Version, Ordinal: 0}
	if err := core.ScopeAcquireRow(scope, candidate); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	replacement := *b.Storage
	e.Storage.InstallRelationLocked(b.Ref.Address, &replacement)
	e.Mu.Unlock()
	if _, err := scope.Writes().Delete(candidate); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("changed shape admitted staged deletion", err)
	}
	if err := core.ScopeAcquireRow(scope, candidate); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("changed shape admitted row acquisition", err)
	}
	_ = scope.Finish(ids.ErrCatalogReplaced)
	enginetest.TransactionRows(t, s, "SELECT v FROM t ORDER BY v", 1, 2)
	enginetest.LockEmpty(t, e)
}

func TestDeleteCoreCombinedPreflight(t *testing.T) {
	for _, failure := range []string{"wrong_version", "copied_storage", "replaced_shape", "exhausted_frontier", "stale_epoch"} {
		t.Run(failure, func(t *testing.T) {
			e := engine.New()
			a := enginetest.StartSession(t, e, nil)
			b := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, b, "CREATE TABLE u(v INT)")
			enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(1),(2)")
			enginetest.SessionSQL(t, b, "INSERT INTO u VALUES(9)")
			ref := enginetest.TransactionBegin(t, a)
			deleteCount(t, a, "DELETE FROM t WHERE v=1", 1)
			enginetest.SessionSQL(t, a, "INSERT INTO u VALUES(7)")
			target := catalogRef(t, e, e.CatalogRead(), "t")
			frontier := e.Visibility.CommitSeq()
			switch failure {
			case "wrong_version":
				for id, candidate := range a.Transactions.Deletes[target.Address].Rows {
					candidate.Version++
					a.Transactions.Deletes[target.Address].Rows[id] = candidate
				}
			case "copied_storage":
				for id, candidate := range a.Transactions.Deletes[target.Address].Rows {
					copied := *candidate.Storage
					candidate.Storage = &copied
					a.Transactions.Deletes[target.Address].Rows[id] = candidate
				}
			case "replaced_shape":
				// Preserve payload and width: only the exact shape generation changes.
				replacement := *e.Storage.Relations[target.Address]
				e.Mu.Lock()
				e.Storage.InstallRelationLocked(target.Address, &replacement)
				e.Mu.Unlock()
			case "exhausted_frontier":
				e.Visibility.SetCommitSeqLocked(ids.CommitSeq(^uint64(0)))
				frontier = e.Visibility.CommitSeq()
			case "stale_epoch":
				e.Catalog.PublishLocked(catalog.CloneState(e.Catalog.State))
				e.Catalog.State.SetEpoch(e.Catalog.State.Epoch() + 1)
			}
			op := enginetest.TransactionOperation(t, a)
			_, err := e.Commit(op, ref)
			op.Done()
			if err == nil || e.Visibility.CommitSeq() != frontier || e.Storage.Relations[target.Address].Ends != nil {
				t.Fatal("failed combined batch published", err)
			}
			// Restore only the deliberately exhausted test frontier to permit read witnesses.
			if failure == "exhausted_frontier" {
				e.Visibility.SetCommitSeqLocked(2)
			}
			enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 2)
			enginetest.TransactionRows(t, b, "SELECT v FROM u ORDER BY v", 9)
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestDeleteCoreDeletedScanCancellation(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1),(2),(3)")
	deleteCount(t, s, "DELETE FROM t", 3)
	scope, err := e.StartStatement(enginetest.TransactionOperation(t, s))
	if err != nil {
		t.Fatal(err)
	}
	_, err = scope.Describe(enginetest.Statement(t, "DELETE FROM t"))
	if err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	view, err := scope.AcquireDataView(scope.LockedRef)
	if err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(scope.Op.Context())
	defer cancel()
	ctx := &cancelAtErrCheck{Context: base, cancel: cancel, at: 2}
	scope.Op.Ctx = ctx
	root := e.Storage.Relations[scope.LockedRef.Address].Ends
	target := scope.LockedRef // Finish retires admission; retain the exact witness.
	frontier := e.Visibility.CommitSeq()
	source, err := exec.CaptureSource(ctx, view, scope.LockedRef)
	if source != nil || !errors.Is(err, context.Canceled) || ctx.checks.Load() != 2 {
		_ = scope.Finish(err)
		t.Fatal(source, err, ctx.checks.Load())
	}
	if err = scope.Finish(err); !errors.Is(err, context.Canceled) || e.Storage.Relations[target.Address].Ends != root || e.Visibility.CommitSeq() != frontier {
		t.Fatal(err, "deleted scan changed state")
	}
	enginetest.TransactionRows(t, s, "SELECT v FROM t ORDER BY v")
	enginetest.LockEmpty(t, e)
}
