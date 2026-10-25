package core_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// This fixture binds the real INSERT plan and starts its private output owner;
// the tests replace only the producer/projection seam that they observe.
func insertReturningAnalyzedScope(t *testing.T, e *core.Engine, s *session.Session, query string) (*session.Operation, *core.StatementScope, analyze.Binding) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !scope.Finished {
			_ = scope.Finish(errors.New("unit rollback"))
		}
		if scope.InsertReturning != nil {
			_ = scope.InsertReturning.Rows.Close()
		}
		op.Done()
	})
	stmt := enginetest.Statement(t, query)
	if err := scope.Gate(stmt); err != nil {
		t.Fatal(err)
	}
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var b analyze.Binding
	if err == nil {
		b, err = core.Describe(op.Context(), e.AnalysisHost(op), stmt, auth, ref, scope.Input, nil, nil)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	conflict := dml.NewInsertConflictState(b.Insert.Conflict)
	scope.InsertConflict = conflict
	if conflict != nil {
		c := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Input: scope.Input, Output: scope.Output, Host: core.HostOf(scope)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		if err := conflict.SelectArbiters(&c, b.Relation, e.CatalogRead()); err != nil {
			t.Fatal(err)
		}
		if err := dml.PreflightConflictUpdate(conflict, &c); err != nil {
			t.Fatal(err)
		}
	}
	dml.BeginInsertReturning(scope.WriteHost(), b)
	return op, scope, b
}

func TestInsertReturningModes(t *testing.T) {
	_, _, relation := generatedUnitDescriptor(t)
	typ := relation.Columns[0].Usage.Type
	three, _ := sqltype.Integer(typ, 3)
	seven, _ := sqltype.Integer(typ, 7)
	column := plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage()}, Scope: 1, Frame: 4, Slot: 0}
	outer := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 4, Row: []sqltype.Value{three}, OldRow: []sqltype.Value{seven}}}
	inner := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 8, Outer: &outer}}
	calls := 0
	probe := queryProbe(typ.Usage(), func(c *exec.Context) (sqltype.Value, error) { calls++; return column.Eval(c) })
	for _, tc := range []struct {
		name                 string
		old, absent, missing bool
		want                 any
	}{
		{"new", false, false, false, int64(3)},
		{"old", true, false, false, int64(7)},
		{"insert_old", true, false, true, nil},
		{"delete_new", false, true, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outer.OldRow = []sqltype.Value{seven}
			if tc.missing {
				outer.OldRow = nil
			}
			before := calls
			expr := plan.ReturningColumnExpr{ColumnExpr: column, Old: tc.old, Absent: tc.absent, Value: probe}
			v, err := expr.Eval(&inner)
			if err != nil || v.Type() != typ || enginetest.Export(v) != tc.want {
				t.Fatal(v, err)
			}
			wantCalls := before + 1
			if tc.absent || tc.missing {
				wantCalls = before
			}
			if calls != wantCalls || outer.Row[0] != three || inner.Frame != 8 {
				t.Fatal("side evaluated or caller frame changed", calls, outer, inner)
			}
		})
	}
	inner.Outer = nil
	if _, err := (plan.ReturningColumnExpr{ColumnExpr: column, Absent: true}).Eval(&inner); err == nil {
		t.Fatal("absent side silently accepted a missing owning frame")
	}
}

func TestInsertReturningWholeRow(t *testing.T) {
	for _, declaration := range []string{"CREATE TABLE t(v INT)", "CREATE TABLE t()"} {
		t.Run(declaration, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, declaration)
			_, scope, b := insertReturningAnalyzedScope(t, e, s, "INSERT INTO t DEFAULT VALUES RETURNING old,new,t")
			typ := b.Returning.Schema[0].Usage.Type
			if !typ.IsComposite() || typ.ID() != b.Relation.RowType || typ.CompositeLen() != len(b.Relation.Columns) {
				t.Fatal("not actual named target descriptor", typ, b.Relation.RowType)
			}
			row := make([]sqltype.Value, len(b.Relation.Columns))
			for i, col := range b.Relation.Columns {
				row[i] = sqltype.Null(col.Usage.Type)
			}
			c := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.Op.Context()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: row}}
			for i, expr := range b.Returning.Expressions {
				v, err := expr.Eval(&c)
				if err != nil || v.Type() != typ || v.IsNull() != (i == 0) {
					t.Fatal(i, v, err)
				}
				if !analyze.ExpressionUsesFrame(expr, 1) {
					t.Fatal("lost zero-member correlation", i)
				}
				mapped, err := stored.RemapExpression(expr, func(ref ids.ObjectRef) ids.ObjectRef { ref.Address.ObjectID++; return ref }, nil)
				if err != nil || mapped.ResultUsage().Type != typ || !analyze.ExpressionUsesFrame(mapped, 1) {
					t.Fatal("retained row remapping", mapped, err)
				}
			}
		})
	}
}

