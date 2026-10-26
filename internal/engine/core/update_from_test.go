package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// This fixture exercises package admission/binding/capture functions, not the
// public driver. Captures use exactly the executor's combined relation set.
func updateFromBoundSources(t *testing.T, e *core.Engine, session *session.Session, ctx context.Context, q string) (*session.Operation, *core.StatementScope, analyze.Binding, *exec.Source, *exec.Source) {
	t.Helper()
	op, err := session.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var scope *core.StatementScope
	var source, from *exec.Source
	// Register ownership before parsing, admission or capture can fail. Normal
	// callers still release immediately; these idempotent fallbacks also cover
	// Fatal/panic exits before a caller reaches Finish/Done.
	t.Cleanup(func() {
		if from != nil {
			from.Close()
		}
		if source != nil {
			source.Close()
		}
		if scope != nil && !scope.Finished {
			scope.Finish(errors.New("abandoned mutation capture fixture"))
		}
		op.Done()
	})
	scope, err = e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	fail := func(err error) { scope.Finish(err); op.Done(); t.Fatal(err) }
	ast := enginetest.Statement(t, q)
	if err = scope.Gate(ast); err != nil {
		fail(err)
	}
	if err = scope.AcquireRelation(ast, true); err != nil {
		fail(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var b analyze.Binding
	var view *visibility.View
	if err == nil {
		b, err = core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
		scope.RoutineAuth, scope.RoutineDependencies = auth, b.Dependencies
	}
	if err == nil {
		err = core.EngineInitializeQueryLocksLocked(e, scope, nil, b.QuerySources)
	}
	if err == nil {
		err = e.CaptureCommentSnapshotLocked(scope)
	}
	var refs []ids.ObjectRef
	if err == nil {
		refs, err = e.QueryTableRefsLocked(auth, auth.State.CurrentUser, b.QuerySources, []ids.ObjectRef{b.Ref}, scope.Op.Session.SessionToken, plan.RowMark{})
	}
	if err == nil {
		_, err = scope.AcquireDataViewsLocked(e, refs)
	}
	if err == nil {
		view, err = core.ScopeAcquireDataViewLocked(scope, e, b.Ref)
	}
	if err == nil {
		source, err = exec.CaptureSource(op.Ctx, view, b.Ref)
	}
	if err == nil {
		from, err = e.CaptureQuerySourcesLocked(scope, b.QuerySources, false)
	}
	e.Mu.Unlock()
	if err != nil {
		if source != nil {
			source.Close()
		}
		fail(err)
	}
	return op, scope, b, source, from
}

func TestUpdateFromFramesOriginsAndPhysicalTargets(t *testing.T) {
	e := engine.New()
	a := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT,a INT,b INT)", "CREATE TABLE s(k BIGINT,v VARCHAR(7))", "CREATE TABLE r(k INT)", "CREATE TABLE unused(k INT)", "INSERT INTO t VALUES(1,10,20),(1,10,20)", "INSERT INTO s VALUES(1,'x'),(1,'y')", "INSERT INTO r VALUES(1)"} {
		sessionSQL(t, a, q)
	}
	op, scope, b, target, from := updateFromBoundSources(t, e, a, context.Background(), "UPDATE t AS dst SET a=dst.b,b=dst.a FROM s JOIN r USING(k) WHERE dst.id=k RETURNING dst.*,k,s.k,r.k,s.v,old.a,new.a")
	data := target.View.Data()
	if len(data.Committed) != 3 || len(b.QuerySources) != 2 || from.Sources[0].View.Data() != data || from.Sources[1].View.Data() != data {
		t.Fatal("incoherent target/source capture", data)
	}
	unused := enginetest.CatalogRef(t, e, target.View.Catalog(), "unused")
	if _, found := data.Committed[unused.Address]; found {
		t.Fatal("unrelated relation captured")
	}
	metadata := b.Returning.Schema
	if len(metadata) != 9 || metadata[3].Usage.Type.ID() != sqltype.Int8 || metadata[3].OriginTable != metadata[4].OriginTable || metadata[3].OriginAttribute != metadata[4].OriginAttribute || metadata[3].OriginTable == metadata[5].OriginTable || metadata[6].Usage.Typmod != 11 {
		t.Fatal(metadata)
	}
	original := append([]storage.RowVersion(nil), target.Committed...)
	r, err := core.ExecuteUpdate(op.Ctx, scope, b, target, from, nil, "public")
	if err != nil || r.Count != 2 {
		scope.Finish(err)
		op.Done()
		t.Fatal(r, err)
	}
	got := enginetest.Tuples(r)
	r.Rows.Close()
	if len(got) != 2 {
		t.Fatal(got)
	}
	for _, row := range got {
		if !reflect.DeepEqual(row[:6], []driver.Value{int64(1), int64(20), int64(10), int64(1), int64(1), int64(1)}) || row[7] != int64(10) || row[8] != int64(20) || (row[6] != "x" && row[6] != "y") {
			t.Fatal(row)
		}
	}
	if err = scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()
	if target.View != nil || from.Sources != nil || len(scope.QuerySources) != 0 {
		t.Fatal("capture ownership leaked")
	}
	store := e.Storage.Relations[b.Ref.Address]
	if len(store.Versions) != 2 || store.Versions[0].Row != original[0].Row || store.Versions[1].Row != original[1].Row || original[0].Row == original[1].Row || store.Versions[0].Version == original[0].Version || enginetest.Export(original[0].Values[1]) != int64(10) {
		t.Fatal("physical identity/head immutability", store.Versions, original)
	}
	transactionRows(t, a, "SELECT a FROM t", 20, 20)
	enginetest.LockEmpty(t, e)
}

