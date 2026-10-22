package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestMergeDeleteSourceCompletion(t *testing.T) {
	for _, phase := range []string{"next", "close", "primaryWins", "cancelAtClose"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(id INT,v INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(1,10),(2,20)")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, scope, b, target, inputs := updateFromBoundSources(t, e, s, ctx, "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN DELETE WHEN NOT MATCHED BY SOURCE THEN DELETE")
			defer op.Done()
			defer target.Close()
			defer inputs.Close()
			one, _ := sqltype.Integer(b.Relation.Columns[0].Usage.Type, 1)
			primary, cleanup := errors.New("late DELETE source Next"), errors.New("required DELETE source Close")
			cursor := &mergeInsertTestCursor{schema: b.Merge.Source.Schema(), rows: [][]sqltype.Value{{one}}}
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
			if !errors.Is(err, want) || rows != nil || cursor.closed != 1 || len(scope.stage.rows) != 0 || len(scope.stage.deletes) != 0 || premature {
				scope.Finish(err)
				t.Fatal("source failure escaped into deletion", err, cursor.closed, premature)
			}
			scope.Finish(err)
			transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20)
			lockEmpty(t, e)
		})
	}
}
