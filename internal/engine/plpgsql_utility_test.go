package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPLExceptionUtilityPublication(t *testing.T) {
	for _, completed := range []bool{false, true} {
		for _, sessionCompleted := range []bool{false, true} {
			for _, outcome := range []struct {
				name string
				err  error
			}{{"success", nil}, {"ordinary_error", errors.New("nested body failure")}, {"cancellation", context.Canceled}} {
				t.Run(fmt.Sprintf("parent_%t_%t/%s", completed, sessionCompleted, outcome.name), func(t *testing.T) {
					e := New()
					sess := sessionStart(t, e, nil)
					sessionSQL(t, sess, "CREATE TABLE ex_log(v integer)")
					sessionSQL(t, sess, "CREATE FUNCTION ex_put(x integer) RETURNS integer LANGUAGE SQL AS $$INSERT INTO ex_log VALUES(x); SELECT x$$")
					scope := foreignActionUnitScope(t, e, sess, "INSERT INTO ex_log VALUES(1)")
					ref := catalogRef(t, e, e.CatalogRead(), "ex_log")
					prefix := scope.Stage.Rows[ref.Address][0]
					scope.Completed, *ScopeSessionCompletedRef(scope) = completed, sessionCompleted
					restore := ScopeActivateSQLRoutine(scope, nil, e.CatalogRead())
					if scope.Completed || ScopeSessionCompleted(scope) {
						t.Fatal("child inherited parent's completion outcomes")
					}
					boolean, ok := e.CatalogRead().LookupType(sqltype.Bool)
					if !ok {
						t.Fatal("missing boolean")
					}
					program := &proc.PLProgram{FoundSlot: 0, Variables: []proc.PLVariable{
						{Name: "found", Usage: boolean.Usage(), Role: proc.PLFoundSlot},
					}}
					frame := proc.NewPLFrame(program, &proc.Definition{}, &proc.Frame{}, sqltype.CollationRef{})
					defer frame.Close()
					syntax, err := parser.ParsePLpgSQL("BEGIN RETURN ex_put(2); END", parser.PLReturnPolicy{})
					if err != nil {
						t.Fatal(err)
					}
					expression := syntax.Statements[0].Expression
					if _, _, _, err := ScopeEvaluatePLExpression(scope, frame, &parser.Select{Targets: []parser.Expr{expression}, Pos: expression.SourcePosition()}, e.CatalogRead(), nil, NewPLNoticeOrigin(PLNoticeOriginFields{})); err != nil {
						t.Fatal(err)
					}
					nestedRestore := ScopeActivateSQLRoutine(scope, nil, e.CatalogRead())
					if scope.Completed || ScopeSessionCompleted(scope) {
						t.Fatal("nested child did not start with reset completion flags")
					}
					if _, err := EngineExecuteStatement(e, scope, statement(t, "SET application_name TO 'child'"), 0, nil); err != nil || !scope.Completed {
						t.Fatal("actual child utility failed to install completion", err)
					}
					if err := nestedRestore(nil); err != nil || scope.Completed || ScopeSessionCompleted(scope) {
						t.Fatal("nested utility outcome leaked into its parent", err)
					}
					if errors.Is(outcome.err, context.Canceled) {
						ctx, cancel := context.WithCancel(scope.Op.Context())
						scope.Op.Ctx = ctx
						cancel()
					}
					if err := restore(outcome.err); !errors.Is(err, outcome.err) || scope.Completed != completed || ScopeSessionCompleted(scope) != sessionCompleted {
						t.Fatal("activation unwind changed saved parent outcomes", err, scope.Completed, ScopeSessionCompleted(scope))
					}
					rows := scope.Stage.Rows[ref.Address]
					if len(rows) != 2 || rows[0].Version != prefix.Version || rows[0].Row != prefix.Row {
						t.Fatal("lexical unwind restored away actual staged writes", rows)
					}
					value, err := rows[1].Values[0].Integer()
					if err != nil || value != 2 || sess.Settings.Values["application_name"] != "child" {
						t.Fatal("lexical unwind lost real child effects", value, err)
					}
				})
			}
		}
	}
	t.Run("genuine_completed_root_survives_late_cancellation", func(t *testing.T) {
		e := New()
		sess := sessionStart(t, e, nil)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		op, scope := discardUnitScope(t, sess, ctx, parser.DiscardPlans)
		defer op.Done()
		defer scope.Finish(context.Canceled)
		if _, err := ScopeExecuteDiscard(scope, &parser.Discard{Category: parser.DiscardPlans}, 0); err != nil || !scope.Completed || !ScopeSessionCompleted(scope) || sess.DiscardPlansGeneration != 1 {
			t.Fatal("root utility did not actually install its outcome", err)
		}
		restore := ScopeActivateSQLRoutine(scope, nil, e.CatalogRead())
		if scope.Completed || ScopeSessionCompleted(scope) {
			t.Fatal("completed root outcome leaked into child")
		}
		if _, err := EngineExecuteStatement(e, scope, statement(t, "SET application_name TO 'child'"), 0, nil); err != nil {
			t.Fatal(err)
		}
		if err := restore(nil); err != nil || !scope.Completed || !ScopeSessionCompleted(scope) {
			t.Fatal("child unwind removed genuine root installation fence", err)
		}
		cancel()
		if err := scope.Finish(nil); err != nil || sess.DiscardPlansGeneration != 1 || sess.Settings.Values["application_name"] != "child" {
			t.Fatal("late cancellation retracted or replayed completed root effects", err)
		}
		if err := scope.Finish(nil); err != nil || sess.DiscardPlansGeneration != 1 {
			t.Fatal("second Finish changed installed outcome", err)
		}
	})
}