func TestUpdateFromLosingPairsDemandAndFailureCleanup(t *testing.T) {
	for _, failLast := range []bool{false, true} {
		t.Run(map[bool]string{false: "demand", true: "late_error"}[failLast], func(t *testing.T) {
			e := engine.New()
			a := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(id INT,a INT)", "CREATE TABLE s(k INT)", "INSERT INTO t VALUES(1,10),(2,20)", "INSERT INTO s VALUES(1),(1),(1),(2),(2),(2)"} {
				sessionSQL(t, a, q)
			}
			op, scope, b, target, from := updateFromBoundSources(t, e, a, context.Background(), "UPDATE t SET a=id FROM s WHERE id=k RETURNING old.a,new.a,s.k")
			children := append([]*exec.Source(nil), from.Sources...)
			saved := append([]storage.RowVersion(nil), target.Committed...)
			calls, outputs := 0, 0
			sentinel := errors.New("losing UPDATE FROM assignment failure")
			rhs := b.Update.Assignments[0]
			b.Update.Assignments[0] = queryProbe(rhs.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
				calls++
				if failLast && calls == 6 {
					return sqltype.Value{}, sentinel
				}
				return rhs.Eval(c)
			})
			out := b.Returning.Expressions[0]
			b.Returning.Expressions[0] = queryProbe(out.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) { outputs++; return out.Eval(c) })
			r, err := core.ExecuteUpdate(op.Ctx, scope, b, target, from, nil, "public")
			if calls != 6 || outputs != 2 {
				t.Fatal("pair versus successful-target demand", calls, outputs)
			}
			if failLast {
				if !errors.Is(err, sentinel) || r.Count != 0 || r.Rows != nil {
					t.Fatal(r, err)
				}
			} else {
				if err != nil || r.Count != 2 {
					t.Fatal(r, err)
				}
				got := enginetest.Tuples(r)
				r.Rows.Close()
				if !reflect.DeepEqual(got, [][]driver.Value{{int64(10), int64(1), int64(1)}, {int64(20), int64(2), int64(2)}}) {
					t.Fatal(got)
				}
			}
			if finishErr := scope.Finish(err); !errors.Is(finishErr, err) {
				t.Fatal(finishErr, err)
			}
			op.Done()
			for _, child := range children {
				if child.View != nil || child.Committed != nil || child.Own != nil {
					t.Fatal("orphan source capture", child)
				}
			}
			if target.View != nil || from.Sources != nil || len(scope.QuerySources) != 0 {
				t.Fatal("orphan statement capture")
			}
			if enginetest.Export(saved[0].Values[1]) != int64(10) || enginetest.Export(saved[1].Values[1]) != int64(20) {
				t.Fatal("retained predecessor changed")
			}
			if failLast {
				transactionRows(t, a, "SELECT a FROM t ORDER BY id", 10, 20)
			} else {
				transactionRows(t, a, "SELECT a FROM t ORDER BY id", 1, 2)
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestUpdateFromImmediateGrantFreezesSource(t *testing.T) {
	e := engine.New()
	a := sessionStart(t, e, nil)
	bSession := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "CREATE TABLE s(k INT,n INT)", "INSERT INTO t VALUES(1,10),(2,20)", "INSERT INTO s VALUES(1,100),(2,200)"} {
		sessionSQL(t, a, q)
	}
	op, scope, b, target, from := updateFromBoundSources(t, e, bSession, context.Background(), "UPDATE t SET v=t.v+s.n FROM s WHERE t.id=s.k RETURNING old.v,new.v,s.n")
	sessionSQL(t, a, "UPDATE t SET v=11 WHERE id=1")
	sessionSQL(t, a, "UPDATE s SET n=999")
	sessionSQL(t, a, "INSERT INTO t VALUES(1,30)")
	sessionSQL(t, a, "INSERT INTO s VALUES(1,400)")
	r, err := core.ExecuteUpdate(op.Ctx, scope, b, target, from, nil, "public")
	if err != nil || r.Count != 2 {
		t.Fatal(r, err)
	}
	got := enginetest.Tuples(r)
	r.Rows.Close()
	if !reflect.DeepEqual(got, [][]driver.Value{{int64(11), int64(111), int64(100)}, {int64(20), int64(220), int64(200)}}) {
		t.Fatal(got)
	}
	if err = scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()
	transactionRows(t, a, "SELECT v FROM t ORDER BY v", 30, 111, 220)
	enginetest.LockEmpty(t, e)
}
