package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"testing"
)

func TestMergeNothingSelection(t *testing.T) {
	for _, pred := range []string{"TRUE", "FALSE", "NULL"} {
		t.Run(pred, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(id INT)")
			op, scope, b, target, source := updateFromBoundSources(t, e, s, context.Background(), "MERGE INTO t USING (VALUES(1)) s(id) ON TRUE WHEN MATCHED AND "+pred+" THEN DO NOTHING WHEN NOT MATCHED THEN DO NOTHING WHEN MATCHED THEN DELETE")
			defer op.Done()
			defer target.close()
			defer source.close()
			c := EvalContext{Context: op.Context(), Scope: 1, Frame: 1}
			arm, err := b.merge.selectArm(&c, parser.MergeMatched)
			want := parser.MergeDeleteAction
			if pred == "TRUE" {
				want = parser.MergeNothingAction
			}
			if err != nil || arm == nil || arm.action != want {
				t.Fatal("first true", arm, err)
			}
			arm, err = b.merge.selectArm(&c, parser.MergeNotMatched)
			if err != nil || arm == nil || arm.action != parser.MergeNothingAction {
				t.Fatal("class selection", arm, err)
			}
			arm, err = b.merge.selectArm(&c, parser.MergeNotMatchedBySource)
			if err != nil || arm != nil {
				t.Fatal("class widened", arm, err)
			}
			if err = scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestMergeNothingPreflight(t *testing.T) {
	for _, condition := range []string{"TRUE", "(SELECT 10/id FROM source)=1"} {
		t.Run(condition, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(id INT)")
			sessionSQL(t, s, "CREATE TABLE source(id INT)")
			sessionSQL(t, s, "INSERT INTO source VALUES(0)")
			op, scope, b, target, source := updateFromBoundSources(t, e, s, context.Background(), "MERGE INTO t USING source s ON TRUE WHEN MATCHED AND "+condition+" THEN DO NOTHING")
			defer op.Done()
			defer target.close()
			defer source.close()
			if !b.merge.hasNothing || len(b.merge.arms[0].input.expressions) != 0 || len(b.merge.arms[0].input.defaults) != 0 {
				t.Fatal("no-op mutation schedule", b.merge)
			}
			c := EvalContext{Context: op.Context(), Scope: 1, Frame: 1, Input: scope.input, Output: scope.output, catalog: scope.commentCatalog}
			if err := preflightMerge(&c, b.merge, scope.commentCatalog); err != nil {
				t.Fatal("no-op preflight indexed mutation input", err)
			}
			if condition != "TRUE" {
				if err := selectQuerySources(op.Context(), source); err != nil {
					t.Fatal(err)
				}
				query := &queryState{source: source, input: scope.input, output: scope.output}
				c.query = query
				_, err := b.merge.selectArm(&c, parser.MergeMatched)
				query.releaseMemberships()
				state(t, err, "22012")
			}
			if err := scope.Finish(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestMergeNothingPublication(t *testing.T) {
	for _, veto := range []bool{false, true} {
		t.Run(map[bool]string{false: "finish", true: "veto"}[veto], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(id INT,v INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(1,10)")
			op, scope, b, target, source := updateFromBoundSources(t, e, s, context.Background(), "MERGE INTO t USING (VALUES(1),(1)) s(id) ON t.id=s.id WHEN MATCHED THEN DO NOTHING RETURNING merge_action(),t.v")
			defer op.Done()
			defer target.close()
			defer source.close()
			if err := selectQuerySources(op.Context(), source); err != nil {
				t.Fatal(err)
			}
			query := &queryState{source: source, input: scope.input, output: scope.output}
			defer query.releaseMemberships()
			cursor, err := openQueryNode(op.Context(), query, b.merge.source)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := e.assembleMergeRows(op.Context(), scope, b, target, &cursor, query, "public", false)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows.actions) != 0 || len(rows.values) != 0 || len(rows.predecessors) != 0 || rows.returning == nil || rows.returning.count != 0 {
				t.Fatal("omission assembled mutation/output", rows)
			}
			defer rows.returning.Close()
			if err = rows.stage(scope.Writes(), b.ref); err != nil {
				t.Fatal(err)
			}
			if len(scope.stage.rows) != 0 {
				t.Fatal("no-op mutation stage", scope.stage.rows)
			}
			if len(b.returning.schema) != 2 || b.returning.schema[0].Name != "merge_action" || b.returning.schema[1].Name != "v" {
				t.Fatal("empty output schema", b.returning.schema)
			}
			var finish error
			if veto {
				finish = errors.New("finish veto")
			}
			if err = scope.Finish(finish); !errors.Is(err, finish) {
				t.Fatal(err, finish)
			}
			if scope.view != nil || scope.ticket != nil || !scope.finished {
				t.Fatal("Finish retained ownership")
			}
			transactionRows(t, peer, "SELECT v FROM t", 10)
			lockEmpty(t, e)
		})
	}
	t.Run("execute", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE t(id INT,v INT)")
		sessionSQL(t, s, "INSERT INTO t VALUES(1,10)")
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if !scope.finished {
				scope.Finish(errors.New("abandoned publication unit"))
			}
		}()
		result, err := scope.Execute(statement(t, "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN DO NOTHING RETURNING merge_action(),t.v"), 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		if result.Count != 0 || result.Rows == nil || len(scope.stage.rows) != 0 {
			t.Fatal("Execute published mutation", result)
		}
		defer result.Rows.Close()
		if err = scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		if len(tuples(result)) != 0 {
			t.Fatal("Execute projected omission")
		}
		if scope.view != nil || scope.ticket != nil || !scope.finished {
			t.Fatal("Execute retained ownership")
		}
	})
}