func TestInsertReturningFinalAdmission(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		for _, failure := range []bool{false, true} {
			t.Run(fmt.Sprintf("conflict_%v/failure_%v", conflict, failure), func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, s, "CREATE TABLE t(k INT UNIQUE,v INT)")
				query := "INSERT INTO t SELECT k,v FROM t"
				if conflict {
					query += " ON CONFLICT(k) DO UPDATE SET v=excluded.v"
				}
				query += " RETURNING v"
				op, scope, b := insertReturningAnalyzedScope(t, e, s, query)
				integer := func(n int64) sqltype.Value {
					v, err := sqltype.Integer(b.Relation.Columns[0].Usage.Type, n)
					if err != nil {
						t.Fatal(err)
					}
					return v
				}
				cursor := &insertConflictInputCursor{schema: b.Relation.Columns, rows: [][]sqltype.Value{{integer(2), integer(20)}, {integer(3), integer(30)}}}
				calls := 0
				injected := errors.New("late private output veto")
				b.Returning.Expressions[0] = queryProbe(b.Relation.Columns[1].Usage, func(c *exec.Context) (sqltype.Value, error) {
					calls++
					if len(scope.Stage.Rows[b.Ref.Address]) != calls || cursor.position != calls {
						t.Fatal("projection preceded final stage or next proposal", calls, cursor.position, scope.Stage.Rows)
					}
					if failure && calls == 2 {
						return sqltype.Value{}, injected
					}
					return c.Row[1], nil
				})
				rows, err := dml.AssembleInsertRows(op.Context(), b, cursor, -1, "public", scope.Input, scope.Output, scope.WriteHost(), true, nil, nil)
				if rows.Values != nil || calls != 2 || cursor.closes != 1 {
					t.Fatal(rows, calls, cursor.closes)
				}
				if failure {
					if !errors.Is(err, injected) || scope.InsertReturning.Rows.Count != 1 {
						t.Fatal(err, scope.InsertReturning.Rows.Count)
					}
				} else if err != nil || scope.InsertReturning.Rows.Count != 2 {
					t.Fatal(err, scope.InsertReturning.Rows.Count)
				}
				if finished := scope.Finish(err); !errors.Is(finished, err) {
					t.Fatal(finished, err)
				}
				op.Done()
				if failure {
					enginetest.TransactionRows(t, s, "SELECT v FROM t ORDER BY k")
				} else {
					enginetest.TransactionRows(t, s, "SELECT v FROM t ORDER BY k", 20, 30)
				}
				enginetest.LockEmpty(t, e)
			})
		}
	}
	t.Run("provider_drift", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TABLE t(k INT,v INT)")
		enginetest.SessionSQL(t, s, "CREATE UNIQUE INDEX u ON t(k)")
		op, scope, b := insertReturningAnalyzedScope(t, e, s, "INSERT INTO t VALUES(1,10) RETURNING v")
		one, _ := sqltype.Integer(b.Relation.Columns[0].Usage.Type, 1)
		ten, _ := sqltype.Integer(b.Relation.Columns[1].Usage.Type, 10)
		row := []sqltype.Value{one, ten}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Input: scope.Input, Output: scope.Output, Host: core.HostOf(scope)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		admitted, err := dml.AdmitInsertRow(scope.WriteHost(), b.Ref, &index.WriteCandidate{Values: row}, index.WriteBatch{}, &evaluation)
		if err != nil || !admitted {
			t.Fatal(admitted, err)
		}
		prior := scope.UniqueBinding
		// Publish an actual installed provider generation through the validated
		// catalog authority, between candidate admission and final staging.
		base := e.CatalogRead()
		indexRef := enginetest.CatalogRef(t, e, base, "u")
		next := *base.MustRelation(indexRef.Address)
		next.ObjectRevision++
		stage := e.BeginCatalogStage(base)
		defer e.DiscardCatalogStage(stage)
		if err := stage.Replace(&next); err != nil {
			t.Fatal(err)
		}
		prepared, err := e.ValidateCatalogStage(op.Context(), stage)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
			t.Fatal(err)
		}
		accepted, retry, err := dml.AppendAcceptedInsert(scope.WriteHost(), b.Ref, &index.WriteCandidate{Values: row})
		if err != nil || accepted || !retry || len(scope.Stage.Rows[b.Ref.Address]) != 0 || scope.InsertReturning.Rows.Count != 0 {
			t.Fatal("stale provider admitted output", accepted, retry, err, scope.Stage.Rows)
		}
		calls := 0
		b.Returning.Expressions[0] = queryProbe(b.Relation.Columns[1].Usage, func(c *exec.Context) (sqltype.Value, error) { calls++; return c.Row[1], nil })
		if _, err := dml.AdmitInsertProducer(scope.WriteHost(), b, &index.WriteCandidate{Values: row}, index.WriteBatch{}, &evaluation); err != nil {
			t.Fatal(err)
		}
		if scope.UniqueBinding == prior || calls != 1 || scope.InsertReturning.Rows.Count != 1 || len(scope.Stage.Rows[b.Ref.Address]) != 1 {
			t.Fatal("retry did not freshly admit exactly one output", calls, scope.InsertReturning.Rows.Count)
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		op.Done()
		enginetest.TransactionRows(t, s, "SELECT v FROM t", 10)
		enginetest.LockEmpty(t, e)
	})
}

