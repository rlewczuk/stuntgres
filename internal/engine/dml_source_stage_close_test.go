package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

// Exercise the actual DML stage owners with admitted captured sources. SQL only
// supplies incidental catalog/row state; the injected required Close contract
// has no portable SQL callback counterpart.
func TestDMLSourceStageClose(t *testing.T) {
	for _, command := range []string{"update_from", "delete_using"} {
		for _, primary := range []bool{false, true} {
			phase := "close_veto"
			if primary {
				phase = "primary_preserved"
			}
			t.Run(command+"/"+phase, func(t *testing.T) {
				e := New()
				session := sessionStart(t, e, nil)
				for _, q := range []string{"CREATE TABLE t(id INT)", "CREATE TABLE s(k INT)", "INSERT INTO t VALUES(1)", "INSERT INTO s VALUES(1),(3)", "CREATE VIEW close_source AS SELECT k FROM s"} {
					sessionSQL(t, session, q)
				}
				q := "UPDATE t SET id=t.id+10 FROM close_source s WHERE t.id=s.k RETURNING t.id"
				if command == "delete_using" {
					q = "DELETE FROM t USING close_source s WHERE t.id=s.k RETURNING t.id"
				}
				op, scope, b, target, inputs := updateFromBoundSources(t, e, session, context.Background(), q)
				if err := exec.SelectQuerySources(op.Context(), inputs); err != nil {
					t.Fatal(err)
				}
				helper := exec.QuerySourceAt(inputs, 0)
				for helper.Input != nil {
					helper = helper.Input
				}
				if helper.NestedRoot == nil {
					t.Fatal("fixture lacks real captured view producer")
				}
				borrower := exec.State{Source: inputs, SourceMode: exec.SourceBorrowed}
				borrower.Input, borrower.Output = scope.WriteHost().Environments()
				reader, err := exec.OpenSourceStage(op.Context(), &borrower, helper, helper.NestedRoot.Schema())
				if err != nil {
					t.Fatal(err)
				}
				if _, present, err := reader.NextOccurrence(op.Context()); err != nil || !present {
					t.Fatal("view first occurrence", present, err)
				}
				if err := reader.Close(); err != nil {
					t.Fatal(err)
				}
				producer := helper.NestedStage
				cleanup := errors.New("required DML source stage close")
				failure := errors.New("primary DML returning expression")
				closes, projections := 0, 0
				finish := producer.Finish
				producer.Finish = func() error {
					closes++
					if finish != nil {
						if err := finish(); err != nil {
							return err
						}
					}
					return cleanup
				}
				original := b.Returning.Expressions[0]
				b.Returning.Expressions = []expr.Expr{queryProbe(b.Returning.Schema[0].Usage, func(c *exec.Context) (sqltype.Value, error) {
					projections++
					if primary {
						return sqltype.Value{}, failure
					}
					return original.Eval(c)
				})}
				var outcome dml.Outcome
				if command == "update_from" {
					outcome, err = dml.ExecuteUpdate(scope.WriteHost(), dml.UpdateInput{Context: op.Context(), Binding: b, Source: target, From: inputs, Schema: "public"})
				} else {
					outcome, err = dml.ExecuteDelete(scope.WriteHost(), dml.DeleteInput{Binding: b, Source: target, Inputs: inputs})
				}
				// Always reject the fixture's staged mutation, even while this regression
				// correctly detects that an ignored first Close returned false success.
				abort := err
				if abort == nil {
					abort = errors.New("fixture rejects missing required close veto")
				}
				if outcome.Rows != nil {
					outcome.Rows.Close()
				}
				finishErr := scope.Finish(abort)
				op.Done()
				want := cleanup
				if primary {
					want = failure
				}
				if !errors.Is(err, want) || outcome.Rows != nil || outcome.Count != 0 {
					t.Fatalf("DML close/primary result: err=%v want=%v count=%d rows=%v", err, want, outcome.Count, outcome.Rows)
				}
				if finishErr != abort || projections != 1 || closes != 1 || !producer.Closed || producer.Finish != nil || producer.Produce != nil {
					t.Fatal("failure precedence or producer disposal", finishErr, projections, closes, producer.Closed)
				}
				target.Close()
				inputs.Close()
				if closes != 1 {
					t.Fatal("duplicate capture disposal", closes)
				}
				transactionRows(t, session, "SELECT id FROM t", 1)
				lockEmpty(t, e)
			})
		}
	}
}
