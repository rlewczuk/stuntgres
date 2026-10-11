package engine

import (
	"context"
	"errors"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestMergeUpdateSourceCompletion(t *testing.T) {
	for _, phase := range []string{"next", "close", "primaryWins", "cancelAtClose"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(id INT,v INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(1,10)")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, scope, b, target, inputs := updateFromBoundSources(t, e, s, ctx, "MERGE INTO t USING (VALUES(1),(2)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=20 WHEN NOT MATCHED THEN INSERT VALUES(s.id,30)")
			defer op.Done()
			defer target.close()
			defer inputs.close()
			typ := b.relation.columns[0].Usage.Type
			one, _ := sqltype.Integer(typ, 1)
			two, _ := sqltype.Integer(typ, 2)
			primary, cleanup := errors.New("late source Next"), errors.New("required source Close")
			cursor := &mergeInsertTestCursor{schema: b.merge.source.Schema(), rows: [][]sqltype.Value{{one}, {two}}}
			if phase == "next" || phase == "primaryWins" {
				cursor.nextError = primary
			}
			if phase == "close" || phase == "primaryWins" {
				cursor.closeError = cleanup
			}
			premature := false
			cursor.beforeClose = func() {
				e.locks.mu.Lock()
				for resource, entry := range e.locks.entries {
					if resource.ObjectRef == b.ref && resource.row != 0 && (len(entry.holders) != 0 || len(entry.pending) != 0) {
						premature = true
					}
				}
				e.locks.mu.Unlock()
				if phase == "cancelAtClose" {
					cancel()
				}
			}
			state := &queryState{source: inputs, input: scope.input, output: scope.output}
			rows, err := e.assembleMergeRows(ctx, scope, b, target, cursor, state, "public", false)
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
