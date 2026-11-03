package core_test

import (
	"context"
	"database/sql/driver"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestStatsXactBlocksExpression(t *testing.T) {
	t.Run("immutable_typed_protocol", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		for _, kind := range []functions.StatsXactBlocksKind{functions.XactBlocksFetched, functions.XactBlocksHit} {
			query := enginetest.BindSelect(t, e, op, "SELECT "+kind.Name()+"(0::oid)")
			node, ok := query.Root.(*plan.ProjectNode).Expressions[0].(functions.StatsXactBlocks)
			if !ok || node.Validate() != nil || node.Kind != kind || node.Argument.ResultUsage().Type.ID() != sqltype.Oid || node.ResultUsage().Type.ID() != sqltype.Int8 || node.RoutineReference == nil || uint32(node.RoutineReference.RoutineOID()) != 3044+uint32(kind) || node.IntrinsicVolatility() != expr.Volatile || node.PlanningFold() != expr.FoldNever || node.StrictNull() != expr.StrictAfterBare || node.PreflightFold() != expr.PreflightNullBare {
				t.Fatal("typed block inquiry ownership", node)
			}
			original := node.Argument
			children := node.Children()
			children[0] = nil
			if !reflect.DeepEqual(node.Argument, original) {
				t.Fatal("mutable borrowed child backing")
			}
			rebuilt, err := node.Rebuild(node.Info, []expr.Expr{original})
			if err != nil || !node.SameGroup(rebuilt, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, false) {
				t.Fatal("reconstruction", rebuilt, err)
			}
			if _, err := node.Rebuild(node.Info, nil); err == nil {
				t.Fatal("invalid child count admitted")
			}
			if _, err := node.Rebuild(node.Info, []expr.Expr{nil}); err == nil {
				t.Fatal("invalid OID child admitted")
			}
			rendering, err := node.Render()
			if err != nil || rendering.Name != kind.Name() || len(rendering.Args) != 1 || !reflect.DeepEqual(rendering.Args[0].Expr, original) {
				t.Fatal("rendered operand ownership", rendering, err)
			}
			capture, accepted, err := node.Capture(expr.CaptureFunction)
			if err != nil || !accepted || capture.Count != 1 || !capture.Indexed {
				t.Fatal("capture", capture, err)
			}
			if child, err := node.CaptureOperand(0); err != nil || !reflect.DeepEqual(child, original) {
				t.Fatal("captured child", child, err)
			}
			if _, err := node.CaptureOperand(1); err == nil {
				t.Fatal("invalid capture index admitted")
			}
			if node.ValidateStoredProvider() != nil {
				t.Fatal("stored provider rejected")
			}
			nullChild := plan.LiteralExpr{Info: expr.Info{Usage: node.Argument.ResultUsage()}, Value: sqltype.Null(node.Argument.ResultUsage().Type)}
			nullNode, err := node.Rebuild(node.Info, []expr.Expr{nullChild})
			if err != nil {
				t.Fatal(err)
			}
			pure := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
			if value, err := nullNode.Eval(pure); err != nil || !value.IsNull() || value.Type() != node.ResultUsage().Type {
				t.Fatal("runtime strict NULL borrowed statistics authority", value, err)
			}
			if _, err := node.Eval(pure); err == nil {
				t.Fatal("nonnull call fabricated missing runtime authority")
			}
			if !reflect.DeepEqual(node.Argument, original) {
				t.Fatal("rebuild changed original recipe")
			}
		}
	})
	t.Run("shared_strict_planning_and_current_frames", func(t *testing.T) {
		e, op, scope := strictNullUnitFixture(t)
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		enginetest.RoleExecute(t, e, op, "CREATE ROLE block_expression_reader LOGIN")
		var err error
		scope, err = e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { scope.Finish(nil) })
		arrayStrictNullAdmitScope(t, scope)
		reader, done := enginetest.RoleOperation(t, e, "block_expression_reader")
		defer done()
		for _, kind := range []functions.StatsXactBlocksKind{functions.XactBlocksFetched, functions.XactBlocksHit} {
			for _, planner := range []struct {
				name string
				run  func(*exec.Context, expr.Expr, bool) (expr.Expr, bool, error)
			}{{"conditional", exec.SimplifyConditional}, {"preflight", exec.PreflightCoercionsWithPolicy}} {
				t.Run(kind.Name()+"/"+planner.name, func(t *testing.T) {
					node, binder, evaluation := strictNullUnitBind(t, e, op, scope, kind.Name()+"(NULL::oid)")
					original := arrayStrictNullSnapshot(t, node)
					var routineRefFound bool
					for _, dependency := range binder.Dependencies {
						if uint32(dependency.Ref.Address.ObjectID) == 3044+uint32(kind) {
							routineRefFound = true
							if err := enginetest.SetObjectACL(e, context.Background(), dependency.Ref, authorization.ACLValue{Explicit: true}); err != nil {
								t.Fatal(err)
							}
						}
					}
					if !routineRefFound {
						t.Fatal("missing selected routine dependency")
					}
					evaluation.RoutineAuthority = eMustJSONBRoutineAuthority(t, e, reader)
					folded, changed, err := planner.run(&evaluation, node, true)
					literal, literalOK := folded.(plan.LiteralExpr)
					if err != nil || !changed || !literalOK || !literal.Value.IsNull() || literal.RoutineReference != nil || literal.ResultUsage() != node.ResultUsage() {
						t.Fatal("bare NULL retained invocation demand", folded, changed, err)
					}
					if err := view.ValidateExpressionJoinExecution(&evaluation, folded, evaluation.Catalog); err != nil {
						t.Fatal("erased NULL demanded revoked ACL", err)
					}
					if !reflect.DeepEqual(node, original) {
						t.Fatal("strict planning mutated retained recipe")
					}
					parameter, parameterBinder, frame := strictNullUnitBind(t, e, op, scope, kind.Name()+"($1::oid)")
					for _, incomplete := range [][]sqltype.Value{nil, {{}}} {
						frame.Parameters = incomplete
						planned, _, err := planner.run(&frame, parameter, true)
						if _, ok := planned.(functions.StatsXactBlocks); err != nil || !ok {
							t.Fatal("incomplete frame manufactured NULL", planned, err)
						}
					}
					frame.Parameters = []sqltype.Value{sqltype.Null(parameterBinder.Params.Parameters[1].Type)}
					folded, _, err = planner.run(&frame, parameter, true)
					if literal, ok := folded.(plan.LiteralExpr); err != nil || !ok || !literal.Value.IsNull() || literal.RoutineReference != nil {
						t.Fatal("current bound NULL was not erased", folded, err)
					}
					zero, err := sqltype.OIDValue(parameterBinder.Params.Parameters[1].Type, 0)
					if err != nil {
						t.Fatal(err)
					}
					frame.Parameters = []sqltype.Value{zero}
					planned, _, err := planner.run(&frame, parameter, true)
					if _, ok := planned.(functions.StatsXactBlocks); err != nil || !ok {
						t.Fatal("prior NULL frame contaminated current nonNULL execution", planned, err)
					}
					row, _, rowContext := strictNullUnitBind(t, e, op, scope, kind.Name()+"(v::oid)")
					rowContext.Row = []sqltype.Value{sqltype.Null(enginetest.Usage(e, sqltype.Int4).Type)}
					rowContext.RoutineAuthority = eMustJSONBRoutineAuthority(t, e, reader)
					planned, _, err = planner.run(&rowContext, row, true)
					if _, ok := planned.(functions.StatsXactBlocks); err != nil || !ok {
						t.Fatal("runtime column NULL was erased", planned, err)
					}
					err = view.ValidateExpressionJoinExecution(&rowContext, planned, rowContext.Catalog)
					enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function " + kind.Name()})
					bad, _, childContext := strictNullUnitBind(t, e, op, scope, kind.Name()+"((1/0)::oid)")
					_, _, err = planner.run(&childContext, bad, true)
					enginetest.Diagnostic(t, err, sqlstate.Error{Code: "22012", Message: "division by zero"})
				})
			}
		}
	})
	t.Run("live_forwarding_prepared_and_stored_ownership", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, s, "CREATE TABLE block_expression(k integer)", "INSERT INTO block_expression VALUES(1)")
		ref := enginetest.RoleTableRef(t, e, "block_expression")
		query := fmt.Sprintf("SELECT pg_stat_get_xact_blocks_fetched(%d::oid)", ref.Address.ObjectID)
		statsFunctionExec(t, s, "CREATE VIEW block_expression_view AS "+query)
		read := e.CatalogRead()
		viewRef := enginetest.CatalogRef(t, e, read, "block_expression_view")
		definition := stored.ViewOf(read.MustRelation(viewRef.Address))
		root := definition.Query().Root.(*plan.ProjectNode)
		backing, revision := &root.Expressions[0], definition.DefinitionRevision()
		node, ok := root.Expressions[0].(functions.StatsXactBlocks)
		if !ok || node.ValidateStoredProvider() != nil || node.RoutineReference == nil || node.RoutineReference.RoutineOID() != 3044 {
			t.Fatal("stored immutable provider", root.Expressions[0])
		}
		ast, description := enginetest.PreparedDescription(t, e, s, query)
		defer e.RetirePreparedDescription(&description)
		analysis := description.Analysis
		statsFunctionExec(t, s, "BEGIN")
		check := func(want int64) {
			t.Helper()
			result, err := enginetest.PreparedExecute(e, s, ast, &description)
			if err != nil {
				t.Fatal(err)
			}
			got := enginetest.Tuples(result)
			result.Rows.Close()
			if !reflect.DeepEqual(got, [][]driver.Value{{want}}) {
				t.Fatal("held recipe cached pending value", got, want)
			}
			if description.Analysis != analysis || stored.ViewOf(e.CatalogRead().MustRelation(viewRef.Address)) != definition || definition.Query().Root != root || &root.Expressions[0] != backing || definition.DefinitionRevision() != revision {
				t.Fatal("execution replaced immutable accepted definition")
			}
		}
		check(0)
		capture := bufferUnitCapture(t, e, s, "SELECT k FROM block_expression")
		_, stage := blocksUnitOpen(t, capture)
		ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(capture.scope)}}
		value, err := node.Eval(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := value.Integer(); err != nil || n != 1 {
			t.Fatal("real typed runtime forwarding", n, err)
		}
		stage.Close()
		capture.close(t)
		check(1)
		statsFunctionExec(t, s, "SELECT k FROM block_expression")
		check(2)
		statsFunctionExec(t, s, "ROLLBACK")
	})
}
