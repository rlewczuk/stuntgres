package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestUpdateRowCorrelatedCTEFrames(t *testing.T) {
	e := engine.New()
	owner := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, owner, "CREATE TABLE t(id INT,a INT,b INT)")
	enginetest.SessionSQL(t, owner, "INSERT INTO t VALUES(1,10,20),(2,30,40)")
	ctx := context.Background()
	op, scope, b, _, sources := updateFromBoundSources(t, e, owner, ctx, "UPDATE t SET (a,b)=(WITH v AS MATERIALIZED (SELECT t.id AS x) SELECT x,x+100 FROM v)")
	defer op.Done()
	defer scope.Finish(context.Canceled)
	if err := exec.SelectQuerySources(ctx, sources); err != nil {
		t.Fatal(err)
	}
	source := b.Update.Assignments[0].(plan.SubqueryExpr)
	project := source.QueryPlan.Root.(*plan.ProjectNode)
	var producer *plan.ProjectNode
	for _, analysis := range project.AnalysisExpressions {
		if edge, ok := analysis.(plan.SubqueryExpr); ok && edge.CTEEdge {
			producer = edge.QueryPlan.Root.(*plan.ProjectNode)
			break
		}
	}
	if producer == nil || project.Frame != 2 || producer.Frame != 3 {
		t.Fatal("missing nested query frames", project.Frame, producer)
	}
	var target plan.ColumnExpr
	found := false
	plan.VisitCTEQuery(producer, func(plan.Node) {}, func(node expr.Expr) {
		if column, ok := node.(plan.ColumnExpr); ok && column.Origin.Address.ObjectID == b.Ref.Address.ObjectID {
			if column.Frame != 1 {
				t.Fatal("CTE correlation impersonated an empty intermediate SELECT frame", column.Frame)
			}
			target, found = column, true
		}
	}, true)
	if !found {
		t.Fatal("missing actual outer target recipe")
	}
	draws := 0
	subqueryTestTarget(t, producer, queryProbe(producer.Schema()[0].Usage, func(c *exec.Context) (sqltype.Value, error) {
		value, err := target.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		id, err := value.Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		draws++
		return sqltype.Integer(value.Type(), id+int64(draws*10))
	}))
	execution := exec.State{Source: sources, Input: scope.Input, Output: scope.Output}
	defer execution.ReleaseMemberships()
	eval := execution.Evaluation(ctx)
	typ := b.Relation.Columns[0].Usage.Type
	for i, id := range []int64{1, 2, 1} {
		old := []sqltype.Value{subqueryTestInteger(t, typ, id), subqueryTestInteger(t, typ, 7), subqueryTestInteger(t, typ, 8)}
		eval.Row = old
		row, err := dml.EvaluateUpdateAssignments(&eval, b.Update, b.Targets, old)
		want := id + int64((i+1)*10)
		if err != nil || enginetest.Export(row[0]) != id || enginetest.Export(row[1]) != want || enginetest.Export(row[2]) != want+100 || draws != i+1 {
			t.Fatal("producer tuple was stale, rebound or duplicated", row, draws, err)
		}
		if eval.UpdateOutputs != nil || eval.UpdateOutputsReady {
			t.Fatal("correlated invocation leaked tuple owner")
		}
	}
}
