package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func deleteProbeScope(t *testing.T, s *session.Session) (*StatementScope, analyze.Binding, *exec.Source) {
	t.Helper()
	op := transactionOperation(t, s)
	scope, err := EngineOf(s).StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !scope.Finished {
			_ = scope.Finish(errors.New("test cleanup"))
		}
	})
	_, err = scope.Describe(statement(t, "DELETE FROM t"))
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
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(v INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(1),(2),(3)")
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
			result, err := dml.ExecuteDelete(scope.WriteHost(), dml.DeleteInput{Binding: b, Source: source})
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
			transactionRows(t, s, "SELECT v FROM t ORDER BY v", 1, 2, 3)
			lockEmpty(t, e)
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
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(v INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(1),(2)")
			op := transactionOperation(t, s)
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			result, err := scope.Execute(statement(t, "DELETE FROM t"), 0, nil)
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
				transactionRows(t, s, "SELECT v FROM t ORDER BY v")
			} else {
				if !errors.Is(err, context.Canceled) || e.Visibility.CommitSeq() != frontier {
					t.Fatal(err, e.Visibility.CommitSeq(), frontier)
				}
				transactionRows(t, s, "SELECT v FROM t ORDER BY v", 1, 2)
			}
			lockEmpty(t, e)
		})
	}
}

func TestDeleteCoreExplicitFailure(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT)")
	sessionSQL(t, b, "INSERT INTO t VALUES(1),(2),(3)")
	ref := transactionBegin(t, a)
	sessionSQL(t, a, "INSERT INTO t VALUES(7)")
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
	_, err := dml.ExecuteDelete(scope.WriteHost(), dml.DeleteInput{Binding: binding, Source: source})
	if err = scope.Finish(err); err != sentinel {
		t.Fatal(err)
	}
	if a.TransactionState() != session.TxAborted || a.Transactions.Writes != nil || a.Transactions.Deletes != nil || a.Transactions.Ticket != nil {
		t.Fatal("failure did not discard earlier commands")
	}
	op := transactionOperation(t, a)
	_, err = e.Execute(op, statement(t, "DELETE FROM t"), 0, nil)
	state(t, err, "25P02")
	op.Done()
	transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 2, 3)
	transactionEnd(t, a, ref, false)
	lockEmpty(t, e)
}

func TestDeleteCoreStageAuthority(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(v INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1),(1)")
	scope, b, source := deleteProbeScope(t, s)
	version := source.Committed[0]
	candidate := visibility.DeleteCandidate{Ref: b.Ref, Storage: b.Storage, Row: version.Row, Version: version.Version, Ordinal: 0}
	copiedScope := *scope
	if err := copiedScope.acquireRow(candidate); err == nil {
		t.Fatal("copied scope acquired a row")
	}
	var absent *WriteStage
	if _, err := absent.Delete(candidate); err == nil {
		t.Fatal("nil stage accepted")
	}
	if _, err := scope.Writes().Delete(candidate); err == nil {
		t.Fatal("ungranted deletion accepted")
	}
	if err := scope.acquireRow(candidate); err != nil {
		t.Fatal(err)
	}
	copied := *scope.Writes()
	if _, err := copied.Delete(candidate); err == nil {
		t.Fatal("copied stage accepted")
	}
	invalidStorage := *candidate.Storage
	invalid := candidate
	invalid.Storage = &invalidStorage
	if err := scope.acquireRow(invalid); err == nil {
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
	transactionRows(t, s, "SELECT v FROM t ORDER BY v", 1)
	lockEmpty(t, e)
}

func TestDeleteCoreStageShapeAuthority(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(v INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1),(2)")
	scope, b, source := deleteProbeScope(t, s)
	version := source.Committed[0]
	candidate := visibility.DeleteCandidate{Ref: b.Ref, Storage: b.Storage, Row: version.Row, Version: version.Version, Ordinal: 0}
	if err := scope.acquireRow(candidate); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	replacement := *b.Storage
	e.Storage.InstallRelationLocked(b.Ref.Address, &replacement)
	e.Mu.Unlock()
	if _, err := scope.Writes().Delete(candidate); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("changed shape admitted staged deletion", err)
	}
	if err := scope.acquireRow(candidate); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("changed shape admitted row acquisition", err)
	}
	_ = scope.Finish(ids.ErrCatalogReplaced)
	transactionRows(t, s, "SELECT v FROM t ORDER BY v", 1, 2)
	lockEmpty(t, e)
}

func TestDeleteCoreCombinedPreflight(t *testing.T) {
	for _, failure := range []string{"wrong_version", "copied_storage", "replaced_shape", "exhausted_frontier", "stale_epoch"} {
		t.Run(failure, func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			b := sessionStart(t, e, nil)
			sessionSQL(t, b, "CREATE TABLE t(v INT)")
			sessionSQL(t, b, "CREATE TABLE u(v INT)")
			sessionSQL(t, b, "INSERT INTO t VALUES(1),(2)")
			sessionSQL(t, b, "INSERT INTO u VALUES(9)")
			ref := transactionBegin(t, a)
			deleteCount(t, a, "DELETE FROM t WHERE v=1", 1)
			sessionSQL(t, a, "INSERT INTO u VALUES(7)")
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
			op := transactionOperation(t, a)
			_, err := e.Commit(op, ref)
			op.Done()
			if err == nil || e.Visibility.CommitSeq() != frontier || e.Storage.Relations[target.Address].Ends != nil {
				t.Fatal("failed combined batch published", err)
			}
			// Restore only the deliberately exhausted test frontier to permit read witnesses.
			if failure == "exhausted_frontier" {
				e.Visibility.SetCommitSeqLocked(2)
			}
			transactionRows(t, b, "SELECT v FROM t ORDER BY v", 1, 2)
			transactionRows(t, b, "SELECT v FROM u ORDER BY v", 9)
			lockEmpty(t, e)
		})
	}
}

func TestDeleteCoreDeletedScanCancellation(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(v INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1),(2),(3)")
	deleteCount(t, s, "DELETE FROM t", 3)
	scope, err := e.StartStatement(transactionOperation(t, s))
	if err != nil {
		t.Fatal(err)
	}
	_, err = scope.Describe(statement(t, "DELETE FROM t"))
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
	transactionRows(t, s, "SELECT v FROM t ORDER BY v")
	lockEmpty(t, e)
}
