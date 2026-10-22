package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestMergeUpdateSourceCompletion(t *testing.T) {
	mergeUpdateSourceCompletion(t, false, false)
}

func TestMergeSourceUpdateSourceCompletion(t *testing.T) {
	t.Run("empty", func(t *testing.T) { mergeUpdateSourceCompletion(t, true, true) })
	t.Run("nonmatching", func(t *testing.T) { mergeUpdateSourceCompletion(t, true, false) })
}

func mergeUpdateSourceCompletion(t *testing.T, targetOnly, empty bool) {
	for _, phase := range []string{"next", "close", "primaryWins", "cancelAtClose"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(id INT,v INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(1,10)")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			q := "MERGE INTO t USING (VALUES(1),(2)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=20 WHEN NOT MATCHED THEN INSERT VALUES(s.id,30)"
			if targetOnly {
				q = "MERGE INTO t USING (SELECT 2 AS id WHERE FALSE) s ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=20"
				if !empty {
					q = "MERGE INTO t USING (VALUES(2),(2)) s(id) ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=20"
				}
			}
			op, scope, b, target, inputs := updateFromBoundSources(t, e, s, ctx, q)
			defer op.Done()
			defer target.Close()
			defer inputs.Close()
			typ := b.Relation.Columns[0].Usage.Type
			one, _ := sqltype.Integer(typ, 1)
			two, _ := sqltype.Integer(typ, 2)
			primary, cleanup := errors.New("late source Next"), errors.New("required source Close")
			cursor := &mergeInsertTestCursor{schema: b.Merge.Source.Schema(), rows: [][]sqltype.Value{{one}, {two}}}
			if targetOnly {
				cursor.rows = [][]sqltype.Value{{two}, {two}}
				if empty {
					cursor.rows = nil
				}
			}
			if phase == "next" || phase == "primaryWins" {
				cursor.nextError = primary
			}
			if phase == "close" || phase == "primaryWins" {
				cursor.closeError = cleanup
			}
			premature := false
			cursor.beforeClose = func() {
				lockState := e.locks.Inspect()
				for resource, entry := range lockState.Resources {
					if resource.ObjectRef == b.Ref && resource.Row != 0 && (len(entry.Holders) != 0 || len(entry.Pending) != 0) {
						premature = true
					}
				}
				if phase == "cancelAtClose" {
					cancel()
				}
			}
			state := &exec.State{Source: inputs, Input: scope.input, Output: scope.output}
			rows, err := AssembleMergeRows(scope.writeHost(), scope.mergeInput(ctx, b, target, nil, nil, "public"), cursor, state, false)
			want := primary
			if phase == "close" {
				want = cleanup
			}
			if phase == "cancelAtClose" {
				want = context.Canceled
			}
			if !errors.Is(err, want) || rows != nil || cursor.closed != 1 || len(scope.stage.rows) != 0 || premature {
				scope.Finish(err)
				t.Fatal("source completion escaped into mutation", err, cursor.closed, premature, scope.stage.rows)
			}
			scope.Finish(err)
			transactionRows(t, peer, "SELECT v FROM t", 10)
			lockEmpty(t, e)
		})
	}
}
