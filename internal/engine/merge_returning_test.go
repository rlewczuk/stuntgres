package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestMergeReturningFrames(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(id INT,v INT)")
	op, scope, b, target, inputs := updateFromBoundSources(t, e, s, context.Background(), "MERGE INTO t USING (VALUES(1,7)) src(id,delta) ON t.id=src.id WHEN MATCHED THEN UPDATE SET v=t.v+src.delta RETURNING merge_action(),old.v,new.v,t,old,new,src")
	defer op.Done()
	defer target.close()
	defer inputs.close()
	integer := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(b.relation.columns[0].Usage.Type, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	row := []sqltype.Value{integer(1), integer(17), integer(1), integer(7)}
	old := []sqltype.Value{integer(1), integer(10)}
	owner := EvalContext{Context: op.Context(), Scope: 1, Frame: 1, Row: row, OldRow: old, mergeReturning: true, mergeSourcePresent: true}
	child := EvalContext{Context: op.Context(), Scope: 1, Frame: 8, Outer: &owner, mergeReturning: true, mergeAction: parser.MergeDeleteAction}
	for _, action := range []parser.MergeAction{parser.MergeInsertAction, parser.MergeUpdateAction, parser.MergeDeleteAction} {
		owner.mergeAction = action
		owner.OldRow = old
		if action == parser.MergeInsertAction {
			owner.OldRow = nil
		}
		v, err := b.returning.expressions[0].Eval(&child)
		want := map[parser.MergeAction]string{parser.MergeInsertAction: "INSERT", parser.MergeUpdateAction: "UPDATE", parser.MergeDeleteAction: "DELETE"}[action]
		if err != nil || testExport(v) != want {
			t.Fatal("lexical action owner", v, err, want)
		}
		for index, absent := range map[int]bool{1: action == parser.MergeInsertAction, 2: action == parser.MergeDeleteAction, 4: action == parser.MergeInsertAction, 5: action == parser.MergeDeleteAction} {
			v, err = b.returning.expressions[index].Eval(&child)
			if err != nil || v.IsNull() != absent {
				t.Fatal("transition presence", action, index, v, err)
			}
		}
	}
	owner.mergeAction = parser.MergeUpdateAction
	owner.OldRow = old
	whole := b.returning.expressions[4].(returningWholeRowExpression)
	calls := 0
	member := whole.members[0]
	whole.members[0] = queryProbe(member.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) { calls++; return member.Eval(c) })
	owner.mergeAction = parser.MergeInsertAction
	owner.OldRow = nil
	v, err := whole.Eval(&child)
	if err != nil || !v.IsNull() || calls != 0 {
		t.Fatal("absent transition evaluated recipe", v, err, calls)
	}
	owner.mergeAction = parser.MergeUpdateAction
	owner.OldRow = []sqltype.Value{sqltype.Null(row[0].Type()), sqltype.Null(row[1].Type())}
	v, err = whole.Eval(&child)
	if err != nil || v.IsNull() || calls != 1 {
		t.Fatal("present all-NULL row collapsed", v, err, calls)
	}
	owner.mergeSourcePresent = false
	v, err = b.returning.expressions[6].Eval(&child)
	if err != nil || !v.IsNull() {
		t.Fatal("missing source whole row", v, err)
	}
	child.Outer = nil
	if _, err = b.returning.expressions[0].Eval(&child); err == nil {
		t.Fatal("missing lexical owner accepted")
	}
	if err = scope.Finish(errors.New("unit rollback")); err == nil {
		t.Fatal("rollback reason lost")
	}
}

func TestMergeReturningPublication(t *testing.T) {
	for _, phase := range []string{"success", "outputLate", "producerClose", "typeDrift", "finishVeto", "noReturning"} {
		t.Run(phase, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			peer := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(id INT,v INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(1,10),(2,20)")
			q := "MERGE INTO t USING (VALUES(1),(2)) src(id) ON t.id=src.id WHEN MATCHED THEN UPDATE SET v=t.v+1"
			if phase != "noReturning" {
				q += " RETURNING old.v,new.v"
			}
			op, scope, b, target, inputs := updateFromBoundSources(t, e, s, context.Background(), q)
			defer op.Done()
			defer target.close()
			defer inputs.close()
			one, _ := sqltype.Integer(b.relation.columns[0].Usage.Type, 1)
			two, _ := sqltype.Integer(one.Type(), 2)
			injected := errors.New("private output/producer veto")
			cursor := &mergeInsertTestCursor{schema: b.merge.source.Schema(), rows: [][]sqltype.Value{{one}, {two}}}
			if phase == "producerClose" {
				cursor.closeError = injected
			}
			calls := 0
			if b.returning != nil {
				b.returning.expressions[1] = queryProbe(b.returning.schema[1].Usage, func(c *EvalContext) (sqltype.Value, error) {
					calls++
					if cursor.closed != 1 || len(scope.stage.rows) != 0 || c.mergeAction != parser.MergeUpdateAction {
						t.Fatal("projection crossed source/publication boundary", cursor.closed, calls)
					}
					if phase == "outputLate" && calls == 2 {
						return sqltype.Value{}, injected
					}
					if phase == "typeDrift" {
						return sqltype.Null(testUsage(e, sqltype.Text).Type), nil
					}
					return c.Row[1], nil
				})
			}
			state := &queryState{source: inputs, input: scope.input, output: scope.output}
			rows, err := e.assembleMergeRows(op.Context(), scope, b, target, cursor, state, "public", false)
			if phase == "producerClose" || phase == "outputLate" || phase == "typeDrift" {
				if rows != nil || err == nil {
					t.Fatal("failed private output escaped", rows, err)
				}
				if phase != "typeDrift" && !errors.Is(err, injected) {
					t.Fatal(err)
				}
				if phase == "producerClose" && calls != 0 || phase == "outputLate" && calls != 2 {
					t.Fatal("evaluation timing", phase, calls)
				}
				scope.Finish(err)
				transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20)
				lockEmpty(t, e)
				return
			}
			if err != nil || len(rows.actions) != 2 {
				t.Fatal(rows, err)
			}
			if phase == "noReturning" {
				if rows.returning != nil || calls != 0 {
					t.Fatal("collector on no-output path")
				}
			} else {
				defer rows.returning.Close()
				if rows.returning.count != 2 || calls != 2 {
					t.Fatal("accepted output count", calls, rows.returning.count)
				}
			}
			if err = rows.stage(scope.Writes(), b.ref); err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20)
			if phase == "finishVeto" {
				err = injected
			}
			if got := scope.Finish(err); !errors.Is(got, err) {
				t.Fatal("Finish outcome", got, err)
			}
			if phase == "finishVeto" {
				transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 10, 20)
			} else {
				transactionRows(t, peer, "SELECT v FROM t ORDER BY id", 11, 21)
			}
			if phase == "success" && !reflect.DeepEqual(tuples(Result{Rows: rows.returning}), [][]driver.Value{{int64(10), int64(11)}, {int64(20), int64(21)}}) {
				t.Fatal("completed output changed")
			}
			if scope.view != nil || scope.ticket != nil || !scope.finished {
				t.Fatal("Finish retained owners")
			}
			lockEmpty(t, e)
		})
	}
}
