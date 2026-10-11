package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqltype"
)

// This fixture exercises package admission/binding/capture functions, not the
// public driver. Captures use exactly the executor's combined relation set.
func updateFromBoundSources(t *testing.T, e *Engine, session *Session, ctx context.Context, q string) (*Operation, *StatementScope, binding, *querySource, *querySource) {
	t.Helper()
	op, err := session.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var scope *StatementScope
	var source, from *querySource
	// Register ownership before parsing, admission or capture can fail. Normal
	// callers still release immediately; these idempotent fallbacks also cover
	// Fatal/panic exits before a caller reaches Finish/Done.
	t.Cleanup(func() {
		if from != nil {
			from.close()
		}
		if source != nil {
			source.close()
		}
		if scope != nil && !scope.finished {
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
	ast := statement(t, q)
	if err = scope.gate(ast); err != nil {
		fail(err)
	}
	if err = scope.acquireRelation(ast, true); err != nil {
		fail(err)
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	var b binding
	var view *VisibilityView
	if err == nil {
		b, err = e.describe(op.ctx, ast, auth, scope.lockedRef, scope.input, op, nil, &scope.queryLockAdmission)
	}
	if err == nil {
		err = e.initializeQueryLocksLocked(scope, nil, b.querySources)
	}
	if err == nil {
		err = e.captureCommentSnapshotLocked(scope)
	}
	var refs []ObjectRef
	if err == nil {
		refs, err = e.queryTableRefsLocked(auth.catalog, b.querySources, []ObjectRef{b.ref}, scope.op.session.token)
	}
	if err == nil {
		_, err = scope.acquireDataViewsLocked(e, refs)
	}
	if err == nil {
		view, err = scope.acquireDataViewLocked(e, b.ref)
	}
	if err == nil {
		source, err = captureQuerySource(op.ctx, view, b.ref)
	}
	if err == nil {
		from, err = e.captureQuerySourcesLocked(scope, b.querySources, false)
	}
	e.mu.Unlock()
	if err != nil {
		if source != nil {
			source.close()
		}
		fail(err)
	}
	return op, scope, b, source, from
}

func TestUpdateFromFramesOriginsAndPhysicalTargets(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE t(id INT,a INT,b INT)", "CREATE TABLE s(k BIGINT,v VARCHAR(7))", "CREATE TABLE r(k INT)", "CREATE TABLE unused(k INT)", "INSERT INTO t VALUES(1,10,20),(1,10,20)", "INSERT INTO s VALUES(1,'x'),(1,'y')", "INSERT INTO r VALUES(1)"} {
		sessionSQL(t, a, q)
	}
	op, scope, b, target, from := updateFromBoundSources(t, e, a, context.Background(), "UPDATE t AS dst SET a=dst.b,b=dst.a FROM s JOIN r USING(k) WHERE dst.id=k RETURNING dst.*,k,s.k,r.k,s.v,old.a,new.a")
	data := target.view.read()
	if len(data.committed) != 3 || len(b.querySources) != 2 || from.sources[0].view.read() != data || from.sources[1].view.read() != data {
		t.Fatal("incoherent target/source capture", data)
	}
	unused := catalogRef(t, e, target.view.Catalog(), "unused")
	if _, found := data.committed[unused.Address]; found {
		t.Fatal("unrelated relation captured")
	}
	metadata := b.returning.schema
	if len(metadata) != 9 || metadata[3].Usage.Type.ID() != sqltype.Int8 || metadata[3].OriginTable != metadata[4].OriginTable || metadata[3].OriginAttribute != metadata[4].OriginAttribute || metadata[3].OriginTable == metadata[5].OriginTable || metadata[6].Usage.Typmod != 11 {
		t.Fatal(metadata)
	}
	original := append([]rowVersion(nil), target.committed...)
	r, err := e.executeUpdate(op.ctx, scope, b, target, from, nil, "public")
	if err != nil || r.Count != 2 {
		scope.Finish(err)
		op.Done()
		t.Fatal(r, err)
	}
	got := tuples(r)
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
	if target.view != nil || from.sources != nil || len(scope.querySources) != 0 {
		t.Fatal("capture ownership leaked")
	}
	store := e.storage[b.ref.Address]
	if len(store.versions) != 2 || store.versions[0].row != original[0].row || store.versions[1].row != original[1].row || original[0].row == original[1].row || store.versions[0].version == original[0].version || testExport(original[0].values[1]) != int64(10) {
		t.Fatal("physical identity/head immutability", store.versions, original)
	}
	transactionRows(t, a, "SELECT a FROM t", 20, 20)
	lockEmpty(t, e)
}

func TestUpdateFromLosingPairsDemandAndFailureCleanup(t *testing.T) {
	for _, failLast := range []bool{false, true} {
		t.Run(map[bool]string{false: "demand", true: "late_error"}[failLast], func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE t(id INT,a INT)", "CREATE TABLE s(k INT)", "INSERT INTO t VALUES(1,10),(2,20)", "INSERT INTO s VALUES(1),(1),(1),(2),(2),(2)"} {
				sessionSQL(t, a, q)
			}
			op, scope, b, target, from := updateFromBoundSources(t, e, a, context.Background(), "UPDATE t SET a=id FROM s WHERE id=k RETURNING old.a,new.a,s.k")
			children := append([]*querySource(nil), from.sources...)
			saved := append([]rowVersion(nil), target.committed...)
			calls, outputs := 0, 0
			sentinel := errors.New("losing UPDATE FROM assignment failure")
			rhs := b.update.assignments[0]
			b.update.assignments[0] = queryProbe(rhs.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
				calls++
				if failLast && calls == 6 {
					return sqltype.Value{}, sentinel
				}
				return rhs.Eval(c)
			})
			out := b.returning.expressions[0]
			b.returning.expressions[0] = queryProbe(out.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) { outputs++; return out.Eval(c) })
			r, err := e.executeUpdate(op.ctx, scope, b, target, from, nil, "public")
			if calls != 6 || outputs != 2 {
				t.Fatal("pair versus successful-target demand", calls, outputs)
			}
			if failLast {
				if !errors.Is(err, sentinel) || r.Count != 0 || r.Rows != nil || len(scope.stage.rows) != 0 {
					t.Fatal(r, err)
				}
			} else {
				if err != nil || r.Count != 2 {
					t.Fatal(r, err)
				}
				got := tuples(r)
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
				if child.view != nil || child.committed != nil || child.own != nil {
					t.Fatal("orphan source capture", child)
				}
			}
			if target.view != nil || from.sources != nil || len(scope.querySources) != 0 {
				t.Fatal("orphan statement capture")
			}
			if testExport(saved[0].values[1]) != int64(10) || testExport(saved[1].values[1]) != int64(20) {
				t.Fatal("retained predecessor changed")
			}
			if failLast {
				transactionRows(t, a, "SELECT a FROM t ORDER BY id", 10, 20)
			} else {
				transactionRows(t, a, "SELECT a FROM t ORDER BY id", 1, 2)
			}
			lockEmpty(t, e)
		})
	}
}

func TestUpdateFromImmediateGrantFreezesSource(t *testing.T) {
	e := New()
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
	r, err := e.executeUpdate(op.ctx, scope, b, target, from, nil, "public")
	if err != nil || r.Count != 2 {
		t.Fatal(r, err)
	}
	got := tuples(r)
	r.Rows.Close()
	if !reflect.DeepEqual(got, [][]driver.Value{{int64(11), int64(111), int64(100)}, {int64(20), int64(220), int64(200)}}) {
		t.Fatal(got)
	}
	if err = scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()
	transactionRows(t, a, "SELECT v FROM t ORDER BY v", 30, 111, 220)
	lockEmpty(t, e)
}