func TestInsertReturningProducerClose(t *testing.T) {
	for _, primary := range []bool{false, true} {
		t.Run(fmt.Sprint(primary), func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE t(k INT UNIQUE,v INT)")
			op, scope, b := insertReturningAnalyzedScope(t, e, s, "INSERT INTO t SELECT k,v FROM t RETURNING v")
			integer := func(n int64) sqltype.Value {
				v, err := sqltype.Integer(b.Relation.Columns[0].Usage.Type, n)
				if err != nil {
					t.Fatal(err)
				}
				return v
			}
			closeErr, nextErr := errors.New("required producer close veto"), errors.New("primary producer veto")
			cursor := &insertConflictInputCursor{schema: b.Relation.Columns, rows: [][]sqltype.Value{{integer(2), integer(20)}, {integer(3), integer(30)}}, closeError: closeErr}
			if primary {
				cursor.nextError = nextErr
			}
			rows, err := dml.AssembleInsertRows(op.Context(), b, cursor, -1, "public", scope.Input, scope.Output, scope.WriteHost(), true, nil, nil)
			want, count := closeErr, 2
			if primary {
				want, count = nextErr, 1
			}
			if !errors.Is(err, want) || rows.Values != nil || cursor.closes != 1 || scope.InsertReturning.Rows.Count != count || len(scope.Stage.Rows[b.Ref.Address]) != int(count) {
				t.Fatal("veto did not follow actual staged/output prefix", rows, err, cursor.closes, scope.InsertReturning.Rows.Count)
			}
			if err := scope.Finish(err); !errors.Is(err, want) {
				t.Fatal(err)
			}
			op.Done()
			enginetest.TransactionRows(t, s, "SELECT v FROM t")
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestInsertReturningRowRecheck(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, filtered := range []bool{false, true} {
			t.Run(fmt.Sprintf("commit_%v/filtered_%v", commit, filtered), func(t *testing.T) {
				e := engine.New()
				holder, worker, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, observer, "CREATE TABLE t(k INT UNIQUE,v INT)")
				enginetest.SessionSQL(t, observer, "INSERT INTO t VALUES(1,10)")
				ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
				id := e.Storage.Relations[ref.Address].Versions[0].Row
				holderTx := enginetest.TransactionBegin(t, holder)
				enginetest.TransactionRows(t, holder, "SELECT v FROM t FOR UPDATE", 10)
				query := "INSERT INTO t VALUES(1,20) ON CONFLICT(k) DO UPDATE SET v=t.v+excluded.v"
				if filtered {
					query += " WHERE FALSE"
				}
				query += " RETURNING old.v,new.v"
				running := conflictUpdateStartWorker(t, e, worker, query)
				ticket, mode := conflictUpdatePendingRow(t, e, ref, id)
				if mode != lock.ForNoKeyUpdate || ticket == nil {
					t.Fatal("not exact pending row resource", ticket, mode)
				}
				enginetest.SessionSQL(t, holder, "UPDATE t SET v=40 WHERE k=1")
				enginetest.TransactionEnd(t, holder, holderTx, commit)
				outcome := running.await(t)
				if outcome.err != nil || outcome.result.Rows == nil {
					t.Fatal(outcome)
				}
				defer outcome.result.Rows.Close()
				row := make([]sqltype.Value, 2)
				ok, err := outcome.result.Rows.Next(context.Background(), row)
				before := int64(10)
				if commit {
					before = 40
				}
				if filtered {
					if ok || err != nil || outcome.result.Count != 0 {
						t.Fatal("filtered action emitted output", ok, err, outcome.result.Count)
					}
				} else {
					if !ok || err != nil || outcome.result.Count != 1 || enginetest.Export(row[0]) != before || enginetest.Export(row[1]) != before+20 {
						t.Fatal("not actual predecessor/replacement", row, err, outcome.result.Count)
					}
					if ok, err := outcome.result.Rows.Next(context.Background(), row); ok || err != nil {
						t.Fatal(ok, err)
					}
					before += 20
				}
				enginetest.TransactionRows(t, observer, "SELECT v FROM t", before)
				enginetest.LockEmpty(t, e)
			})
		}
	}
}

func TestInsertReturningAuthorizationAfterWait(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("conflict_%v/prepared_%v", conflict, prepared), func(t *testing.T) {
				e := engine.New()
				holder, worker, admin := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
				for _, q := range []string{"CREATE USER output_waiter", "CREATE TABLE t(k INT UNIQUE,v INT,secret INT)", "GRANT INSERT,UPDATE ON t TO output_waiter", "GRANT SELECT(k,v,secret) ON t TO output_waiter"} {
					enginetest.SessionSQL(t, admin, q)
				}
				enginetest.SessionSQL(t, worker, "SET SESSION AUTHORIZATION output_waiter")
				ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
				var indexAddress ids.ObjectAddress
				for address, descriptor := range e.Catalog.State.Descriptors() {
					if index, ok := descriptor.(*catalog.Relation); ok && index.SQLIndex != nil && index.SQLIndex.Parent == ref.Address && index.SQLIndex.Unique {
						indexAddress = address
					}
				}
				q := "INSERT INTO t VALUES(1,20,99) ON CONFLICT(k) DO UPDATE SET v=excluded.v RETURNING secret"
				if prepared {
					enginetest.SessionSQL(t, worker, "PREPARE held AS "+q)
					q = "EXECUTE held"
				}
				if conflict {
					enginetest.SessionSQL(t, admin, "INSERT INTO t VALUES(1,10,77)")
				}
				tx := enginetest.TransactionBegin(t, holder)
				if conflict {
					enginetest.TransactionRows(t, holder, "SELECT v FROM t FOR UPDATE", 10)
				} else {
					enginetest.SessionSQL(t, holder, "INSERT INTO t VALUES(1,10,77)")
				}
				running := conflictUpdateStartWorker(t, e, worker, q)
				if conflict {
					id := e.Storage.Relations[ref.Address].Versions[0].Row
					ticket, mode := conflictUpdatePendingRow(t, e, ref, id)
					if ticket == nil || mode != lock.ForNoKeyUpdate {
						t.Fatal("not exact pending output worker", ticket, mode)
					}
				} else {
					enginetest.LockPending(t, e, 1)
					lockState := e.Locks.Inspect()
					exact := false
					for resource, entry := range lockState.Resources {
						if len(entry.Pending) == 1 && resource.Key() != nil && entry.Pending[0].Resource == resource && entry.Pending[0].Ticket != nil {
							exact = resource.ObjectRef.Address == indexAddress
						}
					}
					if !exact {
						t.Fatal("not actual target unique-key wait")
					}
				}
				enginetest.SessionSQL(t, admin, "REVOKE SELECT(secret) ON t FROM output_waiter")
				enginetest.TransactionEnd(t, holder, tx, false)
				outcome := running.await(t)
				conflictUpdateWantError(t, outcome, "42501", "permission denied for table t")
				var d *sqlstate.Error
				if !errors.As(outcome.err, &d) || d.Detail != "" || d.Hint != "" || d.Position != "" || d.InternalPosition != "" || d.InternalQuery != "" || d.Context != "" || d.Schema != "" || d.Table != "" || d.Column != "" || d.DataType != "" || d.Constraint != "" {
					t.Fatal("unexpected authorization diagnostic fields", outcome.err)
				}
				if conflict {
					enginetest.TransactionRows(t, admin, "SELECT k FROM t", 1)
					enginetest.TransactionRows(t, admin, "SELECT v FROM t", 10)
					enginetest.TransactionRows(t, admin, "SELECT secret FROM t", 77)
				} else {
					enginetest.TransactionRows(t, admin, "SELECT k FROM t")
				}
				enginetest.LockEmpty(t, e)
			})
		}
	}
}

func generatedUnitDescriptor(t *testing.T) (*core.Engine, catalog.Read, *catalog.Relation) {
	t.Helper()
	e := engine.New()
	view := e.CatalogRead()
	integer, ok := view.LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("missing int4")
	}
	var namespace ids.ObjectAddress
	for address, descriptor := range view.Descriptors() {
		if descriptor.Kind() == ids.NamespaceObject && descriptor.Name() == "public" {
			namespace = address
			break
		}
	}
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 25000}, ObjectNamespace: namespace, ObjectName: "g", ObjectRevision: 1, Columns: []catalog.Column{{Name: "a", Usage: integer.Usage(), Nullable: true}, {Name: "b", Usage: integer.Usage(), Nullable: true}}, Attributes: []int32{1, 2}, ColumnMeta: catalog.ColumnMetadata{HighWater: 2}, Index: map[string]int{"a": 0, "b": 1}}
	return e, view, d
}
