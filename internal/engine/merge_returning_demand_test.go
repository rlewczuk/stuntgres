package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestMergeReturningOutputDemand(t *testing.T) {
	t.Run("virtualSourceAndTarget", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		for _, q := range []string{"CREATE TABLE t(id INT,v INT,g INT GENERATED ALWAYS AS(v+1) VIRTUAL)", "INSERT INTO t(id,v) VALUES(1,10),(2,20)", "CREATE TABLE source(id INT,g INT GENERATED ALWAYS AS(7) VIRTUAL)", "INSERT INTO source(id) VALUES(1)"} {
			sessionSQL(t, s, q)
		}
		op, scope, b, target, inputs := updateFromBoundSources(t, e, s, context.Background(), "MERGE INTO t USING source src ON t.id=src.id WHEN MATCHED THEN UPDATE SET v=t.v+1 WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+1 RETURNING t.id,src.g,old.g,new.g,src")
		defer op.Done()
		defer target.Close()
		defer inputs.Close()
		if err := exec.SelectQuerySources(op.Context(), inputs); err != nil {
			t.Fatal(err)
		}
		state := &exec.State{Source: inputs, Input: scope.Input, Output: scope.Output}
		for _, expr := range b.Returning.Expressions {
			state.IdentityReads = state.IdentityReads || exec.BoundHasIdentityReads(expr)
		}
		if !state.IdentityReads {
			t.Fatal("output-only virtual demand absent")
		}
		cursor, err := exec.OpenNode(op.Context(), state, b.Merge.Source)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := dml.AssembleMergeRows(scope.WriteHost(), scope.mergeInput(op.Context(), b, target, nil, nil, "public"), &cursor, state, false)
		if err != nil {
			scope.Finish(err)
			t.Fatal(err)
		}
		defer rows.Returning.Close()
		var got [][]driver.Value
		values := make([]sqltype.Value, len(rows.Returning.Schema()))
		var retainedSource sqltype.Value
		for {
			present, nextErr := rows.Returning.Next(op.Context(), values)
			if nextErr != nil {
				t.Fatal(nextErr)
			}
			if !present {
				break
			}
			row := []driver.Value{testExport(values[0]), testExport(values[1]), testExport(values[2]), testExport(values[3]), nil}
			source := values[4]
			if source.Type() != rows.Returning.Schema()[4].Usage.Type || !source.Type().IsComposite() {
				t.Fatal("source whole-row descriptor", source.Type())
			}
			if !source.IsNull() {
				if source.CompositeLen() != 2 || testExport(source.CompositeField(0)) != int64(1) || testExport(source.CompositeField(1)) != int64(7) {
					t.Fatal("owned source whole-row fields", source)
				}
				text, codecErr := source.Type().OutputWithEnvironment(op.Context(), source, rows.Returning.OutputEnvironment())
				if codecErr != nil {
					t.Fatal("source whole-row codec", codecErr)
				}
				row[4] = text
				retainedSource = source
			}
			got = append(got, row)
		}
		// Advancing through the missing-source row must not overwrite the
		// completed present composite's owned fields.
		if retainedSource.Type() == nil || retainedSource.IsNull() || testExport(retainedSource.CompositeField(0)) != int64(1) || testExport(retainedSource.CompositeField(1)) != int64(7) {
			t.Fatal("source whole-row ownership lost")
		}
		want := [][]driver.Value{{int64(1), int64(7), int64(11), int64(12), "(1,7)"}, {int64(2), nil, int64(21), int64(22), nil}}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("drained source/transition recipe provenance", got, want)
		}
		if err = dml.StageMergeRows(scope.WriteHost(), rows, b.Ref); err != nil {
			scope.Finish(err)
			t.Fatal(err)
		}
		if err = scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		transactionRows(t, s, "SELECT g FROM t ORDER BY id", 12, 22)
		lockEmpty(t, e)
	})
	t.Run("outputCommentSnapshot", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		for _, q := range []string{"CREATE TABLE t(id INT)", "INSERT INTO t VALUES(1)", "COMMENT ON TABLE t IS 'table snapshot'", "COMMENT ON COLUMN t.id IS 'column snapshot'"} {
			sessionSQL(t, s, q)
		}
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		result, err := e.Execute(op, statement(t, "MERGE INTO t USING (VALUES(1)) src(id) ON t.id=src.id WHEN MATCHED THEN DELETE RETURNING col_description('t'::regclass,1),obj_description('t'::regclass,'pg_class'),'t'::regclass"), 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer result.Rows.Close()
		values := make([]sqltype.Value, 3)
		present, nextErr := result.Rows.Next(op.Context(), values)
		if nextErr != nil || !present {
			t.Fatal("comment output missing", present, nextErr)
		}
		regclass, codecErr := result.Rows.RegclassText(values[2])
		if codecErr != nil {
			t.Fatal("captured regclass output", codecErr)
		}
		got := [][]driver.Value{{testExport(values[0]), testExport(values[1]), regclass}}
		present, nextErr = result.Rows.Next(op.Context(), values)
		if nextErr != nil || present {
			t.Fatal("unexpected comment output tail", present, nextErr)
		}
		want := [][]driver.Value{{"column snapshot", "table snapshot", "t"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("MERGE output lost snapshot environment", got, want)
		}
	})
	t.Run("zeroMemberReadAndPresence", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE t()")
		sessionSQL(t, s, "INSERT INTO t DEFAULT VALUES")
		op, scope, b, target, inputs := updateFromBoundSources(t, e, s, context.Background(), "MERGE INTO t USING (VALUES(1)) src(x) ON TRUE WHEN MATCHED THEN DELETE RETURNING old,new,t")
		defer op.Done()
		defer target.Close()
		defer inputs.Close()
		if !b.Returning.ReadRequired {
			t.Fatal("zero-member target whole row lost SELECT demand")
		}
		c := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, MergeReturning: true, MergeAction: b.Merge.Arms[0].Action}}
		for i, expr := range b.Returning.Expressions {
			v, err := expr.Eval(&c)
			if err != nil || v.IsNull() != (i == 1) || v.Type().CompositeLen() != 0 {
				t.Fatal("zero-member presence", i, v, err)
			}
			if !analyze.ExpressionUsesFrame(expr, 1) {
				t.Fatal("zero-member owner correlation", i)
			}
		}
		if err := scope.Finish(errors.New("unit rollback")); err == nil {
			t.Fatal("rollback reason lost")
		}
	})
	t.Run("absentVirtualSourceShortCircuit", func(t *testing.T) {
		e := New()
		typ := testUsage(e, sqltype.Int4).Type
		value, _ := sqltype.Integer(typ, 1)
		calls := 0
		expr := plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage()}, Scope: 1, Frame: 4, Slot: 0, MergeSource: true, Virtual: queryProbe(typ.Usage(), func(*exec.Context) (sqltype.Value, error) { calls++; return value, nil })}
		parent := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 4, Row: []sqltype.Value{sqltype.Null(typ)}, MergeReturning: true}}
		child := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 8, Outer: &parent, MergeSourcePresent: true}}
		v, err := expr.Eval(&child)
		if err != nil || !v.IsNull() || calls != 0 {
			t.Fatal("absent source recipe invoked", v, err, calls)
		}
	})
}
